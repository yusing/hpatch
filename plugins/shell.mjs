import {spawn, spawnSync} from "node:child_process";
import {closeSync, writeFileSync} from "node:fs";

const maxInputBytes = 64 * 1024;
const maxArgvItems = 256;
const maxArgBytes = 64 * 1024;
const maxOutputBytesPerStream = 8 * 1024 * 1024 - 4096;

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

function validateArgv(interpreter, body) {
  const argv = [...interpreter, body];
  if (argv.length > maxArgvItems) {
    throw new Error(`shebang selects more than ${maxArgvItems - 1} interpreter fields`);
  }
  if (argv.some((value) => Buffer.byteLength(value, "utf8") > maxArgBytes)) {
    throw new Error(`shell arguments must not exceed ${maxArgBytes} UTF-8 bytes each`);
  }
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
  if (Buffer.byteLength(input, "utf8") > maxInputBytes) {
    throw new Error(`script must not exceed ${maxInputBytes} UTF-8 bytes`);
  }
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
  validateArgv(interpreter, command.body);
  return {interpreter, body: command.body, commandTemplate: command.commandTemplate};
}

function executionError(error) {
  return error instanceof Error ? error.message : String(error);
}

function executeScriptThroughStdin(argv) {
  const interpreter = argv[0];
  const interpreterArguments = argv.slice(1, -1);
  const body = argv.at(-1);

  try {
    const result = spawnSync(interpreter, interpreterArguments, {
      encoding: "utf8",
      env: process.env,
      input: body,
      maxBuffer: maxOutputBytesPerStream,
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
  const interpreterArguments = [...argv.slice(1, -1), "/dev/fd/3"];
  const body = argv.at(-1);

  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(interpreter, interpreterArguments, {
        env: process.env,
        stdio: [context.stdinFD, "pipe", "pipe", context.scriptReadFD],
      });
    } catch (error) {
      resolve({stderr: `shell: ${executionError(error)}\n`, exitCode: 1});
      return;
    }

    const stdoutChunks = [];
    const stderrChunks = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let overflow = false;
    let spawnError;
    let scriptError;

    const capture = (chunks, currentBytes, chunk) => {
      const bytes = Buffer.from(chunk);
      const remaining = Math.max(0, maxOutputBytesPerStream - currentBytes);
      if (remaining > 0) {
        chunks.push(bytes.subarray(0, remaining));
      }
      if (bytes.length > remaining && !overflow) {
        overflow = true;
        child.kill("SIGKILL");
      }
      return currentBytes + bytes.length;
    };
    child.stdout.on("data", (chunk) => {
      stdoutBytes = capture(stdoutChunks, stdoutBytes, chunk);
    });
    child.stderr.on("data", (chunk) => {
      stderrBytes = capture(stderrChunks, stderrBytes, chunk);
    });
    child.on("error", (error) => {
      spawnError = error;
    });
    child.on("close", (status, signal) => {
      const stdout = Buffer.concat(stdoutChunks).toString("utf8");
      let stderr = Buffer.concat(stderrChunks).toString("utf8");
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
        stderr += `shell: interpreter output exceeds ${maxOutputBytesPerStream} bytes per stream\n`;
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
      writeFileSync(context.scriptWriteFD, body);
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
    return executeScriptThroughStdin(argv);
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
The executor uses an anonymous descriptor for the exact remaining script body.
The interpreter keeps frontend standard input available as program data.`,
  },
  maxInputBytes,

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
