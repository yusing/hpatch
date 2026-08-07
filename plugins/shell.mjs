import {spawn, spawnSync} from "node:child_process";
import {closeSync, writeFileSync} from "node:fs";


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


function parseCommandTemplate(body) {
  const first = splitFirstLine(body);
  const trimmedLine = trimShebangField(first.line);
  if (!trimmedLine.startsWith("#!cmd=")) {
    return {commandTemplate: "", body};
  }

  const commandTemplate = trimmedLine.slice("#!cmd=".length);
  if (commandTemplate === "") {
    throw new Error("command template must not be empty");
  }
  if (commandTemplate.split("{.}").length !== 2) {
    throw new Error("command template must contain exactly one {.} placeholder");
  }
  return {commandTemplate, body: first.body};
}

function parseScript(input) {
  if (input.includes("\0")) {
    throw new Error("script must not contain a NUL byte");
  }

  let interpreter = ["bash"];
  let body = input;
  const first = splitFirstLine(input);
  const trimmedLine = trimShebangField(first.line);
  if (trimmedLine.startsWith("#!") && !trimmedLine.startsWith("#!cmd=")) {
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

  const command = parseCommandTemplate(body);
  return {interpreter, body: command.body, commandTemplate: command.commandTemplate};
}

function executionError(error) {
  return error instanceof Error ? error.message : String(error);
}

function scriptEvaluationFlag(interpreter) {
  const executable = interpreter.replaceAll("\\", "/").split("/").at(-1)?.toLowerCase();
  switch (executable) {
    case "bun":
    case "bun.exe":
    case "node":
    case "node.exe":
    case "nodejs":
    case "nodejs.exe":
      return "-e";
    default:
      return null;
  }
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
  if (![context?.stdinFD, context?.scriptReadFD, context?.scriptWriteFD].every(
    (fileDescriptor) => Number.isSafeInteger(fileDescriptor) && fileDescriptor >= 3,
  )) {
    return executeScriptThroughStdin(argv, context.outputBudgetBytes);
  }
  return executeScriptWithProgramInput(argv, context);
}

const shellTool = {
  specification: {
    type: "custom",
    name: "shell",
    description: `Run one free-form script without an outer heredoc or command-string quoting.
Use the first line as an optional shebang. A bare interpreter, a full path, and
/usr/bin/env forms are accepted. Without a shebang, bash runs the complete input.
An optional #!cmd= directive can follow the shebang or be the first line.
Its single {.} placeholder expands to the normalized shell frontend command.
The executor passes the exact body without shell parsing or a temporary file.
Bun and Node.js receive the body through their direct evaluation option.
Other interpreters receive the body through an anonymous descriptor.
The interpreter keeps frontend standard input available as program data.`,
  },

  parse(input) {
    return parseScript(input);
  },

  argv(input) {
    return [...input.interpreter, input.body];
  },

  translate(input, api) {
    return input.commandTemplate === "" ? api.exec() : api.exec(input.commandTemplate);
  },

  execute(argv, context) {
    return executeScript(argv, context);
  },
};

export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "example.shell",
  tools: [shellTool],
};
