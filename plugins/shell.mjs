import {spawn, spawnSync} from "node:child_process";
import {closeSync, readFileSync, writeFileSync} from "node:fs";
import {
  interpreterIdentity,
  parseShellHeader,
} from "hpatch:core/v1";

/**
 * parseScript parses a shell script using the shared-core shell header parser.
 */
function parseScript(input, context) {
  const parsed = parseShellHeader(input);
  if (Object.hasOwn(parsed, "scriptPath")) {
    return parseScript(readFileSync(context.resolvePath(parsed.scriptPath), "utf8"), context);
  }
  if (parsed.params !== undefined) {
    if (Object.hasOwn(parsed.params, "cmd")) {
      throw new Error("#!params must not contain cmd; the script body supplies it");
    }
    if (Object.hasOwn(parsed.params, "login") && parsed.params.login !== false) {
      throw new Error("#!params login must be false");
    }
  }
  return {
    interpreter: parsed.interpreter ?? ["bash"],
    body: parsed.body ?? "",
    commandTemplate: parsed.commandTemplate ?? "",
    source: input,
    params: parsed.params,
  };
}

/**
 * retainInput determines whether the script source should be retained for translation.
 */
function retainInput(input) {
  const interpreter = interpreterIdentity(input.interpreter[0]);
  if (interpreter !== "bash" && interpreter !== "sh") {
    return true;
  }
  const normalized = input.source.replaceAll("\r\n", "\n");
  return normalized.split(/\n|\r/u).length > 3;
}

/**
 * executionError extracts a string message from an error value.
 */
function executionError(error) {
  return error instanceof Error ? error.message : String(error);
}

/**
 * scriptEvaluationFlag returns the command-line flag for inline script evaluation.
 */
function scriptEvaluationFlag(interpreter) {
  switch (interpreterIdentity(interpreter)) {
    case "bun":
    case "node":
    case "nodejs":
      return "-e";
    default:
      return null;
  }
}


/**
 * executeScriptThroughStdin runs a script by passing the body through stdin.
 */
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

/**
 * executeScriptWithProgramInput runs a script using a file descriptor or evaluation flag.
 */
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

/**
 * executeScript executes a shell script with the appropriate interpreter.
 */
function executeScript(argv, context) {
  if (argv.length < 2) {
    return {stderr: "shell: missing interpreter or script body\n", exitCode: 1};
  }
  const interpreter = interpreterIdentity(argv[0]);
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
    return api.exec(template, input.params, retainInput(input));
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
