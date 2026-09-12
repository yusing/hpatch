import type {ExecutionOutput} from "../internal/router/toolplugin/plugin.d.ts";
import {countGPT5Tokens, encodeGPT5, tokenBytes} from "./tokens.ts";

export function selectHRunText(value: string, budget: number, tail: boolean): {text: string; tokens: number} {
  if (budget === 0) {
    return {text: "", tokens: 0};
  }
  const tokens = encodeGPT5(value);
  if (tokens.length <= budget) {
    return {text: value, tokens: tokens.length};
  }
  const decoder = new TextDecoder("utf-8", {fatal: true, ignoreBOM: true});
  for (let keep = budget; keep > 0;) {
    let bytes = Buffer.concat((tail ? tokens.slice(-keep) : tokens.slice(0, keep)).map(tokenBytes));
    let text = "";
    // Encoding valid text can still split a character at the selected token edge.
    while (bytes.length > 0) {
      try {
        text = decoder.decode(bytes);
        break;
      } catch {
        bytes = tail ? bytes.subarray(1) : bytes.subarray(0, -1);
      }
    }
    const count = countGPT5Tokens(text);
    if (count <= budget) {
      return {text, tokens: count};
    }
    keep -= Math.max(1, count - budget);
  }
  return {text: "", tokens: 0};
}

// Private shell-executor formatting operation. It never starts a command.
export function formatHRunOutput(argv: string[]): ExecutionOutput {
  const [rawBudget, mode, stdout, stderr] = argv;
  const budget = Number(rawBudget);
  if (argv.length !== 4 || !/^[1-9][0-9]*$/u.test(rawBudget)
      || budget > 15_500 || (mode !== "head" && mode !== "tail")) {
    throw new Error("invalid hrun output selection");
  }
  const error = selectHRunText(stderr, budget, mode === "tail");
  const output = selectHRunText(stdout, budget - error.tokens, mode === "tail");
  return {stdout: output.text, stderr: error.text, exitCode: 0};
}
