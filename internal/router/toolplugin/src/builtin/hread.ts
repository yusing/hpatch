import {constants} from "node:fs";
import {open} from "node:fs/promises";

import type {Tool} from "../../plugin.d.ts";

import {
  MAX_OUTPUT_BYTES,
  byteLength,
  errorText,
  createExecutorTool,
  formatHashLine,
} from "./common.ts";

// Source: hread.go:45:385 parseHReadSpec, readHashLinesForHost, and formatHashLineStream.
const MAX_BATCH_ITEMS = 6;
const READ_BUFFER_BYTES = 32 * 1024;
const BATCH_LIMIT_MESSAGE = "hread: batch output limit reached; retry remaining items in a narrower batch\n";

class ResultTooLargeError extends Error {}

type ReadSpec = {
  input: string;
  path: string;
  startLine: number;
  endLine: number;
};

type ParsedReadSpec = {
  spec: ReadSpec;
  error: unknown | null;
};

function parseQuotedPath(input: string): {path: string; trailing: string} {
  let escaped = false;
  for (let index = 1; index < input.length; index += 1) {
    const character = input[index];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (character === "\\") {
      escaped = true;
      continue;
    }
    if (character !== "\"") {
      continue;
    }
    const encoded = input.slice(0, index + 1);
    let path;
    try {
      path = JSON.parse(encoded);
    } catch (error) {
      throw new Error(`invalid hread path: ${errorText(error)}`);
    }
    if (typeof path !== "string") {
      throw new Error("invalid hread path");
    }
    return {path, trailing: input.slice(index + 1)};
  }
  throw new Error("invalid hread path: unterminated quoted string");
}

function parseReadSpec(input: string): ReadSpec {
  let path;
  let trailing;
  if (input.startsWith("\"")) {
    ({path, trailing} = parseQuotedPath(input));
  } else {
    const separator = input.indexOf(" ");
    path = separator < 0 ? input : input.slice(0, separator);
    trailing = separator < 0 ? "" : input.slice(separator);
    if (/[\u0000-\u0020"]/u.test(path)) {
      throw new Error("invalid bare hread path");
    }
  }
  if (path === "") {
    throw new Error("hread path must not be empty");
  }
  if (trailing === "") {
    return {input, path, startLine: 0, endLine: 0};
  }
  const match = trailing.match(/^ ([1-9][0-9]*):([1-9][0-9]*)$/u);
  if (match === null) {
    throw new Error("hread input must be PATH or PATH START:END");
  }
  const startLine = Number(match[1]);
  const endLine = Number(match[2]);
  if (!Number.isSafeInteger(startLine)) {
    throw new Error("hread start line is out of range");
  }
  if (!Number.isSafeInteger(endLine)) {
    throw new Error("hread end line is out of range");
  }
  if (startLine > endLine) {
    throw new Error("hread line range start exceeds end");
  }
  return {input, path, startLine, endLine};
}

function parseReadSpecs(input: string): ParsedReadSpec[] {
  const rawSpecs = input.split("\n");
  if (rawSpecs.length > MAX_BATCH_ITEMS) {
    throw new Error(`hread batch exceeds ${MAX_BATCH_ITEMS} items`);
  }
  const specs = rawSpecs.map((raw) => {
    const normalized = raw.endsWith("\r") ? raw.slice(0, -1) : raw;
    if (normalized === "") {
      throw new Error("hread batch contains an empty read specification");
    }
    try {
      return {spec: parseReadSpec(normalized), error: null};
    } catch (error) {
      return {
        spec: {input: normalized, path: "", startLine: 0, endLine: 0},
        error,
      };
    }
  });
  if (specs.length === 1 && specs[0].error !== null) {
    throw specs[0].error;
  }
  return specs;
}

async function readHashLines(spec: ReadSpec, maxOutputBytes: number): Promise<string> {
  let handle;
  try {
    handle = await open(spec.path, constants.O_RDONLY | (constants.O_NONBLOCK ?? 0));
  } catch (error) {
    throw new Error(`reading ${spec.path}: ${errorText(error)}`);
  }

  try {
    const info = await handle.stat();
    if (!info.isFile()) {
      throw new Error(`${spec.path} is not a regular file`);
    }

    const wholeFile = spec.startLine === 0 && spec.endLine === 0;
    let lineNumber = 1;
    let lineOpen = false;
    let pendingCR = false;
    let content = "";
    let contentBytes = 0;
    let output = "";
    let outputBytes = 0;

    const selected = () => wholeFile
      || (lineNumber >= spec.startLine && lineNumber <= spec.endLine);
    const capacityError = () => new ResultTooLargeError(
      `hread result exceeds its configured bound of ${maxOutputBytes} bytes`,
    );
    const appendContent = (text: string): void => {
      if (text === "") {
        return;
      }
      lineOpen = true;
      if (!selected()) {
        return;
      }
      const addedBytes = byteLength(text);
      const rowFramingBytes = byteLength(String(lineNumber)) + 7;
      if (outputBytes + rowFramingBytes + contentBytes + addedBytes > maxOutputBytes) {
        throw capacityError();
      }
      content += text;
      contentBytes += addedBytes;
    };
    const finishLine = (): void => {
      if (selected()) {
        const row = formatHashLine(lineNumber, content);
        const rowBytes = byteLength(row);
        if (outputBytes + rowBytes > maxOutputBytes) {
          throw capacityError();
        }
        output += row;
        outputBytes += rowBytes;
      }
      content = "";
      contentBytes = 0;
      lineNumber += 1;
      lineOpen = false;
    };
    const consume = (text: string): void => {
      let offset = 0;
      while (offset < text.length) {
        if (pendingCR) {
          pendingCR = false;
          finishLine();
          if (text[offset] === "\n") {
            offset += 1;
            continue;
          }
        }
        const cr = text.indexOf("\r", offset);
        const lf = text.indexOf("\n", offset);
        let end = text.length;
        if (cr >= 0) {
          end = cr;
        }
        if (lf >= 0 && lf < end) {
          end = lf;
        }
        appendContent(text.slice(offset, end));
        if (end === text.length) {
          break;
        }
        lineOpen = true;
        if (text[end] === "\r") {
          pendingCR = true;
        } else {
          finishLine();
        }
        offset = end + 1;
      }
    };

    const decoder = new TextDecoder("utf-8", {fatal: true});
    const stream = handle.createReadStream({
      autoClose: false,
      highWaterMark: READ_BUFFER_BYTES,
    });
    try {
      for await (const chunk of stream) {
        consume(decoder.decode(chunk, {stream: true}));
      }
      consume(decoder.decode());
    } catch (error) {
      if (error instanceof ResultTooLargeError) {
        throw error;
      }
      if (error instanceof TypeError) {
        throw new Error(`${spec.path} is not UTF-8`);
      }
      throw new Error(`reading ${spec.path}: ${errorText(error)}`);
    } finally {
      stream.destroy();
    }

    if (pendingCR) {
      finishLine();
    } else if (lineOpen) {
      finishLine();
    }
    const lineCount = lineNumber - 1;
    if (!wholeFile && spec.startLine > lineCount) {
      throw new Error(
        `requested lines ${spec.startLine}:${spec.endLine} are outside file with ${lineCount} lines`,
      );
    }
    return output;
  } finally {
    await handle.close();
  }
}

async function executeRead(input: string): Promise<string> {
  const specs = parseReadSpecs(input);
  if (specs.length === 1) {
    return readHashLines(specs[0].spec, MAX_OUTPUT_BYTES);
  }

  const dataLimit = Math.max(0, MAX_OUTPUT_BYTES - byteLength(BATCH_LIMIT_MESSAGE));
  let output = "";
  let outputBytes = 0;
  const appendBounded = (text: string): boolean => {
    const addedBytes = byteLength(text);
    if (outputBytes + addedBytes > dataLimit) {
      return false;
    }
    output += text;
    outputBytes += addedBytes;
    return true;
  };
  const appendLimitMessage = (): void => {
    if (outputBytes + byteLength(BATCH_LIMIT_MESSAGE) <= MAX_OUTPUT_BYTES) {
      output += BATCH_LIMIT_MESSAGE;
      outputBytes += byteLength(BATCH_LIMIT_MESSAGE);
    }
  };

  for (const item of specs) {
    if (!appendBounded(`==> ${item.spec.input} <==\n`)) {
      appendLimitMessage();
      break;
    }
    if (item.error !== null) {
      if (!appendBounded(`hread: ${errorText(item.error)}\n`)) {
        appendLimitMessage();
        break;
      }
      continue;
    }
    try {
      const result = await readHashLines(item.spec, dataLimit - outputBytes);
      output += result;
      outputBytes += byteLength(result);
    } catch (error) {
      if (error instanceof ResultTooLargeError) {
        appendLimitMessage();
        break;
      }
      if (!appendBounded(`hread: ${errorText(error)}\n`)) {
        appendLimitMessage();
        break;
      }
    }
  }
  return output;
}

export function createHReadTool(description: string, grammar: string): Tool<string> {
  return createExecutorTool({
    name: "hread",
    description,
    grammar,
    argv(input) {
      return [input];
    },
    async execute(argv) {
      if (argv.length !== 1) {
        return {
          stderr: "hread: expected one complete grammar input argument\n",
          exitCode: 1,
        };
      }
      try {
        return {stdout: await executeRead(argv[0]), exitCode: 0};
      } catch (error) {
        return {stderr: `hread: ${errorText(error)}\n`, exitCode: 1};
      }
    },
  });
}
