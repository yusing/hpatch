import {spawn} from "node:child_process";
import {readFile, realpath, stat} from "node:fs/promises";
import path from "node:path";
import {fileURLToPath} from "node:url";

import type {SyntaxNode} from "@lezer/common";
import {parser as goParser} from "@lezer/go";

import type {ExecutionResult, Tool} from "../internal/router/toolplugin/plugin.d.ts";
import {
  byteLength,
  createExecutorTool,
  decodeUTF8,
  errorText,
  formatHashLine,
  hashLine,
  isOutsideWorkspace,
  stripOptionalFinalNewline,
  VERIFIED_ROW_LIMIT_DIAGNOSTIC,
  VerifiedRowOutput,
} from "./common.ts";
import {goDeclarationRange, LineMap} from "./inspect_file.ts";

type QueryMode = "def" | "refs";

type Query = {
  mode: QueryMode;
  path: string;
  line: number;
  hash: string;
  identifier: string;
  occurrence: number | null;
};

type SourceFile = {
  path: string;
  source: string;
  lines: LineMap;
};

type SourceFailureReason = "outside workspace" | "unavailable" | "not regular" | "not Go" | "not UTF-8";

type DefinitionLocation = {
  path: string;
  line: number;
  startOffset: number;
  endOffset: number;
};

type ReferenceLocation = {
  path: string;
  line: number;
};

type GoplsResult = {
  stdout: string;
  stderr: string;
};

const goKeywords = new Set([
  "break", "default", "func", "interface", "select",
  "case", "defer", "go", "map", "struct",
  "chan", "else", "goto", "package", "switch",
  "const", "fallthrough", "if", "range", "type",
  "continue", "for", "import", "return", "var",
]);

class HSymbolFailure extends Error {}

class SourceFailure extends Error {
  constructor(readonly reason: SourceFailureReason, message: string) {
    super(message);
  }
}

function parsePositiveInteger(value: string, label: string): number {
  if (!/^[1-9][0-9]*$/u.test(value)) {
    throw new HSymbolFailure(`${label} must be a positive decimal integer`);
  }
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) {
    throw new HSymbolFailure(`${label} is too large`);
  }
  return parsed;
}

function validGoIdentifier(value: string): boolean {
  return /^(?:_|\p{L})(?:_|\p{L}|\p{Nd})*$/u.test(value) && !goKeywords.has(value);
}

function parseQuery(argv: string[]): Query {
  if (argv.length !== 4 && argv.length !== 5) {
    throw new HSymbolFailure("usage: hsymbol (def|refs) PATH LINE:HASH IDENT [N]");
  }
  const [mode, inputPath, row, identifier, occurrenceText] = argv;
  if (mode !== "def" && mode !== "refs") {
    throw new HSymbolFailure("mode must be def or refs");
  }
  if (inputPath === "" || inputPath.includes("\0")) {
    throw new HSymbolFailure("path must be usable");
  }
  const rowMatch = /^([1-9][0-9]*):([0-9a-f]{4})$/u.exec(row);
  if (rowMatch === null) {
    throw new HSymbolFailure("row must be LINE:HASH with a positive line and lowercase four-digit hash");
  }
  if (!validGoIdentifier(identifier)) {
    throw new HSymbolFailure("IDENT must be a non-keyword Go identifier");
  }
  return {
    mode,
    path: inputPath,
    line: parsePositiveInteger(rowMatch[1], "line"),
    hash: rowMatch[2],
    identifier,
    occurrence: occurrenceText === undefined ? null : parsePositiveInteger(occurrenceText, "N"),
  };
}

function sourceFailure(error: unknown): SourceFailure {
  if (error instanceof SourceFailure) {
    return error;
  }
  if (error instanceof Error && "code" in error && error.code === "ENOENT") {
    return new SourceFailure("unavailable", "path does not exist");
  }
  return new SourceFailure("unavailable", `cannot read path: ${errorText(error)}`);
}

async function loadSource(
  workspace: string,
  inputPath: string,
  cache: Map<string, SourceFile>,
): Promise<SourceFile> {
  const resolved = path.resolve(workspace, inputPath);
  if (isOutsideWorkspace(workspace, resolved)) {
    throw new SourceFailure("outside workspace", "path is outside the workspace");
  }
  let canonicalPath: string;
  try {
    canonicalPath = await realpath(resolved);
  } catch (error) {
    throw sourceFailure(error);
  }
  if (isOutsideWorkspace(workspace, canonicalPath)) {
    throw new SourceFailure("outside workspace", "path resolves outside the workspace");
  }
  const cached = cache.get(canonicalPath);
  if (cached !== undefined) {
    return cached;
  }
  if (path.extname(canonicalPath) !== ".go") {
    throw new SourceFailure("not Go", "path is not a Go source file");
  }
  let info;
  try {
    info = await stat(canonicalPath);
  } catch (error) {
    throw sourceFailure(error);
  }
  if (!info.isFile()) {
    throw new SourceFailure("not regular", "path is not a regular file");
  }
  let bytes: Uint8Array;
  try {
    bytes = await readFile(canonicalPath);
  } catch (error) {
    throw sourceFailure(error);
  }
  let source: string;
  try {
    source = decodeUTF8(bytes, "source file");
  } catch {
    throw new SourceFailure("not UTF-8", "path is not UTF-8");
  }
  const loaded = {path: canonicalPath, source, lines: new LineMap(source)};
  cache.clear();
  cache.set(canonicalPath, loaded);
  return loaded;
}

function identifierOffsets(file: SourceFile, line: number, identifier: string): number[] {
  const logicalLine = file.lines.logicalLine(line);
  if (logicalLine === null) {
    return [];
  }
  const offsets: number[] = [];
  const visit = (node: SyntaxNode): void => {
    if (node.to <= logicalLine.from || node.from >= logicalLine.to) {
      return;
    }
    if (node.firstChild === null) {
      if (
        !node.type.isError
        && node.from >= logicalLine.from
        && node.to <= logicalLine.to
        && file.source.slice(node.from, node.to) === identifier
      ) {
        offsets.push(node.from);
      }
      return;
    }
    for (let child = node.firstChild; child !== null; child = child.nextSibling) {
      visit(child);
    }
  };
  visit(goParser.parse(file.source).topNode);
  return offsets;
}

function selectIdentifier(file: SourceFile, query: Query): number {
  const logicalLine = file.lines.logicalLine(query.line);
  if (logicalLine === null) {
    throw new HSymbolFailure(`line ${query.line} is past EOF`);
  }
  if (hashLine(logicalLine.text) !== query.hash) {
    throw new HSymbolFailure(`stale row ${query.line}:${query.hash}`);
  }
  const offsets = identifierOffsets(file, query.line, query.identifier);
  if (offsets.length === 0) {
    throw new HSymbolFailure(`${query.identifier} is not an identifier token on the verified line`);
  }
  if (query.occurrence === null) {
    if (offsets.length !== 1) {
      throw new HSymbolFailure(`${query.identifier} is ambiguous on the verified line; supply N`);
    }
    return offsets[0];
  }
  const selected = offsets[query.occurrence - 1];
  if (selected === undefined) {
    throw new HSymbolFailure(`identifier occurrence ${query.occurrence} is missing`);
  }
  return selected;
}

async function collect(stream: AsyncIterable<Uint8Array>): Promise<Uint8Array> {
  const chunks: Uint8Array[] = [];
  let length = 0;
  for await (const chunk of stream) {
    chunks.push(chunk);
    length += chunk.byteLength;
  }
  return Buffer.concat(chunks, length);
}

function conciseGoplsError(stderr: string, exitCode: number | null): string {
  const line = stderr.trim().split(/\r?\n/u).find((candidate) => candidate.trim() !== "");
  return line === undefined ? `gopls exited with status ${exitCode ?? "unknown"}` : line.trim();
}

async function runGopls(mode: QueryMode, position: string): Promise<GoplsResult> {
  const argumentsValue = mode === "def"
    ? ["definition", "-json", position]
    : ["references", "-d", position];
  const child = spawn("gopls", argumentsValue, {stdio: ["ignore", "pipe", "pipe"]});
  const completion = new Promise<{exitCode: number | null; error?: Error}>((resolve) => {
    child.once("error", (error) => resolve({exitCode: null, error}));
    child.once("close", (exitCode) => resolve({exitCode}));
  });
  const stdoutPromise = collect(child.stdout);
  const stderrPromise = collect(child.stderr);
  try {
    const completed = await completion;
    if (completed.error !== undefined) {
      await Promise.allSettled([stdoutPromise, stderrPromise]);
      if ("code" in completed.error && completed.error.code === "ENOENT") {
        throw new HSymbolFailure("gopls is unavailable");
      }
      throw new HSymbolFailure(`cannot start gopls: ${errorText(completed.error)}`);
    }
    const [stdoutBytes, stderrBytes] = await Promise.all([stdoutPromise, stderrPromise]);
    const stdout = decodeUTF8(stdoutBytes, "gopls stdout");
    const stderr = decodeUTF8(stderrBytes, "gopls stderr");
    if (completed.exitCode !== 0) {
      throw new HSymbolFailure(conciseGoplsError(stderr, completed.exitCode));
    }
    return {stdout, stderr};
  } catch (error) {
    child.kill("SIGKILL");
    await completion;
    if (error instanceof HSymbolFailure) {
      throw error;
    }
    throw new HSymbolFailure(`gopls query failed: ${errorText(error)}`);
  }
}

function parseDefinition(stdout: string): DefinitionLocation {
  let value: unknown;
  try {
    value = JSON.parse(stdout);
  } catch (error) {
    throw new HSymbolFailure(`invalid gopls definition output: ${errorText(error)}`);
  }
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new HSymbolFailure("invalid gopls definition output");
  }
  const span = (value as {span?: unknown}).span;
  if (span === null || typeof span !== "object" || Array.isArray(span)) {
    throw new HSymbolFailure("invalid gopls definition span");
  }
  const uri = (span as {uri?: unknown}).uri;
  const start = (span as {start?: unknown}).start;
  const end = (span as {end?: unknown}).end;
  if (
    typeof uri !== "string"
    || start === null || typeof start !== "object" || Array.isArray(start)
    || end === null || typeof end !== "object" || Array.isArray(end)
  ) {
    throw new HSymbolFailure("invalid gopls definition span");
  }
  const line = (start as {line?: unknown}).line;
  const startOffset = (start as {offset?: unknown}).offset;
  const endOffset = (end as {offset?: unknown}).offset;
  if (
    !Number.isSafeInteger(line) || (line as number) < 1
    || !Number.isSafeInteger(startOffset) || (startOffset as number) < 0
    || !Number.isSafeInteger(endOffset) || (endOffset as number) < (startOffset as number)
  ) {
    throw new HSymbolFailure("invalid gopls definition span");
  }
  let definitionPath: string;
  try {
    definitionPath = fileURLToPath(uri);
  } catch {
    throw new SourceFailure("outside workspace", "definition is not a workspace file");
  }
  return {
    path: definitionPath,
    line: line as number,
    startOffset: startOffset as number,
    endOffset: endOffset as number,
  };
}

function parseReferences(stdout: string): ReferenceLocation[] {
  const locations: ReferenceLocation[] = [];
  for (const rawLine of stdout.split("\n")) {
    const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
    if (line === "") {
      continue;
    }
    const match = /^(.*):([1-9][0-9]*):([1-9][0-9]*)-([1-9][0-9]*)$/u.exec(line);
    if (match === null) {
      throw new HSymbolFailure("invalid gopls references output");
    }
    locations.push({path: match[1], line: parsePositiveInteger(match[2], "reference line")});
  }
  return locations;
}

function verifiedSourceRow(workspace: string, file: SourceFile, line: number): string | null {
  const logicalLine = file.lines.logicalLine(line);
  return logicalLine === null
    ? null
    : `${JSON.stringify(path.relative(workspace, file.path))}:${formatHashLine(line, logicalLine.text)}`;
}

function skippedDiagnostic(skipped: Map<SourceFailureReason, number>): string {
  const parts = [...skipped].map(([reason, count]) => `${count} ${count === 1 ? "location" : "locations"} ${reason}`);
  return parts.length === 0 ? "" : `hsymbol: skipped ${parts.join(", ")}\n`;
}

async function executeQuery(query: Query): Promise<ExecutionResult> {
  let workspace: string;
  try {
    workspace = await realpath(process.cwd());
  } catch (error) {
    throw new HSymbolFailure(`cannot resolve workspace: ${errorText(error)}`);
  }
  const cache = new Map<string, SourceFile>();
  let inputFile: SourceFile;
  try {
    inputFile = await loadSource(workspace, query.path, cache);
  } catch (error) {
    const failure = sourceFailure(error);
    throw new HSymbolFailure(failure.message);
  }
  const identifierOffset = selectIdentifier(inputFile, query);
  const position = `${inputFile.path}:#${byteLength(inputFile.source.slice(0, identifierOffset))}`;
  const gopls = await runGopls(query.mode, position);
  cache.clear();
  let currentInput: SourceFile;
  try {
    currentInput = await loadSource(workspace, inputFile.path, cache);
  } catch {
    throw new HSymbolFailure("input changed during query");
  }
  if (currentInput.source !== inputFile.source) {
    throw new HSymbolFailure("input changed during query");
  }
  const stock = {
    stdout: gopls.stdout,
    ...(gopls.stderr === "" ? {} : {stderr: gopls.stderr}),
    exitCode: 0,
  };
  const output = new VerifiedRowOutput();
  const skipped = new Map<SourceFailureReason, number>();
  const skip = (reason: SourceFailureReason): void => {
    skipped.set(reason, (skipped.get(reason) ?? 0) + 1);
  };

  if (query.mode === "def") {
    try {
      const location = parseDefinition(gopls.stdout);
      const file = await loadSource(workspace, location.path, cache);
      const declaration = goDeclarationRange(file.source, location.startOffset, location.endOffset);
      const startLine = declaration?.line ?? location.line;
      const endLine = declaration?.line_end ?? location.line;
      for (let line = startLine; line <= endLine; line += 1) {
        const row = verifiedSourceRow(workspace, file, line);
        if (row === null) {
          skip("unavailable");
          break;
        }
        if (!output.append(row, "")) {
          break;
        }
      }
    } catch (error) {
      if (!(error instanceof SourceFailure)) {
        throw error;
      }
      skip(error.reason);
    }
  } else {
    const seen = new Set<string>();
    for (const location of parseReferences(gopls.stdout)) {
      try {
        const file = await loadSource(workspace, location.path, cache);
        const row = verifiedSourceRow(workspace, file, location.line);
        if (row === null) {
          throw new SourceFailure("unavailable", "reference line is unavailable");
        }
        const key = `${file.path}\0${location.line}`;
        if (seen.has(key)) {
          continue;
        }
        seen.add(key);
        if (!output.incomplete) {
          output.append(row, "");
        }
      } catch (error) {
        const failure = sourceFailure(error);
        skip(failure.reason);
      }
    }
  }

  let stderr = gopls.stderr;
  if (stderr !== "" && !stderr.endsWith("\n")) {
    stderr += "\n";
  }
  stderr += skippedDiagnostic(skipped);
  if (query.mode === "def" && output.current === "" && !output.incomplete) {
    stderr += "hsymbol: definition has no editable workspace location\n";
    return {
      ...(stderr === "" ? {} : {stderr}),
      stock,
      exitCode: 1,
    };
  }
  if (output.incomplete) {
    stderr += `hsymbol: ${VERIFIED_ROW_LIMIT_DIAGNOSTIC}`;
  }
  return {
    stdout: output.current,
    ...(stderr === "" ? {} : {stderr}),
    stock,
    exitCode: output.incomplete ? 1 : 0,
  };
}

export function createHSymbolTool(description: string, grammar: string): Tool<string[]> {
  return createExecutorTool({
    name: "hsymbol",
    description,
    grammar,
    argv(input) {
      const value = stripOptionalFinalNewline(input).trim();
      return value === "" ? [] : value.split(/[ \t]+/u);
    },
    async execute(argv) {
      try {
        return await executeQuery(parseQuery(argv));
      } catch (error) {
        const message = error instanceof HSymbolFailure ? error.message : errorText(error);
        return {stderr: `hsymbol: ${message}\n`, exitCode: 1};
      }
    },
  });
}
