import {spawnSync} from "node:child_process";

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

function parseScript(input) {
  if (Buffer.byteLength(input, "utf8") > maxInputBytes) {
    throw new Error(`script must not exceed ${maxInputBytes} UTF-8 bytes`);
  }
  if (input.includes("\0")) {
    throw new Error("script must not contain a NUL byte");
  }

  const first = splitFirstLine(input);
  const trimmedLine = trimShebangField(first.line);
  if (!trimmedLine.startsWith("#!")) {
    const result = {interpreter: ["bash"], body: input};
    validateArgv(result.interpreter, result.body);
    return result;
  }

  const selector = trimShebangField(trimmedLine.slice(2));
  if (selector === "") {
    throw new Error("shebang must select an interpreter");
  }

  const interpreter = selector.split(/[ \t]+/u);
  if (interpreter[0] === "env" || interpreter[0] === "/usr/bin/env") {
    interpreter.shift();
    if (interpreter[0] === "-S") {
      interpreter.shift();
    }
    if (interpreter.length === 0 || interpreter[0].startsWith("-")) {
      throw new Error("env shebang must select an interpreter");
    }
  }

  validateArgv(interpreter, first.body);
  return {interpreter, body: first.body};
}

function executionError(error) {
  return error instanceof Error ? error.message : String(error);
}

function executeScript(argv) {
  if (argv.length < 2) {
    return {stderr: "shell: missing interpreter or script body\n", exitCode: 1};
  }

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

const shellTool = {
  specification: {
    type: "custom",
    name: "shell",
    description: `Run one free-form script without an outer heredoc or command-string quoting.
Use the first line as an optional shebang. A bare interpreter, a full path, and
/usr/bin/env forms are accepted. Without a shebang, bash runs the complete input.
The translated Codex exec command shows the normalized interpreter and exact script body.
The executor sends that body to the interpreter through standard input.`,
  },
  maxInputBytes,

  parse(input) {
    return parseScript(input);
  },

  argv(input) {
    return [...input.interpreter, input.body];
  },

  translate(_input, api) {
    return api.exec();
  },

  execute(argv) {
    return executeScript(argv);
  },
};

export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "example.shell",
  tools: [shellTool],
};
