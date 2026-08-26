import {spawn, spawnSync} from "node:child_process";
import {closeSync, readFileSync, writeFileSync} from "node:fs";


function trimShebangField(value) {
  return value.replace(/^[ \t]+|[ \t]+$/gu, "");
}

function splitFirstLine(input) {
  const match = /\r\n|\n|\r/u.exec(input);
  if (match === null) {
    return {line: input, body: ""};
  }
  return {
    line: input.slice(0, match.index),
    body: input.slice(match.index + match[0].length),
  };
}


function parseDirectiveLine(line) {
  const assignment = /^#!([A-Za-z][A-Za-z0-9_-]*)=([\s\S]*)$/u.exec(line);
  if (assignment !== null) {
    return {key: assignment[1], value: assignment[2]};
  }
  const recoveredParams = /^(?:#[ \t]*!|!)params(?:[ \t]+([\s\S]+))?$/u.exec(line);
  if (recoveredParams !== null) {
    return {key: "params", value: recoveredParams[1] ?? ""};
  }
  return null;
}


function isDirectiveCandidate(line) {
  return parseDirectiveLine(line) !== null || /^#!(?:cmd|params)(?:[ \t]|$)/u.test(line);
}


function parseDirectives(body) {
  let remaining = body;
  let commandTemplate = "";
  let params;
  const seen = new Set();

  while (remaining !== "") {
    const first = splitFirstLine(remaining);
    const trimmedLine = trimShebangField(first.line);
    const directive = parseDirectiveLine(trimmedLine);
    if (directive === null) {
      if (/^#!(?:cmd|params)(?:[ \t]|$)/u.test(trimmedLine) || trimmedLine.startsWith("!")) {
        throw new Error("shell directive must use #!{key}={value}");
      }
      break;
    }

    const {key, value} = directive;
    if (key !== "cmd" && key !== "params") {
      throw new Error(`unsupported shell directive #!${key}`);
    }
    if (seen.has(key)) {
      throw new Error(`shell directive #!${key} must not occur more than once`);
    }
    seen.add(key);

    if (key === "cmd") {
      if (value === "") {
        throw new Error("command template must not be empty");
      }
      if (value.split("{.}").length !== 2) {
        throw new Error("command template must contain exactly one {.} placeholder");
      }
      commandTemplate = value;
    } else {
      let parsed;
      try {
        parsed = JSON.parse(value);
      } catch (error) {
        throw new Error(`#!params must contain a JSON object: ${executionError(error)}`);
      }
      if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
        throw new Error("#!params must contain a JSON object");
      }
      if (Object.hasOwn(parsed, "cmd")) {
        throw new Error("#!params must not contain cmd; the script body supplies it");
      }
      if (Object.hasOwn(parsed, "login") && parsed.login !== false) {
        throw new Error("#!params login must be false");
      }
      params = parsed;
    }
    remaining = first.body;
  }

  return {commandTemplate, params, body: remaining};
}

function parseScript(input, context) {
  if (input.includes("\0")) {
    throw new Error("script must not contain a NUL byte");
  }

  let interpreter = ["bash"];
  const retained = splitFirstLine(input);
  const retainedLine = trimShebangField(retained.line);
  if (retainedLine.startsWith("#!script=")) {
    if (retained.body !== "") {
      throw new Error("#!script must be the sole directive");
    }
    return parseScript(readFileSync(context.resolvePath(retainedLine.slice("#!script=".length)), "utf8"), context);
  }

  let body = input;
  const first = splitFirstLine(input);
  const trimmedLine = trimShebangField(first.line);
  const leadingDirective = isDirectiveCandidate(trimmedLine);
  if (trimmedLine.startsWith("#!") && !leadingDirective) {
    const selector = trimShebangField(trimmedLine.slice(2));
    if (selector === "") {
      throw new Error("shebang must select an interpreter");
    }

    interpreter = selector.split(/[ \t]+/u);
    if (interpreter[0] === "env" || interpreter[0] === "/usr/bin/env") {
      interpreter.shift();
      if (interpreter[0] === "-S") {
        interpreter.shift();
      }
      if (interpreter.length === 0 || interpreter[0].startsWith("-")) {
        throw new Error("env shebang must select an interpreter");
      }
    }
    body = first.body;
  }

  const directives = parseDirectives(body);
  return {
    interpreter,
    body: directives.body,
    commandTemplate: directives.commandTemplate,
    source: input,
    params: directives.params,
  };
}

function retainInput(input) {
  const interpreter = interpreterBasename(input.interpreter[0]).toLowerCase();
  if (interpreter !== "bash" && interpreter !== "sh") {
    return true;
  }
  const normalized = input.source.replaceAll("\r\n", "\n");
  return normalized.split(/\n|\r/u).length > 3;
}

function executionError(error) {
  return error instanceof Error ? error.message : String(error);
}

function interpreterBasename(interpreter) {
  return interpreter.replaceAll("\\", "/").split("/").at(-1)?.replace(/\.exe$/iu, "") ?? "";
}


function scriptEvaluationFlag(interpreter) {
  switch (interpreterBasename(interpreter).toLowerCase()) {
    case "bun":
    case "node":
    case "nodejs":
      return "-e";
    default:
      return null;
  }
}


function shellQuoteArgument(value) {
  if (/^[A-Za-z0-9_@%+=:,./-]+$/u.test(value)) {
    return value;
  }
  return `'${value.replaceAll("'", "'\"'\"'")}'`;
}


function stockEvaluationFlag(interpreter) {
  const executable = interpreterBasename(interpreter).toLowerCase();
  if (/^(?:python(?:[0-9]+(?:\.[0-9]+)*)?|pypy[0-9]*)$/u.test(executable)) {
    return "-c";
  }
  return scriptEvaluationFlag(interpreter);
}


function stockDelimiter(interpreter, body) {
  const executable = interpreterBasename(interpreter).toUpperCase();
  let delimiter;
  if (/^PYTHON[0-9.]*$/u.test(executable) || /^PYPY[0-9]*$/u.test(executable)) {
    delimiter = "PYTHON";
  } else if (/^(?:NODE|NODEJS)$/u.test(executable)) {
    delimiter = "NODE";
  } else {
    delimiter = executable.replace(/[^A-Z0-9_]/gu, "_") || "SCRIPT";
  }
  const lines = new Set(body.split(/\r\n|\n|\r/u));
  while (lines.has(delimiter)) {
    delimiter += "_";
  }
  return delimiter;
}


function stockCommand(input) {
  const [interpreter, ...interpreterArguments] = input.interpreter;
  const evaluationFlag = stockEvaluationFlag(interpreter);
  if (evaluationFlag !== null) {
    return [interpreter, ...interpreterArguments, evaluationFlag, input.body]
      .map(shellQuoteArgument)
      .join(" ");
  }

  const delimiter = stockDelimiter(interpreter, input.body);
  const bodyTerminator = input.body.endsWith("\n") ? "" : "\n";
  const command = [interpreter, ...interpreterArguments, "/dev/fd/3"]
    .map(shellQuoteArgument)
    .join(" ");
  return `${command} 3<<'${delimiter}'\n${input.body}${bodyTerminator}${delimiter}`;
}

function executeScriptThroughStdin(argv, maxOutputBytes) {
  const interpreter = argv[0];
  const interpreterArguments = argv.slice(1, -1);
  const body = argv.at(-1);

  try {
    const result = spawnSync(interpreter, interpreterArguments, {
      encoding: "utf8",
      env: process.env,
      input: body,
      maxBuffer: Math.max(1, Math.floor(maxOutputBytes / 2)),
    });
    const stdout = result.stdout ?? "";
    let stderr = result.stderr ?? "";

    if (result.error !== undefined) {
      stderr += `shell: ${executionError(result.error)}\n`;
      return {
        stdout,
        stderr,
        exitCode: result.error.code === "ENOENT" ? 127 : 1,
      };
    }
    if (result.signal !== null) {
      stderr += `shell: interpreter terminated by ${result.signal}\n`;
      return {stdout, stderr, exitCode: 1};
    }
    return {stdout, stderr, exitCode: result.status ?? 1};
  } catch (error) {
    return {stderr: `shell: ${executionError(error)}\n`, exitCode: 1};
  }
}

function executeScriptWithProgramInput(argv, context) {
  const interpreter = argv[0];
  const body = argv.at(-1);
  const evaluationFlag = scriptEvaluationFlag(interpreter);
  const usesDescriptor = evaluationFlag === null;
  const interpreterArguments = usesDescriptor
    ? [...argv.slice(1, -1), "/dev/fd/3"]
    : [...argv.slice(1, -1), evaluationFlag, body];

  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(interpreter, interpreterArguments, {
        env: process.env,
        stdio: usesDescriptor
          ? [context.stdinFD, "pipe", "pipe", context.scriptReadFD]
          : [context.stdinFD, "pipe", "pipe"],
      });
    } catch (error) {
      resolve({stderr: `shell: ${executionError(error)}\n`, exitCode: 1});
      return;
    }

    const overflowDiagnostic = `shell: interpreter output exceeds ${context.outputBudgetBytes} bytes\n`;
    const captureBudgetBytes = Math.max(
      0,
      context.outputBudgetBytes - Buffer.byteLength(overflowDiagnostic, "utf8") - 3,
    );
    const stdoutChunks = [];
    const stderrChunks = [];
    let capturedBytes = 0;
    let overflow = false;
    let spawnError;
    let scriptError;

    const capture = (chunks, chunk) => {
      const bytes = Buffer.from(chunk);
      const remaining = Math.max(0, captureBudgetBytes - capturedBytes);
      if (remaining > 0) {
        chunks.push(bytes.subarray(0, remaining));
      }
      if (bytes.length > remaining && !overflow) {
        overflow = true;
        child.kill("SIGKILL");
      }
      capturedBytes += Math.min(bytes.length, remaining);
    };
    child.stdout.on("data", (chunk) => {
      capture(stdoutChunks, chunk);
    });
    child.stderr.on("data", (chunk) => {
      capture(stderrChunks, chunk);
    });
    child.on("error", (error) => {
      spawnError = error;
    });
    child.on("close", (status, signal) => {
      let stdout;
      let stderr;
      try {
        stdout = new TextDecoder("utf-8", {fatal: true}).decode(Buffer.concat(stdoutChunks));
        stderr = new TextDecoder("utf-8", {fatal: true}).decode(Buffer.concat(stderrChunks));
      } catch {
        resolve({stderr: "shell: interpreter output is not UTF-8\n", exitCode: 1});
        return;
      }
      if (spawnError !== undefined) {
        stderr += `shell: ${executionError(spawnError)}\n`;
        resolve({
          stdout,
          stderr,
          exitCode: spawnError.code === "ENOENT" ? 127 : 1,
        });
        return;
      }
      if (overflow) {
        stderr += overflowDiagnostic;
        resolve({stdout, stderr, exitCode: 1});
        return;
      }
      if (scriptError !== undefined) {
        stderr += `shell: write script body: ${executionError(scriptError)}\n`;
        resolve({stdout, stderr, exitCode: 1});
        return;
      }
      if (signal !== null) {
        stderr += `shell: interpreter terminated by ${signal}\n`;
        resolve({stdout, stderr, exitCode: 1});
        return;
      }
      resolve({stdout, stderr, exitCode: status ?? 1});
    });

    try {
      if (usesDescriptor) {
        writeFileSync(context.scriptWriteFD, body);
      }
    } catch (error) {
      scriptError = error;
      child.kill("SIGKILL");
    } finally {
      try {
        closeSync(context.scriptWriteFD);
      } catch (error) {
        if (scriptError === undefined) {
          scriptError = error;
          child.kill("SIGKILL");
        }
      }
    }
  });
}

function executeScript(argv, context) {
  if (argv.length < 2) {
    return {stderr: "shell: missing interpreter or script body\n", exitCode: 1};
  }
  const interpreter = interpreterBasename(argv[0]).toLowerCase();
  // Bash and POSIX shell programs must pass through the router's mvdan/sh
  // runner so private commands cannot fall back to executable frontends.
  if (interpreter === "bash" || interpreter === "sh") {
    return {stderr: "shell: bash and sh require the router shell runner\n", exitCode: 1};
  }
  if (![context?.stdinFD, context?.scriptReadFD, context?.scriptWriteFD].every(
    (fileDescriptor) => Number.isSafeInteger(fileDescriptor) && fileDescriptor >= 3,
  )) {
    return executeScriptThroughStdin(argv, context.outputBudgetBytes);
  }
  return executeScriptWithProgramInput(argv, context);
}

export const shellTool = {
  specification: {
    type: "custom",
    name: "shell",
    description: `Run one free-form script. The selected interpreter receives the exact script body, and frontend standard input remains available as program data.`,
  },

  parse(input, context) {
    return parseScript(input, context);
  },

  argv(input) {
    return [...input.interpreter, input.body];
  },

  translate(input, api) {
    const template = input.commandTemplate === "" ? undefined : input.commandTemplate;
    return api.exec(template, input.params, stockCommand(input), retainInput(input));
  },

  execute(argv, context) {
    return executeScript(argv, context);
  },
};

export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "builtin.shell",
  tools: [shellTool],
};
