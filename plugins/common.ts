import {createHash} from "node:crypto";
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

export function hashLine(content: string): string {
  return createHash("sha256").update(content, "utf8").digest("hex").slice(0, 4);
}

export function formatHashLine(number: number, content: string): string {
  return `${number}:${hashLine(content)} ${content}\n`;
}

export function isOutsideWorkspace(root: string, target: string): boolean {
  const relative = path.relative(root, target);
  return relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative);
}

export function countGPT5Tokens(value: string): number {
  return countGPT5TokensWithModel(value, sourceTokenOptions);
}

export class VerifiedRowOutput {
  current = "";
  stock = "";
  incomplete = false;
  #sealed = false;

  append(currentRow: string, stockRow: string): boolean {
    if (this.#sealed) {
      this.incomplete = true;
      return false;
    }
    const candidate = this.current + currentRow;
    const tokens = countGPT5Tokens(candidate);
    if (tokens > VERIFIED_ROW_MAX_TOKENS) {
      this.incomplete = true;
      return false;
    }
    this.current = candidate;
    this.stock += stockRow;
    this.#sealed = tokens > VERIFIED_ROW_SOFT_TOKENS;
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

export function shellQuoteArgument(value: string): string {
  if (/^[A-Za-z0-9_@%+=:,./-]+$/u.test(value)) {
    return value;
  }
  return `'${value.replaceAll("'", "'\"'\"'")}'`;
}

type ExecutorToolOptions = {
  name: string;
  description: string;
  grammar: string;
  argv(input: string, context: TranslationContext): string[] | Promise<string[]>;
  execute(argv: string[], context: ExecutionContext): ExecutionResult | Promise<ExecutionResult>;
  stockCommand?(input: string[]): string;
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
      return api.exec(undefined, undefined, options.stockCommand?.(input));
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
