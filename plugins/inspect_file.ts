import {readFile, realpath, stat} from "node:fs/promises";
import path from "node:path";

import type {SyntaxNode, Tree} from "@lezer/common";
import {parser as goParser} from "@lezer/go";
import {parser as javascriptParser} from "@lezer/javascript";
import {parser as jsonParser} from "@lezer/json";
import {parser as markdownParser} from "@lezer/markdown";
import {parser as pythonParser} from "@lezer/python";
import {isMap, isScalar, parseDocument} from "yaml";

import type {Tool} from "../internal/router/toolplugin/plugin.d.ts";
import {byteLength, createExecutorTool, errorText, stripOptionalFinalNewline} from "./common.ts";
import {inspectFileShapeSchemaJSON} from "./inspect_file_schema.ts";

const OUTPUT_BYTES = 64 * 1024;

type ErrorCode =
  | "usage"
  | "not_found"
  | "not_regular"
  | "not_utf8"
  | "outside_workspace"
  | "read"
  | "parse"
  | "output_limit";

type Language = "go" | "javascript" | "typescript" | "python";
type FileKind = "code" | "markdown" | "json" | "none";
type ValueType = "object" | "array" | "string" | "number" | "boolean" | "null";

type CodeEntry = {
  kind: "import" | "constant" | "variable" | "type" | "class" | "function";
  name: string;
  line: number;
  line_end: number;
};

type MethodEntry = {
  kind: "method";
  name: string;
  receiver: string;
  line: number;
  line_end: number;
};

type HeadingEntry = {
  kind: "heading";
  name: string;
  level: number;
  line: number;
  line_end: number;
};

type FrontmatterEntry = {
  kind: "frontmatter";
  name: string;
  line: number;
  line_end: number;
};

type JSONEntry = {
  kind: "json";
  pointer: string;
  value_type: ValueType;
  line: number;
  line_end: number;
};

type OutlineEntry = CodeEntry | MethodEntry | HeadingEntry | FrontmatterEntry | JSONEntry;
type LocatedEntry = {entry: OutlineEntry; offset: number; order: number};

const typescriptParser = javascriptParser.configure({dialect: "ts"});

const goDeclarationProjections: Record<string, {node: string; name: string; kind: CodeEntry["kind"]}> = {
  ConstDecl: {node: "ConstSpec", name: "DefName", kind: "constant"},
  VarDecl: {node: "VarSpec", name: "DefName", kind: "variable"},
  TypeDecl: {node: "TypeSpec", name: "DefName", kind: "type"},
};

const javascriptDeclarationProjections: Record<string, {name: string; kind: CodeEntry["kind"]}> = {
  FunctionDeclaration: {name: "VariableDefinition", kind: "function"},
  ClassDeclaration: {name: "VariableDefinition", kind: "class"},
  TypeAliasDeclaration: {name: "TypeDefinition", kind: "type"},
  InterfaceDeclaration: {name: "TypeDefinition", kind: "type"},
  EnumDeclaration: {name: "TypeDefinition", kind: "type"},
};

const javascriptMethodNameNodes = new Set([
  "PrivatePropertyDefinition",
  "Number",
  "String",
  "PropertyDefinition",
  "PropertyName",
]);

type InspectionData = {
  path: string;
  kind: FileKind;
  language: Language | null;
  size_bytes: number;
  line_count: number | null;
  parse_complete: boolean;
  outline: OutlineEntry[];
};

class InspectFailure extends Error {
  constructor(readonly code: ErrorCode, message: string) {
    super(message);
  }
}

class LineMap {
  readonly starts = [0];

  constructor(readonly source: string) {
    for (let offset = 0; offset < source.length; offset += 1) {
      if (source[offset] === "\r") {
        if (source[offset + 1] === "\n") {
          offset += 1;
        }
        this.starts.push(offset + 1);
      } else if (source[offset] === "\n") {
        this.starts.push(offset + 1);
      }
    }
    if (this.starts.at(-1) === source.length && source.length > 0) {
      this.starts.pop();
    }
  }

  get count(): number {
    return this.source.length === 0 ? 0 : this.starts.length;
  }

  lineAt(offset: number): number {
    let low = 0;
    let high = this.starts.length;
    while (low + 1 < high) {
      const middle = Math.floor((low + high) / 2);
      if (this.starts[middle] <= offset) {
        low = middle;
      } else {
        high = middle;
      }
    }
    return low + 1;
  }

  range(node: SyntaxNode): {line: number; line_end: number} {
    return {
      line: this.lineAt(node.from),
      line_end: this.lineAt(Math.max(node.from, node.to - 1)),
    };
  }
}

function children(node: SyntaxNode): SyntaxNode[] {
  const result: SyntaxNode[] = [];
  for (let child = node.firstChild; child !== null; child = child.nextSibling) {
    result.push(child);
  }
  return result;
}

function descendants(node: SyntaxNode, name: string): SyntaxNode[] {
  const result: SyntaxNode[] = [];
  const visit = (current: SyntaxNode): void => {
    if (current.name === name) {
      result.push(current);
    }
    for (const child of children(current)) {
      visit(child);
    }
  };
  visit(node);
  return result;
}

function firstDescendant(node: SyntaxNode, names: ReadonlySet<string>): SyntaxNode | null {
  if (names.has(node.name)) {
    return node;
  }
  for (const child of children(node)) {
    const found = firstDescendant(child, names);
    if (found !== null) {
      return found;
    }
  }
  return null;
}

function nodeText(source: string, node: SyntaxNode): string {
  return source.slice(node.from, node.to);
}

function hasParseError(tree: Tree): boolean {
  let found = false;
  tree.iterate({
    enter(node) {
      if (node.type.isError) {
        found = true;
        return false;
      }
    },
  });
  return found;
}

function addNamedEntries(
  output: LocatedEntry[],
  source: string,
  lines: LineMap,
  declaration: SyntaxNode,
  names: SyntaxNode[],
  kind: CodeEntry["kind"],
  rangeNode = declaration,
): void {
  for (const nameNode of names) {
    const name = nodeText(source, nameNode);
    if (name !== "") {
      output.push({
        entry: {kind, name, ...lines.range(rangeNode)},
        offset: rangeNode.from,
        order: output.length,
      });
    }
  }
}

function decodeGoImportPath(source: string, node: SyntaxNode): string | null {
  const literal = nodeText(source, node);
  if (literal.startsWith("`") && literal.endsWith("`")) {
    return literal.slice(1, -1).replaceAll("\r", "");
  }
  try {
    const decoded = JSON.parse(literal);
    return typeof decoded === "string" ? decoded : null;
  } catch {
    return null;
  }
}

function goOutline(source: string, lines: LineMap, tree: Tree): LocatedEntry[] {
  const output: LocatedEntry[] = [];
  for (const declaration of children(tree.topNode)) {
    if (declaration.name === "ImportDecl") {
      for (const specification of descendants(declaration, "ImportSpec")) {
        const pathNode = firstDescendant(specification, new Set(["String"]));
        if (pathNode === null) {
          continue;
        }
        const name = decodeGoImportPath(source, pathNode);
        if (name !== null && name !== "") {
          output.push({
            entry: {kind: "import", name, ...lines.range(specification)},
            offset: specification.from,
            order: output.length,
          });
        }
      }
      continue;
    }

    const projected = goDeclarationProjections[declaration.name];
    if (projected !== undefined) {
      for (const specification of descendants(declaration, projected.node)) {
        addNamedEntries(
          output,
          source,
          lines,
          declaration,
          descendants(specification, projected.name),
          projected.kind,
          specification,
        );
      }
      continue;
    }

    if (declaration.name === "FunctionDecl") {
      const name = firstDescendant(declaration, new Set(["DefName"]));
      if (name !== null) {
        addNamedEntries(output, source, lines, declaration, [name], "function");
      }
      continue;
    }

    if (declaration.name === "MethodDecl") {
      const name = firstDescendant(declaration, new Set(["FieldName"]));
      const receiverParameters = children(declaration).find((child) => child.name === "Parameters");
      const parameter = receiverParameters === undefined
        ? null
        : firstDescendant(receiverParameters, new Set(["Parameter"]));
      const receiverNode = parameter === null
        ? null
        : firstDescendant(parameter, new Set(["PointerType", "ParameterizedType", "TypeName"]));
      if (name !== null && receiverNode !== null) {
        const receiver = nodeText(source, receiverNode).replace(/\s+/gu, "");
        if (receiver !== "") {
          output.push({
            entry: {
              kind: "method",
              name: nodeText(source, name),
              receiver,
              ...lines.range(declaration),
            },
            offset: declaration.from,
            order: output.length,
          });
        }
      }
    }
  }
  return output;
}

function unwrapExport(node: SyntaxNode): SyntaxNode {
  if (node.name !== "ExportDeclaration") {
    return node;
  }
  return children(node).find((child) => /Declaration$/u.test(child.name)) ?? node;
}

function bindingNamesBeforeInitializers(declaration: SyntaxNode): SyntaxNode[] {
  const names: SyntaxNode[] = [];
  let binding = true;
  for (const child of children(declaration)) {
    if (child.name === "Equals") {
      binding = false;
      continue;
    }
    if (child.name === ",") {
      binding = true;
      continue;
    }
    if (binding) {
      names.push(...descendants(child, "VariableDefinition"));
    }
  }
  return names;
}

function javascriptMethodName(source: string, method: SyntaxNode): string | null {
  const parts = children(method);
  const parametersIndex = parts.findIndex((child) => child.name === "ParamList");
  if (parametersIndex < 0) {
    return null;
  }
  const beforeParameters = parts.slice(0, parametersIndex);
  for (let index = beforeParameters.length - 1; index >= 0; index -= 1) {
    if (beforeParameters[index].name !== "]") {
      continue;
    }
    for (let open = index - 1; open >= 0; open -= 1) {
      if (beforeParameters[open].name === "[") {
        return source.slice(beforeParameters[open].from, beforeParameters[index].to);
      }
    }
  }
  const nameNode = beforeParameters.findLast((child) => javascriptMethodNameNodes.has(child.name));
  return nameNode === undefined ? null : nodeText(source, nameNode);
}

function javascriptOutline(source: string, lines: LineMap, tree: Tree): LocatedEntry[] {
  const output: LocatedEntry[] = [];
  for (const topLevel of children(tree.topNode)) {
    const declaration = unwrapExport(topLevel);
    if (declaration.name === "ImportDeclaration") {
      const bindings = descendants(declaration, "VariableDefinition");
      if (bindings.length > 0) {
        addNamedEntries(output, source, lines, declaration, bindings, "import");
      } else {
        const moduleNode = firstDescendant(declaration, new Set(["String"]));
        if (moduleNode !== null) {
          try {
            const name = JSON.parse(nodeText(source, moduleNode));
            if (typeof name === "string" && name !== "") {
              output.push({
                entry: {kind: "import", name, ...lines.range(declaration)},
                offset: declaration.from,
                order: output.length,
              });
            }
          } catch {
            // A recovered invalid string contributes no fabricated module name.
          }
        }
      }
      continue;
    }

    if (declaration.name === "VariableDeclaration") {
      const declarationKind = children(declaration)[0]?.name === "const" ? "constant" : "variable";
      addNamedEntries(
        output,
        source,
        lines,
        declaration,
        bindingNamesBeforeInitializers(declaration),
        declarationKind,
      );
      continue;
    }

    const projected = javascriptDeclarationProjections[declaration.name];
    if (projected === undefined) {
      continue;
    }
    const nameNode = firstDescendant(declaration, new Set([projected.name]));
    if (nameNode === null) {
      continue;
    }
    addNamedEntries(output, source, lines, declaration, [nameNode], projected.kind, topLevel);
    if (projected.kind !== "class") {
      continue;
    }
    const className = nodeText(source, nameNode);
    const body = children(declaration).find((child) => child.name === "ClassBody");
    if (body === undefined) {
      continue;
    }
    for (const method of children(body).filter((child) => child.name === "MethodDeclaration")) {
      const methodName = javascriptMethodName(source, method);
      if (methodName !== null && methodName !== "") {
        output.push({
          entry: {
            kind: "method",
            name: methodName,
            receiver: className,
            ...lines.range(method),
          },
          offset: method.from,
          order: output.length,
        });
      }
    }
  }
  return output;
}

function pythonImportNames(source: string, declaration: SyntaxNode): string[] {
  const parts = children(declaration);
  const importIndex = parts.findLastIndex((child) => child.name === "import");
  if (importIndex < 0) {
    return [];
  }

  const names: string[] = [];
  let segment: SyntaxNode[] = [];
  const addSegment = (): void => {
    const aliasIndex = segment.findIndex((child) => child.name === "as");
    const candidates = aliasIndex < 0 ? segment : segment.slice(aliasIndex + 1);
    const binding = candidates.find((child) => child.name === "VariableName");
    if (binding !== undefined) {
      names.push(nodeText(source, binding));
    }
    segment = [];
  };

  for (const part of parts.slice(importIndex + 1)) {
    if (part.name === ",") {
      addSegment();
    } else {
      segment.push(part);
    }
  }
  addSegment();
  return names;
}

function pythonBindingNames(node: SyntaxNode): SyntaxNode[] {
  if (node.name === "VariableName") {
    return [node];
  }
  if (node.name === "MemberExpression" || node.name === "TypeDef") {
    return [];
  }
  return children(node).flatMap(pythonBindingNames);
}

function pythonAssignmentNames(declaration: SyntaxNode): SyntaxNode[] {
  const names: SyntaxNode[] = [];
  let segment: SyntaxNode[] = [];
  for (const child of children(declaration)) {
    if (child.name === "AssignOp") {
      names.push(...segment.flatMap(pythonBindingNames));
      segment = [];
    } else {
      segment.push(child);
    }
  }
  return names;
}

function pythonDeclaration(node: SyntaxNode): SyntaxNode {
  if (node.name !== "DecoratedStatement") {
    return node;
  }
  return children(node).find((child) =>
    child.name === "FunctionDefinition" || child.name === "ClassDefinition",
  ) ?? node;
}

function pythonOutline(source: string, lines: LineMap, tree: Tree): LocatedEntry[] {
  const output: LocatedEntry[] = [];
  for (const topLevel of children(tree.topNode)) {
    const declaration = pythonDeclaration(topLevel);
    if (declaration.name === "ImportStatement") {
      for (const name of pythonImportNames(source, declaration)) {
        output.push({
          entry: {kind: "import", name, ...lines.range(declaration)},
          offset: declaration.from,
          order: output.length,
        });
      }
      continue;
    }
    if (declaration.name === "AssignStatement") {
      addNamedEntries(
        output,
        source,
        lines,
        declaration,
        pythonAssignmentNames(declaration),
        "variable",
      );
      continue;
    }
    if (declaration.name !== "FunctionDefinition" && declaration.name !== "ClassDefinition") {
      continue;
    }
    const nameNode = firstDescendant(declaration, new Set(["VariableName"]));
    if (nameNode === null) {
      continue;
    }
    const kind = declaration.name === "ClassDefinition" ? "class" : "function";
    addNamedEntries(output, source, lines, declaration, [nameNode], kind, topLevel);
    if (kind !== "class") {
      continue;
    }
    const className = nodeText(source, nameNode);
    const body = children(declaration).find((child) => child.name === "Body");
    if (body === undefined) {
      continue;
    }
    for (const candidate of children(body)) {
      const method = pythonDeclaration(candidate);
      if (method.name !== "FunctionDefinition") {
        continue;
      }
      const methodName = firstDescendant(method, new Set(["VariableName"]));
      if (methodName !== null) {
        output.push({
          entry: {
            kind: "method",
            name: nodeText(source, methodName),
            receiver: className,
            ...lines.range(candidate),
          },
          offset: candidate.from,
          order: output.length,
        });
      }
    }
  }
  return output;
}

function markdownFrontmatter(
  source: string,
  lines: LineMap,
): {endOffset: number | null; entries: LocatedEntry[]; parseComplete: boolean} {
  if (lines.count === 0) {
    return {endOffset: null, entries: [], parseComplete: true};
  }
  const logicalLines = source.split(/\r\n|\r|\n/u);
  if (logicalLines.at(-1) === "" && /(?:\r\n|\r|\n)$/u.test(source)) {
    logicalLines.pop();
  }
  if (logicalLines[0] !== "---") {
    return {endOffset: null, entries: [], parseComplete: true};
  }
  const closingLine = logicalLines.indexOf("---", 1);
  if (closingLine < 1) {
    return {endOffset: null, entries: [], parseComplete: true};
  }

  const contentStart = lines.starts[1] ?? source.length;
  const contentEnd = lines.starts[closingLine] ?? source.length;
  const document = parseDocument(source.slice(contentStart, contentEnd));
  const entries: LocatedEntry[] = [];
  if (isMap(document.contents)) {
    for (const pair of document.contents.items) {
      const key = pair.key;
      if (!isScalar(key) || key.range === undefined || key.value === null || typeof key.value === "object") {
        continue;
      }
      const name = String(key.value);
      if (name === "") {
        continue;
      }
      const start = contentStart + key.range[0];
      const end = contentStart + Math.max(key.range[0], key.range[1] - 1);
      entries.push({
        entry: {
          kind: "frontmatter",
          name,
          line: lines.lineAt(start),
          line_end: lines.lineAt(end),
        },
        offset: start,
        order: entries.length,
      });
    }
  }
  return {
    endOffset: lines.starts[closingLine + 1] ?? source.length,
    entries,
    parseComplete: document.errors.length === 0,
  };
}

function markdownOutline(
  source: string,
  lines: LineMap,
  tree: Tree,
): {entries: LocatedEntry[]; parseComplete: boolean} {
  const frontmatter = markdownFrontmatter(source, lines);
  const output = [...frontmatter.entries];
  tree.iterate({
    enter(node) {
      if (frontmatter.endOffset !== null && node.from < frontmatter.endOffset) {
        return;
      }
      const match = node.name.match(/^ATXHeading([1-6])$/u);
      if (match === null) {
        return;
      }
      const raw = nodeText(source, node);
      const heading = raw
        .replace(/^ {0,3}#{1,6}(?:[ \t]+|$)/u, "")
        .replace(/\r$/u, "")
        .replace(/[ \t]+#+[ \t]*$/u, "")
        .trim();
      if (heading !== "") {
        output.push({
          entry: {
            kind: "heading",
            name: heading,
            level: Number(match[1]),
            ...lines.range(node),
          },
          offset: node.from,
          order: output.length,
        });
      }
    },
  });
  return {entries: output, parseComplete: frontmatter.parseComplete};
}

const jsonValueTypes: Record<string, ValueType> = {
  Object: "object",
  Array: "array",
  String: "string",
  Number: "number",
  True: "boolean",
  False: "boolean",
  Null: "null",
};

function decodePropertyName(source: string, node: SyntaxNode): string | null {
  try {
    const decoded = JSON.parse(nodeText(source, node));
    return typeof decoded === "string" ? decoded : null;
  } catch {
    return null;
  }
}

function containsParseError(node: SyntaxNode): boolean {
  if (node.type.isError) {
    return true;
  }
  return children(node).some(containsParseError);
}

function pointerSegment(value: string): string {
  return value.replaceAll("~", "~0").replaceAll("/", "~1");
}

function jsonOutline(source: string, lines: LineMap, tree: Tree): LocatedEntry[] {
  const output: LocatedEntry[] = [];
  const addValue = (node: SyntaxNode, pointer: string): void => {
    const valueType = jsonValueTypes[node.name];
    if (valueType === undefined) {
      return;
    }
    output.push({
      entry: {kind: "json", pointer, value_type: valueType, ...lines.range(node)},
      offset: node.from,
      order: output.length,
    });

    if (node.name === "Object") {
      for (const property of children(node).filter((child) => child.name === "Property")) {
        const parts = children(property);
        const nameIndex = parts.findIndex((child) => child.name === "PropertyName");
        const colonIndex = parts.findIndex((child) => child.name === ":");
        const valueIndex = parts.findIndex(
          (child, index) => index > colonIndex && jsonValueTypes[child.name] !== undefined,
        );
        if (
          nameIndex < 0
          || colonIndex <= nameIndex
          || valueIndex <= colonIndex
          || parts.slice(nameIndex + 1, valueIndex).some(containsParseError)
        ) {
          continue;
        }
        const name = decodePropertyName(source, parts[nameIndex]);
        if (name !== null) {
          addValue(parts[valueIndex], `${pointer}/${pointerSegment(name)}`);
        }
      }
    } else if (node.name === "Array") {
      let index = 0;
      for (const child of children(node)) {
        if (child.name === ",") {
          index += 1;
        } else if (jsonValueTypes[child.name] !== undefined) {
          addValue(child, `${pointer}/${index}`);
        }
      }
    }
  };

  const root = children(tree.topNode).find((child) => jsonValueTypes[child.name] !== undefined);
  if (root !== undefined) {
    addValue(root, "");
  }
  return output;
}

function ordered(entries: LocatedEntry[]): OutlineEntry[] {
  return entries
    .sort((left, right) => left.offset - right.offset || left.order - right.order)
    .map(({entry}) => entry);
}

function supportedFormat(extension: string): {kind: Exclude<FileKind, "none">; language: Language | null} | null {
  switch (extension) {
    case ".go":
      return {kind: "code", language: "go"};
    case ".js":
      return {kind: "code", language: "javascript"};
    case ".ts":
      return {kind: "code", language: "typescript"};
    case ".py":
      return {kind: "code", language: "python"};
    case ".md":
      return {kind: "markdown", language: null};
    case ".json":
      return {kind: "json", language: null};
    default:
      return null;
  }
}

function parseContent(
  source: string,
  format: {kind: Exclude<FileKind, "none">; language: Language | null},
): {parseComplete: boolean; outline: OutlineEntry[]; lineCount: number} {
  const lines = new LineMap(source);
  try {
    if (format.language === "go") {
      const tree = goParser.parse(source);
      return {parseComplete: !hasParseError(tree), outline: ordered(goOutline(source, lines, tree)), lineCount: lines.count};
    }
    if (format.language === "javascript" || format.language === "typescript") {
      const parser = format.language === "typescript" ? typescriptParser : javascriptParser;
      const tree = parser.parse(source);
      return {parseComplete: !hasParseError(tree), outline: ordered(javascriptOutline(source, lines, tree)), lineCount: lines.count};
    }
    if (format.language === "python") {
      const tree = pythonParser.parse(source);
      return {parseComplete: !hasParseError(tree), outline: ordered(pythonOutline(source, lines, tree)), lineCount: lines.count};
    }
    if (format.kind === "markdown") {
      const tree = markdownParser.parse(source);
      const outline = markdownOutline(source, lines, tree);
      return {
        parseComplete: !hasParseError(tree) && outline.parseComplete,
        outline: ordered(outline.entries),
        lineCount: lines.count,
      };
    }
    const tree = jsonParser.parse(source);
    return {parseComplete: !hasParseError(tree), outline: ordered(jsonOutline(source, lines, tree)), lineCount: lines.count};
  } catch (error) {
    throw new InspectFailure("parse", `parser failed: ${errorText(error)}`);
  }
}

function failure(pathValue: string | null, code: ErrorCode, message: string): string {
  return `${JSON.stringify({ok: false, path: pathValue, error: {code, message}})}\n`;
}

function success(data: InspectionData): string {
  const complete = `${JSON.stringify({ok: true, data, truncated: false, truncation: null})}\n`;
  if (byteLength(complete) <= OUTPUT_BYTES) {
    return complete;
  }
  if (data.outline.length === 0) {
    throw new InspectFailure("output_limit", "minimum success result exceeds the output byte limit");
  }

  const render = (count: number): string => `${JSON.stringify({
    ok: true,
    data: {...data, outline: data.outline.slice(0, count)},
    truncated: true,
    truncation: {reason: "output_bytes", after_entries: count},
  })}\n`;

  let low = 0;
  let high = data.outline.length - 1;
  let selected = -1;
  while (low <= high) {
    const middle = Math.floor((low + high) / 2);
    if (byteLength(render(middle)) <= OUTPUT_BYTES) {
      selected = middle;
      low = middle + 1;
    } else {
      high = middle - 1;
    }
  }
  if (selected < 0) {
    throw new InspectFailure("output_limit", "minimum success result exceeds the output byte limit");
  }
  return render(selected);
}

function normalizeInputPath(input: string): string {
  if (input === "" || input.includes("\0")) {
    throw new InspectFailure("usage", "inspect_file expects exactly one usable path");
  }
  if (path.isAbsolute(input)) {
    throw new InspectFailure("outside_workspace", "path must be workspace-relative");
  }
  const normalized = path.normalize(input);
  if (
    normalized === ".."
    || normalized.startsWith(`..${path.sep}`)
    || normalized === "@shell"
    || normalized.startsWith(`@shell${path.sep}`)
  ) {
    throw new InspectFailure("outside_workspace", "path escapes the workspace");
  }
  return normalized;
}

function isOutside(root: string, target: string): boolean {
  const relative = path.relative(root, target);
  return relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative);
}

function filesystemFailure(error: unknown): InspectFailure {
  if (error instanceof InspectFailure) {
    return error;
  }
  if (error instanceof Error && "code" in error && error.code === "ENOENT") {
    return new InspectFailure("not_found", "path does not exist");
  }
  return new InspectFailure("read", `cannot inspect path: ${errorText(error)}`);
}

async function inspect(input: string): Promise<InspectionData> {
  const normalized = normalizeInputPath(input);
  let workspace: string;
  let canonicalTarget: string;
  try {
    workspace = await realpath(process.cwd());
    canonicalTarget = await realpath(path.resolve(workspace, normalized));
  } catch (error) {
    throw filesystemFailure(error);
  }
  if (isOutside(workspace, canonicalTarget)) {
    throw new InspectFailure("outside_workspace", "path resolves outside the workspace");
  }

  let info;
  try {
    info = await stat(canonicalTarget);
  } catch (error) {
    throw filesystemFailure(error);
  }
  if (!info.isFile()) {
    throw new InspectFailure("not_regular", "path is not a regular file");
  }

  const resultPath = normalized.split(path.sep).join("/");
  const format = supportedFormat(path.extname(normalized));
  if (format === null) {
    return {
      path: resultPath,
      kind: "none",
      language: null,
      size_bytes: info.size,
      line_count: null,
      parse_complete: true,
      outline: [],
    };
  }

  let bytes: Uint8Array;
  try {
    bytes = await readFile(canonicalTarget);
  } catch (error) {
    throw filesystemFailure(error);
  }
  let source: string;
  try {
    source = new TextDecoder("utf-8", {fatal: true}).decode(bytes);
  } catch {
    throw new InspectFailure("not_utf8", "supported file is not valid UTF-8");
  }
  const parsed = parseContent(source, format);
  return {
    path: resultPath,
    kind: format.kind,
    language: format.language,
    size_bytes: bytes.byteLength,
    line_count: parsed.lineCount,
    parse_complete: parsed.parseComplete,
    outline: parsed.outline,
  };
}

function inspectFileInput(input: string): string[] {
  const value = stripOptionalFinalNewline(input);
  if (value === "") {
    return [];
  }
  if (value.startsWith("\"")) {
    try {
      const parsed = JSON.parse(value);
      return typeof parsed === "string" ? [parsed] : [];
    } catch {
      return [];
    }
  }
  return [value];
}

export const inspectFileDescription = `Use \`inspect_file PATH\` through \`shell\` for bounded file metadata and structural outlines.
It returns exact JSON for jq and never raw excerpts, bodies, field definitions, frontmatter values, or JSON scalar values.
Use \`hread\` before editing because inspect_file line numbers are not HPATCH targets.
Reason carefully about the path and make sure the command matches the \`inspect_file PATH\` syntax.

Result shape schema:
${inspectFileShapeSchemaJSON}`;

export function createInspectFileTool(description: string, grammar: string): Tool<string[]> {
  return createExecutorTool({
    name: "inspect_file",
    description,
    grammar,
    argv: inspectFileInput,
    async execute(argv) {
      const suppliedPath = argv.length === 1 ? argv[0] : null;
      if (argv.length !== 1) {
        return {
          stdout: failure(null, "usage", "inspect_file expects exactly one path"),
          exitCode: 1,
        };
      }
      try {
        const stdout = success(await inspect(argv[0]));
        return {stdout, stock: {stdout, exitCode: 0}, exitCode: 0};
      } catch (error) {
        const cause = error instanceof InspectFailure
          ? error
          : new InspectFailure("read", `cannot inspect file: ${errorText(error)}`);
        return {stdout: failure(suppliedPath, cause.code, cause.message), exitCode: 1};
      }
    },
  });
}
