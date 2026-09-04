import {constants} from "node:fs";
import {open} from "node:fs/promises";
import {tmpdir} from "node:os";

import type {Tool} from "../internal/router/toolplugin/plugin.d.ts";
import {
  decodeQuotedOperand,
  formatVerifiedRow,
  parsePositiveInteger,
} from "hpatch:core/v1";

import {
  byteLength,
  errorText,
  createExecutorTool,
  MAX_POSSIBLE_GPT5_TOKEN_BYTES,
  stripOptionalFinalNewline,
  VERIFIED_ROW_LIMIT_DIAGNOSTIC,
  VERIFIED_ROW_MAX_TOKENS,
  VerifiedRowOutput,
} from "./common.ts";

const READ_BUFFER_BYTES = 32 * 1024;

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
  try {
    const decoded = decodeQuotedOperand(input);
    return {path: decoded.value, trailing: decoded.rest};
  } catch (error) {
    throw new Error(`invalid hread path: ${errorText(error)}`);
  }
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
  let requestedStartLine;
  let endLine;
  try {
    requestedStartLine = match[1] === "0" ? 0 : parsePositiveInteger(match[1]);
  } catch {
    throw new Error("hread start line is out of range");
  }
  try {
    endLine = parsePositiveInteger(match[2]);
  } catch {
    throw new Error("hread end line is out of range");
  }
  if (requestedStartLine > endLine) {
    throw new Error("hread line range start exceeds end");
  }
  return {path, startLine: Math.max(1, requestedStartLine), endLine};
}


type ComparedOutput = {
  current: string;
  incomplete: boolean;
  warning?: string;
};


async function readHashLines(spec: ReadSpec): Promise<ComparedOutput> {
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
    const output = new VerifiedRowOutput();

    const selected = () => wholeFile
      || (lineNumber >= spec.startLine && lineNumber <= spec.endLine);
    const appendContent = (text: string): void => {
      if (text === "") {
        return;
      }
      lineOpen = true;
      if (!selected() || output.incomplete) {
        return;
      }
      contentBytes += byteLength(text);
      if (contentBytes > VERIFIED_ROW_MAX_TOKENS * MAX_POSSIBLE_GPT5_TOKEN_BYTES) {
        // Such a row must exceed the token ceiling, regardless of tokenization.
        content = "";
        output.incomplete = true;
        return;
      }
      content += text;
    };
    const finishLine = (): void => {
      if (selected() && !output.incomplete) {
        const row = formatVerifiedRow(lineNumber, content);
        output.append(row);
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
    return {current: output.current, incomplete: output.incomplete, warning};
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


export function createHReadTool(description: string, grammar: string): Tool<string[]> {
  return createExecutorTool({
    name: "hread",
    description,
    grammar,
    argv(input, context) {
      const argumentsValue = hreadArguments(input);
      argumentsValue[0] = context.resolvePath(argumentsValue[0]);
      return argumentsValue;
    },
    async execute(argv) {
      try {
        const executionArguments = [...argv];
        if (executionArguments[0]?.startsWith("@shell/")) {
          const sessionID = process.env.CODEX_THREAD_ID;
          if (sessionID === undefined || sessionID === "") {
            throw new Error("CODEX_THREAD_ID is unavailable");
          }
          const runtimeDirectory = process.env.HPATCH_RUNTIME_DIR || tmpdir();
          executionArguments[0] = `${runtimeDirectory}/hpatch-${sessionID}/${executionArguments[0].slice("@shell/".length)}`;
        }
        const result = await readHashLines(parseReadSpec(stripOptionalFinalNewline(hreadInput(executionArguments))));
        const limitDiagnostic = result.incomplete
          ? `hread: ${VERIFIED_ROW_LIMIT_DIAGNOSTIC}`
          : "";
        const stderr = `${result.warning ?? ""}${limitDiagnostic}`;
        return {
          stdout: result.current,
          ...(stderr === "" ? {} : {stderr}),
          exitCode: result.incomplete ? 1 : 0,
        };
      } catch (error) {
        return {stderr: `hread: ${conciseErrorText(error)}\n`, exitCode: 1};
      }
    },
  });
}
