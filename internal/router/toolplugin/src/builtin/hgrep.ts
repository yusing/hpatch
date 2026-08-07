import {spawn} from "node:child_process";

import type {Tool} from "../../plugin.d.ts";
import {
  byteLength,
  decodeUTF8,
  errorText,
  createExecutorTool,
  formatHashLine,
  stripOptionalFinalNewline,
} from "./common.ts";

// Source: internal/router/hgrep_worker.go:119:460 argument parsing, rg transport, and row rendering.
const MAX_STDERR_BYTES = 64 * 1024;
const LIMIT_MESSAGE = "hgrep: output limit reached; retry with a narrower search\n";

const silentLongOptions = new Set([
  "line-number",
  "no-column",
  "no-config",
  "no-heading",
  "no-json",
  "no-max-columns-preview",
  "no-stats",
  "no-trim",
  "with-filename",
]);
const warnedLongOptions = new Map<string, boolean>([
  ["block-buffered", false],
  ["column", false],
  ["count", false],
  ["count-matches", false],
  ["debug", false],
  ["files", false],
  ["files-with-matches", false],
  ["files-without-match", false],
  ["heading", false],
  ["include-zero", false],
  ["json", false],
  ["line-buffered", false],
  ["max-columns-preview", false],
  ["no-filename", false],
  ["no-ignore-messages", false],
  ["no-line-number", false],
  ["no-messages", false],
  ["null", false],
  ["only-matching", false],
  ["passthru", false],
  ["passthrough", false],
  ["pretty", false],
  ["quiet", false],
  ["stats", false],
  ["trace", false],
  ["trim", false],
  ["vimgrep", false],
  ["color", true],
  ["colors", true],
  ["context-separator", true],
  ["field-context-separator", true],
  ["field-match-separator", true],
  ["hyperlink-format", true],
  ["max-columns", true],
  ["path-separator", true],
  ["replace", true],
]);
const forbiddenLongOptions = new Set([
  "binary",
  "encoding",
  "generate",
  "help",
  "hostname-bin",
  "multiline",
  "multiline-dotall",
  "no-binary",
  "no-text",
  "null-data",
  "pcre2-version",
  "pre",
  "pre-glob",
  "search-zip",
  "text",
  "type-list",
  "version",
]);
const longOptionsWithValue = new Set([
  "after-context",
  "before-context",
  "context",
  "dfa-size-limit",
  "engine",
  "file",
  "glob",
  "iglob",
  "ignore-file",
  "max-count",
  "max-depth",
  "max-filesize",
  "regex-size-limit",
  "regexp",
  "sort",
  "sortr",
  "threads",
  "type",
  "type-add",
  "type-clear",
  "type-not",
]);
const silentShortOptions = new Set("Hn");
const warnedShortOptions = new Map<string, boolean>([
  ["0", false],
  ["I", false],
  ["M", true],
  ["N", false],
  ["b", false],
  ["c", false],
  ["h", false],
  ["l", false],
  ["o", false],
  ["p", false],
  ["q", false],
  ["r", true],
]);
const forbiddenShortOptions = new Set("EUVaz");
const shortOptionsWithValue = new Set("ABCTdefgjmt");

type NormalizedArguments = {
  arguments: string[];
  warnings: string[];
};

type JSONText = {
  text?: string;
  bytes?: string;
};

type JSONEvent = {
  type?: string;
  data?: {
    path?: JSONText;
    lines?: JSONText;
    line_number?: number;
  };
};

export function splitArguments(rawInput: string): string[] {
  const input = stripOptionalFinalNewline(rawInput);
  if (input === "") {
    throw new Error("input must not be empty");
  }
  const argumentsValue: string[] = [];
  let offset = 0;
  while (offset < input.length) {
    while (input[offset] === " " || input[offset] === "\t") {
      offset += 1;
    }
    if (offset === input.length) {
      break;
    }

    let argument = "";
    let started = false;
    while (offset < input.length && input[offset] !== " " && input[offset] !== "\t") {
      started = true;
      const character = input[offset];
      if (character === "\r" || character === "\n") {
        throw new Error("input must contain one argument line");
      }
      if (character === "'" || character === "\"") {
        const quote = character;
        offset += 1;
        while (offset < input.length && input[offset] !== quote) {
          if (input[offset] === "\r" || input[offset] === "\n") {
            throw new Error("quoted argument must not contain a newline");
          }
          if (quote === "\"" && input[offset] === "\\") {
            offset += 1;
            if (offset === input.length) {
              throw new Error("double-quoted argument ends with an escape");
            }
          }
          argument += input[offset];
          offset += 1;
        }
        if (offset === input.length) {
          throw new Error("unterminated quoted argument");
        }
        offset += 1;
        continue;
      }
      if (character === "\\") {
        offset += 1;
        if (offset === input.length) {
          throw new Error("argument ends with an escape");
        }
        argument += input[offset];
        offset += 1;
        continue;
      }
      argument += character;
      offset += 1;
    }
    if (started) {
      argumentsValue.push(argument);
    }
  }
  if (argumentsValue.length === 0) {
    throw new Error("input must contain at least one argument");
  }
  return argumentsValue;
}

function normalizeArguments(input: string[]): NormalizedArguments {
  let options = true;
  let patternFromOption = false;
  let positionals = 0;
  const normalized: string[] = [];
  const warnings: string[] = [];
  const warned = new Set<string>();
  const addWarning = (option: string): void => {
    if (!warned.has(option)) {
      warned.add(option);
      warnings.push(option);
    }
  };

  for (let index = 0; index < input.length; index += 1) {
    const argument = input[index];
    if (options && argument === "--") {
      options = false;
      normalized.push(argument);
      continue;
    }
    if (!options || argument === "-" || !argument.startsWith("-")) {
      positionals += 1;
      normalized.push(argument);
      continue;
    }
    if (argument.startsWith("--")) {
      const option = argument.slice(2);
      const separator = option.indexOf("=");
      const name = separator < 0 ? option : option.slice(0, separator);
      const attached = separator >= 0;
      if (silentLongOptions.has(name)) {
        continue;
      }
      const warnedHasValue = warnedLongOptions.get(name);
      if (warnedHasValue !== undefined) {
        addWarning(`--${name}`);
        if (warnedHasValue && !attached) {
          if (index + 1 === input.length) {
            throw new Error(`ripgrep option --${name} requires a value`);
          }
          index += 1;
        }
        continue;
      }
      if (forbiddenLongOptions.has(name)) {
        throw new Error(`ripgrep option --${name} is incompatible with verified-row output`);
      }
      normalized.push(argument);
      if (name === "regexp" || name === "file") {
        patternFromOption = true;
      }
      if (longOptionsWithValue.has(name) && !attached) {
        if (index + 1 === input.length) {
          throw new Error(`ripgrep option --${name} requires a value`);
        }
        index += 1;
        normalized.push(input[index]);
      }
      continue;
    }

    const short = [...argument.slice(1)];
    let kept = "";
    let keptValue: string | null = null;
    for (let offset = 0; offset < short.length; offset += 1) {
      const option = short[offset];
      if (silentShortOptions.has(option)) {
        continue;
      }
      const warnedHasValue = warnedShortOptions.get(option);
      if (warnedHasValue !== undefined) {
        addWarning(`-${option}`);
        if (warnedHasValue) {
          if (offset === short.length - 1) {
            if (index + 1 === input.length) {
              throw new Error(`ripgrep option -${option} requires a value`);
            }
            index += 1;
          }
          break;
        }
        continue;
      }
      if (forbiddenShortOptions.has(option)) {
        throw new Error(`ripgrep option -${option} is incompatible with verified-row output`);
      }
      kept += option;
      if (option === "e" || option === "f") {
        patternFromOption = true;
      }
      if (shortOptionsWithValue.has(option)) {
        if (offset === short.length - 1) {
          if (index + 1 === input.length) {
            throw new Error(`ripgrep option -${option} requires a value`);
          }
          index += 1;
          keptValue = input[index];
        } else {
          kept += short.slice(offset + 1).join("");
        }
        break;
      }
    }
    if (kept !== "") {
      normalized.push(`-${kept}`);
      if (keptValue !== null) {
        normalized.push(keptValue);
      }
    }
  }

  if (patternFromOption) {
    if (positionals === 0) {
      normalized.push(".");
    }
    return {arguments: normalized, warnings};
  }
  if (positionals === 0) {
    throw new Error("ripgrep search requires a pattern");
  }
  if (positionals === 1) {
    normalized.push(".");
  }
  return {arguments: normalized, warnings};
}

function decodeJSONText(value: JSONText | undefined, label: string): string {
  if (typeof value?.text === "string") {
    return value.text;
  }
  if (typeof value?.bytes !== "string" || value.bytes === "") {
    throw new Error(`rg ${label} has no text`);
  }
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/u.test(value.bytes)) {
    throw new Error(`decode base64 ${label}`);
  }
  return decodeUTF8(Buffer.from(value.bytes, "base64"), `rg ${label}`);
}

async function collectStderr(stream: AsyncIterable<Uint8Array>): Promise<string> {
  const chunks: Buffer[] = [];
  let retained = 0;
  for await (const chunk of stream) {
    if (retained >= MAX_STDERR_BYTES) {
      continue;
    }
    const buffer = Buffer.from(chunk);
    const keep = buffer.subarray(0, MAX_STDERR_BYTES - retained);
    chunks.push(keep);
    retained += keep.length;
  }
  return Buffer.concat(chunks).toString("utf8");
}

function conciseDiagnostic(diagnostic: string): string {
  const lines = diagnostic.split("\n");
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    const line = lines[index].trim();
    if (line === "") {
      continue;
    }
    const operationMarker = ": IO error for operation on ";
    const operationStart = line.indexOf(operationMarker);
    if (line.startsWith("rg: ") && operationStart >= 0) {
      const path = line.slice("rg: ".length, operationStart);
      const messageStart = `${operationMarker}${path}: `;
      const details = line.slice(operationStart);
      if (details.startsWith(messageStart)) {
        return details.slice(messageStart.length);
      }
    }
    return line;
  }
  return diagnostic;
}

type ComparedOutput = {
  current: string;
  stock: string;
};

async function runRipgrep(argumentsValue: string[], maxOutputBytes: number): Promise<ComparedOutput> {
  const child = spawn("rg", ["--json", "--no-config", ...argumentsValue], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  const completion = new Promise<number | null>((resolve, reject) => {
    child.once("error", reject);
    child.once("close", (code) => resolve(code));
  });
  const stderrPromise = collectStderr(child.stderr);

  let current = "";
  let currentBytes = 0;
  let stock = "";
  let pending: Buffer[] = [];
  let pendingBytes = 0;
  let truncated = false;
  const seen = new Set<string>();
  const processEvent = (raw: Buffer): boolean => {
    let event: JSONEvent;
    try {
      event = JSON.parse(decodeUTF8(raw, "rg output"));
    } catch (error) {
      throw new Error(`decode rg output: ${errorText(error)}`);
    }
    if (event.type !== "match" && event.type !== "context") {
      return true;
    }
    const path = decodeJSONText(event.data?.path, "path");
    let line = decodeJSONText(event.data?.lines, "result");
    if (line.endsWith("\n")) {
      line = line.slice(0, -1);
    }
    if (line.endsWith("\r")) {
      line = line.slice(0, -1);
    }
    const lineNumber = event.data?.line_number;
    if (!Number.isSafeInteger(lineNumber) || lineNumber < 1
        || line.includes("\n") || line.includes("\r")) {
      throw new Error("rg returned a non-logical-line result");
    }
    const key = `${path}\u0000${lineNumber}`;
    if (seen.has(key)) {
      return true;
    }
    seen.add(key);
    const prefix = `${JSON.stringify(path)}:`;
    const row = `${prefix}${formatHashLine(lineNumber, line)}`;
    const rowBytes = byteLength(row);
    if (currentBytes + rowBytes + byteLength(LIMIT_MESSAGE) > maxOutputBytes) {
      return false;
    }
    current += row;
    stock += `${prefix}${line}\n`;
    currentBytes += rowBytes;
    return true;
  };
const eventLimit = (): number => Math.max(0, maxOutputBytes - currentBytes - byteLength(LIMIT_MESSAGE));
  const takePending = (): Buffer => {
    const raw = pending.length === 1 ? pending[0] : Buffer.concat(pending, pendingBytes);
    pending = [];
    pendingBytes = 0;
    return raw;
  };

  let exitCode: number | null;
  let stderr: string;
  try {
    outer: for await (const rawChunk of child.stdout) {
      const chunk = Buffer.from(rawChunk);
      let offset = 0;
      while (offset < chunk.length) {
        const newline = chunk.indexOf(0x0a, offset);
        const end = newline < 0 ? chunk.length : newline + 1;
        const fragment = chunk.subarray(offset, end);
        if (pendingBytes + fragment.length > eventLimit()) {
          truncated = true;
          child.kill("SIGKILL");
          break outer;
        }
        pending.push(fragment);
        pendingBytes += fragment.length;
        offset = end;
        if (newline < 0) {
          break;
        }
        if (!processEvent(takePending())) {
          truncated = true;
          child.kill("SIGKILL");
          break outer;
        }
      }
    }
    if (!truncated && pendingBytes !== 0
        && (pendingBytes > eventLimit() || !processEvent(takePending()))) {
      truncated = true;
      child.kill("SIGKILL");
    }
    exitCode = await completion;
    stderr = await stderrPromise;
  } catch (error) {
    child.kill("SIGKILL");
    await completion.catch(() => null);
    await stderrPromise.catch(() => "");
    throw error;
  }

  if (truncated) {
    current += LIMIT_MESSAGE;
    stock += LIMIT_MESSAGE;
    return {current, stock};
  }
  if (exitCode === 0 || exitCode === 1) {
    return {current, stock};
  }
  const diagnostic = conciseDiagnostic(stderr.trim());
  if (diagnostic !== "") {
    throw new Error(diagnostic);
  }
  throw new Error(`execute rg: exit status ${exitCode ?? "unknown"}`);
}

export function createHGrepTool(description: string, grammar: string): Tool<string[]> {
  return createExecutorTool({
    name: "hgrep",
    description,
    grammar,
    argv(input) {
      return splitArguments(input);
    },
    async execute(argv, context) {
      let normalized: NormalizedArguments;
      try {
        normalized = normalizeArguments(argv);
      } catch (error) {
        return {stderr: `hgrep: ${errorText(error)}\n`, exitCode: 1};
      }
      const warning = normalized.warnings.length === 0
        ? ""
        : `hgrep: warning: ignoring ripgrep options ${normalized.warnings.join(", ")}; output remains verified rows\n`;
      try {
        const maxOutputBytes = context.outputBudgetBytes - byteLength(warning);
        const result = await runRipgrep(normalized.arguments, maxOutputBytes);
        return {
          stdout: result.current,
          stderr: warning,
          stock: {stdout: result.stock, stderr: warning, exitCode: 0},
          exitCode: 0,
        };
      } catch (error) {
        return {stderr: `${warning}hgrep: ${errorText(error)}\n`, exitCode: 1};
      }
    },
  });
}
