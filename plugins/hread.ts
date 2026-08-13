import {constants} from "node:fs";
import {open} from "node:fs/promises";

import type {Tool} from "../internal/router/toolplugin/plugin.d.ts";

import {
  byteLength,
  errorText,
  createExecutorTool,
  formatHashLine,
  shellQuoteArgument,
  stripOptionalFinalNewline,
} from "./common.ts";

const READ_BUFFER_BYTES = 32 * 1024;

class ResultTooLargeError extends Error {}
function conciseErrorText(error: unknown): string {
  const message = errorText(error);
  if (!(error instanceof Error) || !("syscall" in error) || typeof error.syscall !== "string") {
    return message;
  }
  const detailsStart = message.indexOf(`, ${error.syscall}`);
  return detailsStart < 0 ? message : message.slice(0, detailsStart);
}

type ReadSpec = {
  path: string;
  startLine: number;
  endLine: number;
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
    return {path, startLine: 0, endLine: 0};
  }
  const match = trailing.match(/^ (0|[1-9][0-9]*):([1-9][0-9]*)$/u);
  if (match === null) {
    throw new Error("hread input must be PATH or PATH START:END");
  }
  const requestedStartLine = Number(match[1]);
  const endLine = Number(match[2]);
  if (!Number.isSafeInteger(requestedStartLine)) {
    throw new Error("hread start line is out of range");
  }
  if (!Number.isSafeInteger(endLine)) {
    throw new Error("hread end line is out of range");
  }
  if (requestedStartLine > endLine) {
    throw new Error("hread line range start exceeds end");
  }
  return {path, startLine: Math.max(1, requestedStartLine), endLine};
}


type ComparedOutput = {
  current: string;
  stock: string;
  warning?: string;
};


async function readHashLines(spec: ReadSpec, maxOutputBytes: number): Promise<ComparedOutput> {
  const handle = await open(spec.path, constants.O_RDONLY | (constants.O_NONBLOCK ?? 0));

  try {
    const info = await handle.stat();
    if (!info.isFile()) {
      throw new Error("not a regular file");
    }

    const wholeFile = spec.startLine === 0 && spec.endLine === 0;
    let lineNumber = 1;
    let lineOpen = false;
    let pendingCR = false;
    let content = "";
    let contentBytes = 0;
    let current = "";
    let currentBytes = 0;
    let stock = "";

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
      if (currentBytes + rowFramingBytes + contentBytes + addedBytes > maxOutputBytes) {
        throw capacityError();
      }
      content += text;
      contentBytes += addedBytes;
    };
    const finishLine = (): void => {
      if (selected()) {
        const row = formatHashLine(lineNumber, content);
        const rowBytes = byteLength(row);
        if (currentBytes + rowBytes > maxOutputBytes) {
          throw capacityError();
        }
        current += row;
        currentBytes += rowBytes;
        stock += `${content}\n`;
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
        throw new Error("not UTF-8");
      }
      throw error;
    } finally {
      stream.destroy();
    }

    if (lineOpen) {
      finishLine();
    }
    const lineCount = lineNumber - 1;
    if (spec.startLine > lineCount) {
      throw new Error(`start line ${spec.startLine} is past EOF (${lineCount} lines)`);
    }
    const missingStartLine = Math.max(spec.startLine, lineCount + 1);
    const warning = !wholeFile && missingStartLine <= spec.endLine
      ? `hread: ${missingStartLine}-${spec.endLine}: [out of range]\n`
      : undefined;
    return {current, stock, warning};
  } finally {
    await handle.close();
  }
}


function hreadArguments(input: string): string[] {
  const spec = parseReadSpec(stripOptionalFinalNewline(input));
  if (spec.startLine === 0) {
    return [spec.path];
  }
  return [spec.path, `${spec.startLine}:${spec.endLine}`];
}


function hreadInput(argv: string[]): string {
  if (argv.length === 1 && argv[0] !== "") {
    return JSON.stringify(argv[0]);
  }
  if (
    argv.length === 2 &&
    argv[0] !== "" &&
    /^(?:0|[1-9][0-9]*):[1-9][0-9]*$/u.test(argv[1])
  ) {
    return `${JSON.stringify(argv[0])} ${argv[1]}`;
  }
  throw new Error("hread expected PATH or PATH START:END");
}


function hreadStockCommand(argv: string[]): string {
  const spec = parseReadSpec(hreadInput(argv));
  const command = `cat ${shellQuoteArgument(spec.path)}`;
  if (spec.startLine === 0) {
    return command;
  }
  return `${command} | sed -n '${spec.startLine},${spec.endLine}p'`;
}

export function createHReadTool(description: string, grammar: string): Tool<string[]> {
  return createExecutorTool({
    name: "hread",
    description,
    grammar,
    stockCommand: hreadStockCommand,
    argv(input, context) {
      const argumentsValue = hreadArguments(input);
      argumentsValue[0] = context.resolvePath(argumentsValue[0]);
      return argumentsValue;
    },
    async execute(argv, context) {
      try {
        const executionArguments = [...argv];
        if (executionArguments[0]?.startsWith("@shell/")) {
          const sessionID = process.env.CODEX_THREAD_ID;
          if (sessionID === undefined || sessionID === "") {
            throw new Error("CODEX_THREAD_ID is unavailable");
          }
          executionArguments[0] = `/tmp/hpatch-${sessionID}/${executionArguments[0].slice("@shell/".length)}`;
        }
        const result = await readHashLines(parseReadSpec(stripOptionalFinalNewline(hreadInput(executionArguments))), context.outputBudgetBytes);
        return {
          stdout: result.current,
          ...(result.warning === undefined ? {} : {stderr: result.warning}),
          stock: {stdout: result.stock, exitCode: 0},
          exitCode: 0,
        };
      } catch (error) {
        return {stderr: `hread: ${conciseErrorText(error)}\n`, exitCode: 1};
      }
    },
  });
}
