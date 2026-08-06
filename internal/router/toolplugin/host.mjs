import { registerHooks } from "node:module";
import path from "node:path";
import process from "node:process";
import { fileURLToPath, pathToFileURL } from "node:url";

const API_VERSION = "hpatch-tool-plugin/v1";
const MAX_PLUGIN_TOOLS = 32;
const MAX_ID_BYTES = 128;
const MAX_DESCRIPTION_BYTES = 16 * 1024;
const MAX_GRAMMAR_BYTES = 1024 * 1024;
const MAX_INPUT_BYTES = 1024 * 1024;
const MAX_ARGV_ITEMS = 256;
const MAX_ARG_BYTES = 64 * 1024;
const MAX_DIAGNOSTIC_BYTES = 16 * 1024;
const MAX_EXECUTION_OUTPUT_BYTES = 16 * 1024 * 1024;
const identifierPattern = /^[A-Za-z][A-Za-z0-9._-]*$/;
const toolNamePattern = /^[A-Za-z_][A-Za-z0-9_-]*$/;
const ruleHeaderPattern = /^[!?]?([A-Za-z_][A-Za-z0-9_]*)\s*:\s*(.*)$/s;
const priorityHeaderPattern = /^[!?]?[A-Za-z_][A-Za-z0-9_]*\.-?[0-9]+\s*:/;
const commonImportPattern = /^%import\s+common\.[A-Za-z_][A-Za-z0-9_]*(?:\s*->\s*[A-Za-z_][A-Za-z0-9_]*)?$/;

const shellCommandNames = new Set([
  "alias", "bg", "bind", "break", "builtin", "caller", "case", "cd", "command",
  "compgen", "complete", "compopt", "continue", "coproc", "declare", "dirs", "disown",
  "do", "done", "echo", "elif", "else", "enable", "esac", "eval", "exec", "exit",
  "export", "false", "fc", "fg", "fi", "for", "function", "getopts", "hash", "help",
  "history", "if", "in", "jobs", "kill", "let", "local", "logout", "mapfile", "popd",
  "printf", "pushd", "pwd", "read", "readarray", "readonly", "return", "select", "set",
  "shift", "shopt", "source", "suspend", "test", "then", "time", "times", "trap",
  "true", "type", "typeset", "ulimit", "umask", "unalias", "unset", "until", "wait",
  "while",
]);

function byteLength(value) {
  return Buffer.byteLength(value, "utf8");
}

function exactKeys(value, allowed) {
  const actual = Object.keys(value).sort();
  const expected = [...allowed].sort();
  return actual.length === expected.length && actual.every((key, index) => key === expected[index]);
}

function inside(root, candidate) {
  const relative = path.relative(root, candidate);
  return relative === "" || (!relative.startsWith(`..${path.sep}`) && relative !== ".." && !path.isAbsolute(relative));
}

function errorText(error) {
  const text = error?.message ?? String(error);
  const encoded = Buffer.from(text, "utf8");
  if (encoded.length <= MAX_DIAGNOSTIC_BYTES) {
    return text;
  }
  return encoded.subarray(0, MAX_DIAGNOSTIC_BYTES).toString("utf8");
}

function validateRegexGrammar(definition) {
  if (/[\r\n]/.test(definition)) {
    return "regex grammar must be one line";
  }
  for (const unsupported of ["(?=", "(?!", "(?<=", "(?<!", "*?", "+?", "??"]) {
    if (definition.includes(unsupported)) {
      return `regex grammar uses unsupported construct ${JSON.stringify(unsupported)}`;
    }
  }

  let groups = 0;
  let inClass = false;
  let escaped = false;
  let canRepeat = false;
  for (let index = 0; index < definition.length; index += 1) {
    const character = definition[index];
    if (escaped) {
      if (/[1-9]/.test(character)) {
        return "regex grammar uses an unsupported backreference";
      }
      escaped = false;
      canRepeat = true;
      continue;
    }
    if (character === "\\") {
      escaped = true;
      continue;
    }
    if (inClass) {
      if (character === "]") {
        inClass = false;
        canRepeat = true;
      }
      continue;
    }
    if (character === "[") {
      inClass = true;
      canRepeat = false;
      continue;
    }
    if (character === "]") {
      return "regex grammar has an unmatched ']'";
    }
    if (character === "(") {
      groups += 1;
      canRepeat = false;
      continue;
    }
    if (character === ")") {
      if (groups === 0) {
        return "regex grammar has an unmatched ')'";
      }
      groups -= 1;
      canRepeat = true;
      continue;
    }
    if (character === "*" || character === "+" || character === "?") {
      if (character === "?" && index > 0 && definition[index - 1] === "(") {
        continue;
      }
      if (!canRepeat) {
        return `regex grammar has misplaced quantifier ${JSON.stringify(character)}`;
      }
      canRepeat = false;
      continue;
    }
    if (character === "{") {
      const quantifier = definition.slice(index).match(/^\{([0-9]+)(?:,([0-9]*))?\}/);
      if (!canRepeat || quantifier === null) {
        return "regex grammar has an invalid counted repetition";
      }
      if (quantifier[2] !== undefined && quantifier[2] !== "" && Number(quantifier[2]) < Number(quantifier[1])) {
        return "regex grammar has a descending counted repetition";
      }
      index += quantifier[0].length - 1;
      canRepeat = false;
      continue;
    }
    if (character === "}") {
      return "regex grammar has an unmatched '}'";
    }
    if (character === "|") {
      canRepeat = false;
      continue;
    }
    if (character === "^" || character === "$" || (character === ":" && index > 0 && definition[index - 1] === "?")) {
      continue;
    }
    canRepeat = true;
  }
  if (escaped) {
    return "regex grammar has a trailing escape";
  }
  if (inClass) {
    return "regex grammar has an unterminated character class";
  }
  if (groups !== 0) {
    return "regex grammar has an unterminated group";
  }
  return null;
}

function tokenizeLarkExpression(expression) {
  const tokens = [];
  for (let index = 0; index < expression.length;) {
    const character = expression[index];
    if (/\s/.test(character)) {
      index += 1;
      continue;
    }
    if (expression.startsWith("//", index)) {
      break;
    }
    if (expression.startsWith("->", index)) {
      tokens.push({kind: "->"});
      index += 2;
      continue;
    }
    if (expression.startsWith("..", index)) {
      tokens.push({kind: ".."});
      index += 2;
      continue;
    }
    if (character === "'" || character === '"') {
      const quote = character;
      let escaped = false;
      let closed = false;
      index += 1;
      while (index < expression.length) {
        const next = expression[index];
        index += 1;
        if (escaped) {
          escaped = false;
        } else if (next === "\\") {
          escaped = true;
        } else if (next === quote) {
          closed = true;
          break;
        }
      }
      if (!closed) {
        throw new Error("unterminated string terminal");
      }
      tokens.push({kind: "atom"});
      continue;
    }
    if (character === "/") {
      let escaped = false;
      let closed = false;
      let body = "";
      index += 1;
      while (index < expression.length) {
        const next = expression[index];
        index += 1;
        if (escaped) {
          body += `\\${next}`;
          escaped = false;
        } else if (next === "\\") {
          escaped = true;
        } else if (next === "/") {
          closed = true;
          break;
        } else {
          body += next;
        }
      }
      if (!closed) {
        throw new Error("unterminated regex terminal");
      }
      while (index < expression.length && /[A-Za-z]/.test(expression[index])) {
        index += 1;
      }
      const regexError = validateRegexGrammar(body);
      if (regexError !== null) {
        throw new Error(regexError);
      }
      tokens.push({kind: "atom"});
      continue;
    }
    const identifier = expression.slice(index).match(/^\$?[A-Za-z_][A-Za-z0-9_]*/);
    if (identifier !== null) {
      tokens.push({kind: "identifier"});
      index += identifier[0].length;
      continue;
    }
    const number = expression.slice(index).match(/^[0-9]+/);
    if (number !== null) {
      tokens.push({kind: "number"});
      index += number[0].length;
      continue;
    }
    if ("()[]|?*+~".includes(character)) {
      tokens.push({kind: character});
      index += 1;
      continue;
    }
    if (character === "{" || character === "}") {
      throw new Error("template syntax is unsupported");
    }
    throw new Error(`unexpected token ${JSON.stringify(character)}`);
  }
  return tokens;
}

function validateLarkExpression(expression) {
  const tokens = tokenizeLarkExpression(expression);
  let position = 0;

  function parseAlternatives(closing) {
    parseExpansion(closing);
    while (tokens[position]?.kind === "|") {
      position += 1;
      parseExpansion(closing);
    }
  }

  function parseExpansion(closing) {
    let atoms = 0;
    while (position < tokens.length) {
      const kind = tokens[position].kind;
      if (kind === "|" || kind === closing || kind === "->") {
        break;
      }
      parseAtom();
      atoms += 1;
    }
    if (atoms === 0) {
      throw new Error("empty grammar expansion");
    }
    if (tokens[position]?.kind === "->") {
      position += 1;
      if (tokens[position]?.kind !== "identifier") {
        throw new Error("alias requires a rule name");
      }
      position += 1;
    }
  }

  function parseAtom() {
    const token = tokens[position];
    if (token === undefined) {
      throw new Error("missing grammar atom");
    }
    if (token.kind === "atom" || token.kind === "identifier") {
      position += 1;
    } else if (token.kind === "(" || token.kind === "[") {
      const closing = token.kind === "(" ? ")" : "]";
      position += 1;
      parseAlternatives(closing);
      if (tokens[position]?.kind !== closing) {
        throw new Error(`missing ${JSON.stringify(closing)}`);
      }
      position += 1;
    } else {
      throw new Error(`unexpected ${JSON.stringify(token.kind)} in grammar expression`);
    }

    if (tokens[position]?.kind === "..") {
      position += 1;
      if (tokens[position]?.kind !== "atom") {
        throw new Error("literal range requires a terminal");
      }
      position += 1;
    }
    if (["?", "*", "+"].includes(tokens[position]?.kind)) {
      position += 1;
    } else if (tokens[position]?.kind === "~") {
      position += 1;
      if (tokens[position]?.kind !== "number") {
        throw new Error("bounded repetition requires a number");
      }
      position += 1;
      if (tokens[position]?.kind === "..") {
        position += 1;
        if (tokens[position]?.kind !== "number") {
          throw new Error("bounded repetition range requires a number");
        }
        position += 1;
      }
    }
  }

  parseAlternatives(undefined);
  if (position !== tokens.length) {
    throw new Error(`unexpected ${JSON.stringify(tokens[position].kind)} after grammar expression`);
  }
}

function larkStatements(definition) {
  const statements = [];
  let current = "";
  for (const physical of definition.split(/\r?\n/)) {
    const line = physical.trim();
    if (line === "" || line.startsWith("//")) {
      continue;
    }
    const indented = /^\s/.test(physical);
    const continuation = indented || line.startsWith("|") || line.startsWith(")") || line.startsWith("]");
    if (current !== "" && !continuation) {
      statements.push(current);
      current = "";
    }
    current += `${current === "" ? "" : " "}${line}`;
  }
  if (current !== "") {
    statements.push(current);
  }
  return statements;
}

function validateLarkGrammar(definition) {
  let hasStart = false;
  const declarations = new Set();
  for (const statement of larkStatements(definition)) {
    if (statement.startsWith("%import")) {
      if (!commonImportPattern.test(statement)) {
        return "Lark grammar imports outside common";
      }
      continue;
    }
    if (statement.startsWith("%declare")) {
      return "Lark grammar uses unsupported %declare";
    }
    if (statement.startsWith("%ignore")) {
      const expression = statement.slice("%ignore".length).trim();
      if (expression === "") {
        return "Lark grammar has an empty %ignore expression";
      }
      try {
        validateLarkExpression(expression);
      } catch (error) {
        return `invalid Lark %ignore expression: ${errorText(error)}`;
      }
      continue;
    }

    let modifiesDeclaration = false;
    let rule = statement;
    if (rule.startsWith("%extend ") || rule.startsWith("%override ")) {
      modifiesDeclaration = true;
      rule = rule.slice(rule.indexOf(" ") + 1).trim();
    } else if (rule.startsWith("%")) {
      return "Lark grammar uses an unsupported directive";
    }
    if (priorityHeaderPattern.test(rule)) {
      return "Lark grammar uses an unsupported priority";
    }
    const match = rule.match(ruleHeaderPattern);
    if (match === null) {
      return "Lark grammar contains an invalid rule declaration";
    }
    if (!modifiesDeclaration) {
      if (declarations.has(match[1])) {
        return `Lark declaration ${JSON.stringify(match[1])} is defined more than once`;
      }
      declarations.add(match[1]);
    }
    hasStart ||= match[1] === "start";
    if (match[2] === "") {
      return `Lark rule ${JSON.stringify(match[1])} has an empty expression`;
    }
    try {
      validateLarkExpression(match[2]);
    } catch (error) {
      return `invalid Lark rule ${JSON.stringify(match[1])}: ${errorText(error)}`;
    }
  }
  if (!hasStart) {
    return "Lark grammar does not define start";
  }
  return null;
}

function validateSpecification(specification, label, errors) {
  if (specification === null || typeof specification !== "object" || Array.isArray(specification)) {
    errors.push(`${label}: specification must be an object`);
    return null;
  }
  const allowed = specification.format === undefined
    ? ["type", "name", "description"]
    : ["type", "name", "description", "format"];
  if (!exactKeys(specification, allowed)) {
    errors.push(`${label}: specification contains unsupported or missing fields`);
    return null;
  }
  if (specification.type !== "custom") {
    errors.push(`${label}: specification type must be "custom"`);
  }
  if (typeof specification.name !== "string" || byteLength(specification.name) > 64 || !toolNamePattern.test(specification.name)) {
    errors.push(`${label}: specification name is invalid`);
  } else if (shellCommandNames.has(specification.name)) {
    errors.push(`${label}: specification name collides with a shell keyword or built-in`);
  }
  if (typeof specification.description !== "string" || byteLength(specification.description) === 0 || byteLength(specification.description) > MAX_DESCRIPTION_BYTES) {
    errors.push(`${label}: specification description must contain 1 to ${MAX_DESCRIPTION_BYTES} UTF-8 bytes`);
  }
  if (specification.format !== undefined) {
    const format = specification.format;
    if (format === null || typeof format !== "object" || Array.isArray(format)
        || !exactKeys(format, ["type", "syntax", "definition"])) {
      errors.push(`${label}: specification format must contain only type, syntax, and definition`);
    } else if (typeof format.definition !== "string" || byteLength(format.definition) === 0 || byteLength(format.definition) > MAX_GRAMMAR_BYTES) {
      errors.push(`${label}: grammar definition must contain 1 to ${MAX_GRAMMAR_BYTES} UTF-8 bytes`);
    } else if (format.type !== "grammar" || (format.syntax !== "lark" && format.syntax !== "regex")) {
      errors.push(`${label}: specification format must be a lark or regex grammar`);
    } else {
      const grammarError = format.syntax === "regex"
        ? validateRegexGrammar(format.definition)
        : validateLarkGrammar(format.definition);
      if (grammarError !== null) {
        errors.push(`${label}: ${grammarError}`);
      }
    }
  }
  return specification;
}

function validateTool(tool, modulePath, index, errors) {
  const label = `${modulePath}: tool ${index + 1}`;
  if (tool === null || typeof tool !== "object" || Array.isArray(tool)
      || !exactKeys(tool, ["specification", "maxInputBytes", "parse", "argv", "translate", "execute"])) {
    errors.push(`${label}: declaration must contain only specification, maxInputBytes, parse, argv, translate, and execute`);
    return null;
  }
  const specification = validateSpecification(tool.specification, label, errors);
  if (!Number.isSafeInteger(tool.maxInputBytes) || tool.maxInputBytes < 1 || tool.maxInputBytes > MAX_INPUT_BYTES) {
    errors.push(`${label}: maxInputBytes must be an integer from 1 through ${MAX_INPUT_BYTES}`);
  }
  for (const name of ["parse", "argv", "translate", "execute"]) {
    if (typeof tool[name] !== "function") {
      errors.push(`${label}: ${name} must be a function`);
    }
  }
  if (specification === null || errors.some((message) => message.startsWith(`${label}:`))) {
    return null;
  }
  return {
    specification,
    maxInputBytes: tool.maxInputBytes,
  };
}

async function loadDeclaration(snapshotRoot, modulePath) {
  const absolute = path.resolve(snapshotRoot, modulePath);
  if (!inside(snapshotRoot, absolute)) {
    throw new Error(`${modulePath}: declaration escapes the plugin snapshot`);
  }
  return (await import(pathToFileURL(absolute).href)).default;
}

async function validateModule(snapshotRoot, modulePath) {
  const errors = [];
  let declaration;
  try {
    declaration = await loadDeclaration(snapshotRoot, modulePath);
  } catch (error) {
    return {errors: [`${modulePath}: cannot load declaration: ${errorText(error)}`], plugin: null};
  }
  if (declaration === null || typeof declaration !== "object" || Array.isArray(declaration)
      || !exactKeys(declaration, ["apiVersion", "id", "tools"])) {
    return {
      errors: [`${modulePath}: default export must contain only apiVersion, id, and tools`],
      plugin: null,
    };
  }
  if (declaration.apiVersion !== API_VERSION) {
    errors.push(`${modulePath}: apiVersion must be "${API_VERSION}"`);
  }
  if (typeof declaration.id !== "string" || byteLength(declaration.id) > MAX_ID_BYTES || !identifierPattern.test(declaration.id)) {
    errors.push(`${modulePath}: id is invalid`);
  }
  if (!Array.isArray(declaration.tools) || declaration.tools.length === 0 || declaration.tools.length > MAX_PLUGIN_TOOLS) {
    errors.push(`${modulePath}: tools must contain 1 to ${MAX_PLUGIN_TOOLS} declarations`);
  }
  const tools = [];
  if (Array.isArray(declaration.tools)) {
    for (const [index, tool] of declaration.tools.entries()) {
      const normalized = validateTool(tool, modulePath, index, errors);
      if (normalized !== null) {
        tools.push(normalized);
      }
    }
  }
  return {
    errors,
    plugin: errors.length === 0 ? {id: declaration.id, module: modulePath, tools} : null,
  };
}

async function loadTool(snapshotRoot, modulePath, index) {
  const declaration = await loadDeclaration(snapshotRoot, modulePath);
  if (declaration?.apiVersion !== API_VERSION || !Array.isArray(declaration.tools)) {
    throw new Error("snapshotted plugin declaration is invalid");
  }
  const tool = declaration.tools[index];
  if (tool === null || typeof tool !== "object") {
    throw new Error(`snapshotted plugin tool ${index + 1} is unavailable`);
  }
  return tool;
}

function validateArguments(argumentsValue) {
  if (!Array.isArray(argumentsValue) || argumentsValue.length > MAX_ARGV_ITEMS
      || argumentsValue.some((argument) => typeof argument !== "string" || byteLength(argument) > MAX_ARG_BYTES)) {
    throw new Error(`argv must contain at most ${MAX_ARGV_ITEMS} strings of at most ${MAX_ARG_BYTES} UTF-8 bytes`);
  }
  return argumentsValue;
}

function validateCarrier(carrier) {
  if (carrier === null || typeof carrier !== "object" || Array.isArray(carrier)) {
    throw new Error("translator must return a carrier object");
  }
  if (carrier.kind === "exec" && exactKeys(carrier, ["kind"])) {
    return carrier;
  }
  if ((carrier.kind === "custom" || carrier.kind === "function")
      && exactKeys(carrier, ["kind", "name", "payload"])
      && typeof carrier.name === "string"
      && toolNamePattern.test(carrier.name)
      && typeof carrier.payload === "string") {
    return carrier;
  }
  throw new Error("translator returned a malformed carrier");
}

async function translateTool(request) {
  const tool = await loadTool(request.snapshotRoot, request.module, request.index);
  let parsed;
  try {
    parsed = await tool.parse(request.input);
  } catch (error) {
    return {rejected: true, diagnostic: errorText(error), arguments: [], carrier: {kind: "", name: "", payload: ""}};
  }
  const argumentsValue = validateArguments(await tool.argv(parsed));
  const api = Object.freeze({
    custom(name, input) {
      return Object.freeze({kind: "custom", name, payload: input});
    },
    function(name, argumentsJSON) {
      return Object.freeze({kind: "function", name, payload: argumentsJSON});
    },
    exec() {
      return Object.freeze({kind: "exec"});
    },
  });
  const carrier = validateCarrier(await tool.translate(parsed, api));
  return {rejected: false, diagnostic: "", arguments: argumentsValue, carrier};
}

async function executeTool(request) {
  const tool = await loadTool(request.snapshotRoot, request.module, request.index);
  const argumentsValue = validateArguments(request.arguments);
  const execution = await tool.execute(argumentsValue);
  if (execution === null || typeof execution !== "object" || Array.isArray(execution)
      || !Object.keys(execution).every((key) => ["stdout", "stderr", "exitCode"].includes(key))
      || !Number.isSafeInteger(execution.exitCode) || execution.exitCode < 0 || execution.exitCode > 255
      || (execution.stdout !== undefined && typeof execution.stdout !== "string")
      || (execution.stderr !== undefined && typeof execution.stderr !== "string")) {
    throw new Error("executor must return stdout/stderr strings and an exitCode from 0 through 255");
  }
  const stdout = execution.stdout ?? "";
  const stderr = execution.stderr ?? "";
  if (byteLength(stdout) + byteLength(stderr) > MAX_EXECUTION_OUTPUT_BYTES) {
    throw new Error(`executor stdout and stderr exceed ${MAX_EXECUTION_OUTPUT_BYTES} UTF-8 bytes`);
  }
  return {
    stdout,
    stderr,
    exitCode: execution.exitCode,
  };
}

async function main() {
  const request = JSON.parse(await new Promise((resolve, reject) => {
    const chunks = [];
    process.stdin.on("data", (chunk) => chunks.push(chunk));
    process.stdin.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    process.stdin.on("error", reject);
  }));
  const snapshotRoot = path.resolve(request.snapshotRoot);
  registerHooks({
    resolve(specifier, context, nextResolve) {
      const resolved = nextResolve(specifier, context);
      if (resolved.url.startsWith("node:")) {
        return resolved;
      }
      if (!resolved.url.startsWith("file:")) {
        throw new Error(`plugin module protocol is not supported: ${resolved.url}`);
      }
      const filename = fileURLToPath(resolved.url);
      if (!inside(snapshotRoot, filename)) {
        throw new Error(`plugin module escapes the immutable snapshot: ${filename}`);
      }
      if (path.extname(filename) === ".js") {
        return {...resolved, format: "module"};
      }
      return resolved;
    },
  });

  let response;
  switch (request.operation) {
    case "validate": {
      const plugins = [];
      const errors = [];
      for (const modulePath of request.modules) {
        const result = await validateModule(snapshotRoot, modulePath);
        errors.push(...result.errors);
        if (result.plugin !== null) {
          plugins.push(result.plugin);
        }
      }
      response = {plugins, errors};
      break;
    }
    case "translate":
      response = await translateTool(request);
      break;
    case "execute":
      response = await executeTool(request);
      break;
    default:
      throw new Error(`unsupported plugin runtime operation ${JSON.stringify(request.operation)}`);
  }
  process.stdout.write(JSON.stringify(response));
}

main().catch((error) => {
  process.stderr.write(`${error?.stack ?? String(error)}\n`);
  process.exitCode = 1;
});
