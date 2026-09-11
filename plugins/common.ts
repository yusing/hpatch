import {formatVerifiedRow, hashLine} from "mekugi:core/v1";
import path from "node:path";
import {countTokens as countGPT5TokensWithModel} from "gpt-tokenizer/model/gpt-5";
import type {ExecutionContext, ExecutionResult, Tool, TranslationContext} from "../internal/router/toolplugin/plugin.d.ts";

const VERIFIED_ROW_SOFT_TOKENS = 15_000;
export const VERIFIED_ROW_MAX_TOKENS = 15_500;
// The pinned GPT-5 vocabulary's longest token is 128 UTF-8 bytes. This bounds
// retained candidate storage without introducing a separate admission policy.
export const MAX_POSSIBLE_GPT5_TOKEN_BYTES = 128;
export const VERIFIED_ROW_LIMIT_DIAGNOSTIC = "output incomplete: 15,000-token limit reached\n";
// Source rows may contain tokenizer control spellings; they remain ordinary source text.
const sourceTokenOptions = {disallowedSpecial: new Set<string>()};

export function byteLength(value: string): number {
  return Buffer.byteLength(value, "utf8");
}

export function isOutsideWorkspace(root: string, target: string): boolean {
  const relative = path.relative(root, target);
  return relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative);
}

export function countGPT5Tokens(value: string): number {
  return countGPT5TokensWithModel(value, sourceTokenOptions);
}

export type ReaderOptions = {maxTokens?: number; previewBytes?: number};

export function readerOptions(argv: string[]): {options: ReaderOptions; rest: string[]; offset: number} {
  const options: ReaderOptions = {};
  let offset = 0;
  while (argv[offset] === "--max-tokens" || argv[offset] === "--preview-bytes") {
    const name = argv[offset];
    const key = name === "--max-tokens" ? "maxTokens" : "previewBytes";
    const maximum = key === "maxTokens" ? VERIFIED_ROW_MAX_TOKENS : 65_536;
    const raw = argv[offset + 1] ?? "";
    const value = Number(raw);
    if (options[key] !== undefined || !/^[1-9][0-9]*$/u.test(raw)
        || !Number.isSafeInteger(value) || value > maximum) {
      throw new Error(`${name} requires one integer from 1 to ${maximum} and cannot repeat`);
    }
    options[key] = value;
    offset += 2;
  }
  return {options, rest: argv.slice(offset), offset};
}

export function readerLimitDiagnostic(options: ReaderOptions): string {
  return options.maxTokens === undefined
    ? VERIFIED_ROW_LIMIT_DIAGNOSTIC
    : `output incomplete: ${options.maxTokens}-token limit reached\n`;
}

// Preview records never impersonate exact source rows. Their identity still
// hashes the entire logical row through the portable core.
export function formatReaderRow(line: number, content: string, options: ReaderOptions, path?: string): string {
  if (options.previewBytes === undefined) {
    return `${path === undefined ? "" : `${JSON.stringify(path)}:`}${formatVerifiedRow(line, content)}`;
  }
  const bytes = Buffer.from(content, "utf8");
  let end = Math.min(bytes.length, options.previewBytes);
  while (end > 0 && end < bytes.length && (bytes[end] & 0xc0) === 0x80) {
    end -= 1;
  }
  return `${JSON.stringify({
    ...(path === undefined ? {} : {path}),
    row: `${line}:${hashLine(content)}`,
    preview: bytes.subarray(0, end).toString("utf8"),
    source_bytes: bytes.length,
    omitted_bytes: bytes.length - end,
  })}\n`;
}

export class VerifiedRowOutput {
  current = "";
  incomplete = false;
  #sealed = false;

  constructor(private readonly maxTokens?: number) {}

  append(currentRow: string): boolean {
    if (this.#sealed) {
      this.incomplete = true;
      return false;
    }
    const candidate = this.current + currentRow;
    const tokens = countGPT5Tokens(candidate);
    if (tokens > (this.maxTokens ?? VERIFIED_ROW_MAX_TOKENS)) {
      this.incomplete = true;
      return false;
    }
    this.current = candidate;
    this.#sealed = this.maxTokens === undefined && tokens > VERIFIED_ROW_SOFT_TOKENS;
    return true;
  }
}

export function errorText(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

export function decodeUTF8(value: Uint8Array, label: string): string {
  try {
    return new TextDecoder("utf-8", {fatal: true}).decode(value);
  } catch {
    throw new Error(`${label} is not UTF-8`);
  }
}

export function stripOptionalFinalNewline(value: string): string {
  if (value.endsWith("\r\n")) {
    return value.slice(0, -2);
  }
  if (value.endsWith("\n")) {
    return value.slice(0, -1);
  }
  return value;
}

type ExecutorToolOptions = {
  name: string;
  description: string;
  grammar: string;
  argv(input: string, context: TranslationContext): string[] | Promise<string[]>;
  execute(argv: string[], context: ExecutionContext): ExecutionResult | Promise<ExecutionResult>;
};

export function createExecutorTool(options: ExecutorToolOptions): Tool<string[]> {
  return {
    specification: {
      type: "custom",
      name: options.name,
      description: options.description,
      format: {type: "grammar", syntax: "regex", definition: options.grammar},
    },
    parse(input, context) {
      return options.argv(input, context);
    },
    argv(input) {
      return input;
    },
    translate(input, api) {
      return api.exec();
    },
    execute: options.execute,
  };
}

export async function collect(stream: AsyncIterable<Uint8Array>): Promise<Uint8Array> {
  const chunks: Uint8Array[] = [];
  let length = 0;
  try {
    for await (const chunk of stream) {
      chunks.push(chunk);
      length += chunk.byteLength;
    }
  } catch (error) {
    if (!(error instanceof Error) || !("code" in error) || error.code !== "ERR_STREAM_PREMATURE_CLOSE") {
      throw error;
    }
  }
  return Buffer.concat(chunks, length);
}
