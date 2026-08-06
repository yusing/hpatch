import {createHash} from "node:crypto";
import type {ExecutionResult, Tool} from "../../plugin.d.ts";

export const MAX_INPUT_BYTES = 64 * 1024;
export const MAX_OUTPUT_BYTES = 16 * 1024 * 1024;

export function byteLength(value: string): number {
  return Buffer.byteLength(value, "utf8");
}

export function hashLine(content: string): string {
  return createHash("sha256").update(content, "utf8").digest("hex").slice(0, 4);
}

export function formatHashLine(number: number, content: string): string {
  return `${number}:${hashLine(content)} ${content}\n`;
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

type ExecutorToolOptions = {
  name: string;
  description: string;
  grammar: string;
  argv(input: string): string[] | Promise<string[]>;
  execute(argv: string[]): ExecutionResult | Promise<ExecutionResult>;
};

export function createExecutorTool(options: ExecutorToolOptions): Tool<string> {
  return {
    specification: {
      type: "custom",
      name: options.name,
      description: options.description,
      format: {type: "grammar", syntax: "lark", definition: options.grammar},
    },
    maxInputBytes: MAX_INPUT_BYTES,
    parse(input) {
      return input;
    },
    argv: options.argv,
    translate(_input, api) {
      return api.exec();
    },
    execute: options.execute,
  };
}
