import {createHash} from "node:crypto";
import type {ExecutionContext, ExecutionResult, Tool, TranslationContext} from "../../plugin.d.ts";


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
