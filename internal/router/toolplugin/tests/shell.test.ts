import {afterEach, describe, expect, test} from "bun:test";
import {spawn, spawnSync, type ChildProcessWithoutNullStreams} from "node:child_process";
import {closeSync, openSync} from "node:fs";
import {lstat, mkdtemp, mkdir, readFile, readdir, rm, stat, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import {fileURLToPath} from "node:url";

import plugin from "../../../../plugins/shell.mjs";

const originalCWD = process.cwd();
const originalTMPDIR = process.env.TMPDIR;
const originalTestValue = process.env.HPATCH_SHELL_TEST;
const temporaryDirectories: string[] = [];
const tool = plugin.tools[0];

test("description stays call-local", () => {
  expect(tool.specification.description).toBe(
    "Run one free-form script. The selected interpreter receives the exact script body, and frontend standard input remains available as program data.",
  );
  for (const persistentGuidance of ["#!cmd=", "#!script=", "@shell/", "default interpreter"]) {
    expect(tool.specification.description).not.toContain(persistentGuidance);
  }
});
const hostPath = fileURLToPath(new URL("../host.mjs", import.meta.url));
const repositoryRoot = fileURLToPath(new URL("../../../../", import.meta.url));
const stockBaseInstructions = [
  "# Codex base",
  "- When you search for text or files, you reach first for `rg` or `rg --files`; they are much faster than alternatives like `grep`. If `rg` is unavailable, you use the next best tool without fuss.",
  "- Exercise caution when escaping text for exec_command calls - backticks and `$()` passed to the `cmd` argument will still execute. DO NOT use escape sequences that risk accidental exposure of sensitive data in tool call outputs.",
  "## File editing constraints",
  "",
  "Use `apply_patch` for local file edits. Do not create or edit files with `cat` or other shell write tricks. Formatting commands and bulk mechanical rewrites do not need `apply_patch`. Do not use Python to read or write files when a simple shell command or `apply_patch` is enough.",
  "",
  "## Remaining instructions",
  "",
  "Preserve unrelated behavior.",
  "",
].join("\n");

async function temporaryDirectory(prefix: string): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

function waitForListening(router: ChildProcessWithoutNullStreams): Promise<string> {
  return new Promise((resolve, reject) => {
    let stderr = "";
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error(`router did not become ready:\n${stderr}`));
    }, 5000);
    const onData = (chunk: Buffer) => {
      stderr += chunk.toString("utf8");
      if (stderr.includes("msg=listening")) {
        cleanup();
        resolve(stderr);
      }
    };
    const onExit = (code: number | null, signal: NodeJS.Signals | null) => {
      cleanup();
      reject(new Error(`router exited before readiness with code ${code}, signal ${signal}:\n${stderr}`));
    };
    const cleanup = () => {
      clearTimeout(timer);
      router.stderr.off("data", onData);
      router.off("exit", onExit);
    };
    router.stderr.on("data", onData);
    router.once("exit", onExit);
  });
}

afterEach(async () => {
  process.chdir(originalCWD);
  if (originalTMPDIR === undefined) {
    delete process.env.TMPDIR;
  } else {
    process.env.TMPDIR = originalTMPDIR;
  }
  if (originalTestValue === undefined) {
    delete process.env.HPATCH_SHELL_TEST;
  } else {
    process.env.HPATCH_SHELL_TEST = originalTestValue;
  }
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) => rm(directory, {recursive: true, force: true})),
  );
});

describe("installable shell plugin", () => {
  test("normalizes shebangs and preserves the exact body", async () => {
    expect(tool.specification).toMatchObject({
      type: "custom",
      name: "shell",
    });
    expect(tool.specification.format).toBeUndefined();
    const body = "  print(\"Hello\")  \n";
    for (const input of [
      `#!python3\n${body}`,
      `#! python3 \n${body}`,
      `#!/usr/bin/env python3\n${body}`,
    ]) {
      const parsed = await tool.parse(input);
      expect(await tool.argv(parsed)).toEqual(["python3", body]);
    }

    const splitSelector = await tool.parse("#!/usr/bin/env -S python3 -u\r\nprint('ok')\r\n");
    expect(await tool.argv(splitSelector)).toEqual(["python3", "-u", "print('ok')\r\n"]);

    const directPath = await tool.parse("#!/opt/python/bin/python3\nprint('ok')");
    expect(await tool.argv(directPath)).toEqual([
      "/opt/python/bin/python3",
      "print('ok')",
    ]);

    const withoutShebang = " \nprintf 'ok'\n ";
    const bash = await tool.parse(withoutShebang);
    expect(await tool.argv(bash)).toEqual(["bash", withoutShebang]);

    expect(await tool.translate(bash, {exec: () => ({kind: "exec"})})).toEqual({kind: "exec"});
  });

  test("classifies retention and resolves retained references", async () => {
    const context = {resolvePath: (value: string) => value};
    const retention = async (input: string) => {
      const parsed = await tool.parse(input, context);
      const carrier = await tool.translate(parsed, {
        exec: (_template, _params, _stockCommand, retainInput) => ({kind: "exec", retainInput}),
      });
      return carrier.retainInput;
    };

    expect(await retention("one\ntwo\nthree")).toBe(false);
    expect(await retention("one\ntwo\nthree\nfour")).toBe(true);
    expect(await retention("#!sh\none\ntwo")).toBe(false);
    expect(await retention("#!sh\none\ntwo\nthree")).toBe(true);
    expect(await retention("#!python3\npass")).toBe(true);

    const directory = await temporaryDirectory("hpatch-shell-reference-");
    const stored = path.join(directory, "call-id");
    await writeFile(stored, "#!python3\nprint('stored')\n");
    const parsed = await tool.parse("#!script=@shell/call-id", {
      resolvePath: (value: string) => value === "@shell/call-id" ? stored : value,
    });
    expect(await tool.argv(parsed)).toEqual(["python3", "print('stored')\n"]);
  });

  test("supplies executable interpreter-specific stock exec commands", async () => {
    const translate = async (input: string) => {
      const parsed = await tool.parse(input);
      return tool.translate(parsed, {
        exec: (
          template?: string,
          params?: Record<string, unknown>,
          stockCommand?: string,
        ) => ({kind: "exec", template, params, stockCommand}),
      });
    };

    expect((await translate("#!/usr/bin/python3 -u\nprint(\"ok\")")).stockCommand).toBe(
      "/usr/bin/python3 -u -c 'print(\"ok\")'",
    );
    expect((await translate("#!nodejs\nconsole.log(\"ok\")")).stockCommand).toBe(
      "nodejs -e 'console.log(\"ok\")'",
    );
    expect((await translate("#!/opt/bun.exe\nconsole.log(\"ok\")")).stockCommand).toBe(
      "/opt/bun.exe -e 'console.log(\"ok\")'",
    );
    expect((await translate("printf ok")).stockCommand).toBe(
      "bash /dev/fd/3 3<<'BASH'\nprintf ok\nBASH",
    );
    expect((await translate("BASH\n")).stockCommand).toBe(
      "bash /dev/fd/3 3<<'BASH_'\nBASH\nBASH_",
    );

    for (const [input, output] of [
      ["#!python3\nprint(\"python\")", "python\n"],
      ["#!node\nprocess.stdout.write(\"node\")", "node"],
      [`#!${process.execPath}\nprocess.stdout.write("bun")`, "bun"],
    ]) {
      const command = (await translate(input)).stockCommand;
      const executed = spawnSync("bash", ["-c", command], {encoding: "utf8"});
      expect(executed.status).toBe(0);
      expect(executed.stdout).toBe(output);
      expect(executed.stderr).toBe("");
    }

    const bashCommand = (await translate("IFS= read -r value; printf 'bash:%s' \"$value\"")).stockCommand;
    const bashExecuted = spawnSync("bash", ["-c", bashCommand], {
      encoding: "utf8",
      input: "program-input\n",
    });
    expect(bashExecuted.status).toBe(0);
    expect(bashExecuted.stdout).toBe("bash:program-input");
    expect(bashExecuted.stderr).toBe("");

    const carriageCommand = (await translate("printf cr\r")).stockCommand;
    expect(carriageCommand).toBe("bash /dev/fd/3 3<<'BASH'\nprintf cr\r\nBASH");
    const carriageExecuted = spawnSync("bash", ["-c", carriageCommand], {encoding: "utf8"});
    expect(carriageExecuted.status).toBe(0);
    expect(carriageExecuted.stdout).toBe("cr\r");
    expect(carriageExecuted.stderr).toBe("");
  });

  test("expands a command directive after an optional interpreter shebang", async () => {
    const template = "curl -fsSL URL | {.} | jq";
    const exec = (commandTemplate?: string) => (
      commandTemplate === undefined ? {kind: "exec"} : {kind: "exec", template: commandTemplate}
    );

    const bash = await tool.parse(`#!cmd=${template}\nprint('bash')`);
    expect(await tool.argv(bash)).toEqual(["bash", "print('bash')"]);
    expect(await tool.translate(bash, {exec})).toEqual({kind: "exec", template});

    const python = await tool.parse(`#!python3\r\n#!cmd=${template}\r\nprint('python')\r\n`);
    expect(await tool.argv(python)).toEqual(["python3", "print('python')\r\n"]);
    expect(await tool.translate(python, {exec})).toEqual({kind: "exec", template});

    const laterDirective = await tool.parse(`#!python3\nprint('python')\n#!cmd=${template}`);
    expect(await tool.argv(laterDirective)).toEqual([
      "python3",
      `print('python')\n#!cmd=${template}`,
    ]);
    expect(await tool.translate(laterDirective, {exec})).toEqual({kind: "exec"});
  });

  test("passes leading JSON params through the exec carrier", async () => {
    const template = "env EXAMPLE=value {.}";
    const params = {workdir: "/tmp/example", tty: true, "yield_time_ms": 30000, login: false};
    const exec = (commandTemplate?: string, commandParams?: Record<string, unknown>) => ({
      kind: "exec",
      ...(commandTemplate === undefined ? {} : {template: commandTemplate}),
      ...(commandParams === undefined ? {} : {params: commandParams}),
    });

    for (const input of [
      `#!python3\n#!params=${JSON.stringify(params)}\n#!cmd=${template}\nprint('ok')`,
      `#!python3\n#!cmd=${template}\n#!params=${JSON.stringify(params)}\nprint('ok')`,
    ]) {
      const parsed = await tool.parse(input);
      expect(await tool.argv(parsed)).toEqual(["python3", "print('ok')"]);
      expect(await tool.translate(parsed, {exec})).toEqual({kind: "exec", template, params});
    }

    const laterDirective = await tool.parse("printf 'body'\n#!params={\"tty\":true}");
    expect(await tool.argv(laterDirective)).toEqual(["bash", "printf 'body'\n#!params={\"tty\":true}"]);
    expect(await tool.translate(laterDirective, {exec})).toEqual({kind: "exec"});
  });

  test("recovers safe params near-misses in directive position", async () => {
    const params = {workdir: "/tmp"};
    const exec = (commandTemplate?: string, commandParams?: Record<string, unknown>) => ({
      kind: "exec",
      ...(commandTemplate === undefined ? {} : {template: commandTemplate}),
      ...(commandParams === undefined ? {} : {params: commandParams}),
    });
    for (const input of [
      `# !params ${JSON.stringify(params)}\nprintf ok`,
      `#!params ${JSON.stringify(params)}\nprintf ok`,
      `!params ${JSON.stringify(params)}\nprintf ok`,
      `#!/usr/bin/env bash\n# !params ${JSON.stringify(params)}\nprintf ok`,
    ]) {
      const parsed = await tool.parse(input);
      expect(await tool.argv(parsed)).toEqual(["bash", "printf ok"]);
      expect(await tool.translate(parsed, {exec})).toEqual({kind: "exec", params});
    }
  });

  test("rejects malformed or unsafe input before execution", async () => {
    for (const input of [
      "#!",
      "#!   ",
      "#!/usr/bin/env",
      "#!/usr/bin/env -S",
      "#!/usr/bin/env -u python3",
      "printf '\\0'\0",
      "#!cmd=",
      "#!cmd {.}\nprintf ok",
      "#!cmd=printf ok",
      "#!cmd={.} && {.}",
      "#!params=",
      "#!params=[]",
      "#!params={bad}",
      "#!params={\"cmd\":\"forbidden\"}",
      "#!params={\"login\":true}",
      "#!params={\"login\":null}",
      "#!params={\"login\":1}",
      "#!params={\"tty\":true}\n#!params={\"workdir\":\"/tmp\"}\nprintf ok",
      "!unknown value\nprintf ok",
    ]) {
      expect(() => tool.parse(input)).toThrow();
    }
  });

  test("executes the exact body and inherits cwd and environment", async () => {
    const temporaryRoot = await temporaryDirectory("shell-plugin-tmp-");
    const workingRoot = await temporaryDirectory("shell-plugin-cwd-");
    const workingDirectory = path.join(workingRoot, "work");
    await mkdir(workingDirectory);
    process.env.TMPDIR = temporaryRoot;
    process.env.HPATCH_SHELL_TEST = "inherited";
    process.chdir(workingDirectory);

    const input = [
      "#!/usr/bin/env -S bash -eu",
      "printf '%s|%s' \"$PWD\" \"$HPATCH_SHELL_TEST\"",
      "exit 7",
      "",
    ].join("\n");
    const parsed = await tool.parse(input);
    const result = await tool.execute(await tool.argv(parsed), {stdinFD: null, scriptReadFD: null, scriptWriteFD: null, outputBudgetBytes: 16 * 1024 * 1024});

    expect(result).toEqual({
      stdout: `${workingDirectory}|inherited`,
      stderr: "",
      exitCode: 7,
    });
    expect(await readdir(temporaryRoot)).toEqual([]);
  });

  test("reports an unavailable interpreter", async () => {
    const parsed = await tool.parse("#!hpatch-missing-interpreter\nignored");
    const result = await tool.execute(await tool.argv(parsed), {stdinFD: null, scriptReadFD: null, scriptWriteFD: null, outputBudgetBytes: 16 * 1024 * 1024});

    expect(result.exitCode).toBe(127);
    expect(result.stderr).toContain("not found");
  });

  test("keeps an overflow diagnostic inside the shared output budget", async () => {
    const nullDevice = process.platform === "win32" ? "NUL" : "/dev/null";
    const inputFD = openSync(nullDevice, "r");
    const scriptReadFD = openSync(nullDevice, "r");
    const scriptWriteFD = openSync(nullDevice, "r");
    try {
      const budget = 128;
      const result = await tool.execute(
        [process.execPath, "process.stdout.write('x'.repeat(200))"],
        {
          stdinFD: inputFD,
          scriptReadFD,
          scriptWriteFD,
          outputBudgetBytes: budget,
        },
      );
      expect(result.exitCode).toBe(1);
      expect(result.stderr).toContain("interpreter output exceeds");
      expect(Buffer.byteLength((result.stdout ?? "") + (result.stderr ?? ""), "utf8")).toBeLessThanOrEqual(budget);
    } finally {
      closeSync(inputFD);
      closeSync(scriptReadFD);
    }
  });

  test("bounds malformed UTF-8 interpreter output", async () => {
    const nullDevice = process.platform === "win32" ? "NUL" : "/dev/null";
    const inputFD = openSync(nullDevice, "r");
    const scriptReadFD = openSync(nullDevice, "r");
    const scriptWriteFD = openSync(nullDevice, "r");
    try {
      const budget = 128;
      const result = await tool.execute(
        [process.execPath, "process.stdout.write(Buffer.alloc(200, 0xff))"],
        {
          stdinFD: inputFD,
          scriptReadFD,
          scriptWriteFD,
          outputBudgetBytes: budget,
        },
      );
      expect(result.exitCode).toBe(1);
      expect(result.stderr).toContain("output is not UTF-8");
      expect(Buffer.byteLength((result.stdout ?? "") + (result.stderr ?? ""), "utf8")).toBeLessThanOrEqual(budget);
    } finally {
      closeSync(inputFD);
      closeSync(scriptReadFD);
    }
  });

  test("validates and executes through the production plugin host", () => {
    const validated = spawnSync("node", [hostPath], {
      cwd: repositoryRoot,
      encoding: "utf8",
      env: {...process.env, NODE_NO_WARNINGS: "1"},
      input: JSON.stringify({
        operation: "validate",
        snapshotRoot: repositoryRoot,
        modules: ["plugins/shell.mjs"],
      }),
    });

    expect(validated.status).toBe(0);
    expect(JSON.parse(validated.stdout)).toMatchObject({
      errors: [],
      plugins: [{
        id: "builtin.shell",
        tools: [{specification: {type: "custom", name: "shell"}}],
      }],
    });
    const executed = spawnSync("node", [hostPath], {
      cwd: repositoryRoot,
      encoding: "utf8",
      env: {...process.env, HPATCH_SHELL_HOST_TEST: "stdin", NODE_NO_WARNINGS: "1"},
      input: JSON.stringify({
        operation: "execute",
        outputBudgetBytes: 16 * 1024 * 1024,
        snapshotRoot: repositoryRoot,
        module: "plugins/shell.mjs",
        index: 0,
        arguments: ["bash", "printf 'host:%s' \"$HPATCH_SHELL_HOST_TEST\""],
      }),
    });

    expect(executed.status).toBe(0);
    expect(JSON.parse(executed.stdout)).toEqual({
      stdout: "host:stdin",
      stderr: "",
      exitCode: 0,
    });
  });

  test("make install provides frontends and Codex model instructions", async () => {
    const installRoot = await temporaryDirectory("shell-plugin-install-");
    const binaryDirectory = path.join(installRoot, "bin");
    const configDirectory = path.join(installRoot, "config");
    const routerPath = path.join(binaryDirectory, "hpatch-router");
    const shellPath = path.join(binaryDirectory, "shell");
    const hreadPath = path.join(binaryDirectory, "hread");
    const installedPlugin = path.join(configDirectory, "hpatch", "plugins", "shell.mjs");
    const codexHome = path.join(installRoot, "codex-home");
    const configPath = path.join(codexHome, "config.toml");
    const instructionsPath = path.join(codexHome, "hpatch-model-instructions.md");
    const fakeCodex = path.join(installRoot, "codex");
    await mkdir(binaryDirectory, {recursive: true});
    await writeFile(
      fakeCodex,
      `#!/bin/sh\nprintf '%s\\n' '${JSON.stringify({models: [{
        slug: "test-model",
        priority: 1,
        base_instructions: stockBaseInstructions,
      }]})}'\n`,
      {mode: 0o700},
    );
    const installEnvironment = {
      ...process.env,
      GOBIN: binaryDirectory,
      GOTELEMETRY: "off",
      XDG_CONFIG_HOME: configDirectory,
      CODEX: fakeCodex,
      PATH: `${installRoot}${path.delimiter}${process.env.PATH ?? ""}`,
      CODEX_HOME: codexHome,
    };
    await mkdir(codexHome, {recursive: true});
    const initialConfig = [
      "model = \"test-model\"",
      "",
      "[model_providers.custom]",
      "name = \"custom\"",
      "",
    ].join("\n");
    await writeFile(configPath, initialConfig);
    const installed = spawnSync("make", ["install"], {
      cwd: repositoryRoot,
      encoding: "utf8",
      env: installEnvironment,
    });

    if (installed.status !== 0) {
      throw new Error(`make install failed (${installed.status}/${installed.signal}, ${installed.error}):
${installed.stdout}
${installed.stderr}`);
    }
    expect(installed.status).toBe(0);
    expect((await stat(routerPath)).mode & 0o111).not.toBe(0);
    await expect(stat(installedPlugin)).rejects.toThrow();
    const installedInstructions = await readFile(instructionsPath, "utf8");
    for (const expected of [
      "Use `functions.hpatch` for local file edits, not `apply_patch`.",
      "The default interpreter is Bash.",
      "accepts exactly one `{.}` placeholder",
      "`hread @shell/<reference>`",
      "Acquire target-bearing context once before editing.",
      "use hgrep first; use `-F` with repeated `-e` literals",
      "`hsymbol refs PATH LINE:HASH SYMBOL [N]`",
      "Use `inspect_file PATH` for bounded metadata and structural outlines.",
    ]) {
      expect(installedInstructions).toContain(expected);
    }
    for (const removed of [
      "Use `apply_patch` for local file edits.",
      "you reach first for `rg` or `rg --files`",
      "escaping text for exec_command calls",
    ]) {
      expect(installedInstructions).not.toContain(removed);
    }
    expect(await readFile(configPath, "utf8")).toBe([
      "model = \"test-model\"",
      "",
      `model_instructions_file = ${JSON.stringify(instructionsPath)}`,
      "",
      "[model_providers.custom]",
      "name = \"custom\"",
      "",
    ].join("\n"));
    const repeatedInstall = spawnSync("make", ["install-instructions"], {
      cwd: repositoryRoot,
      encoding: "utf8",
      env: installEnvironment,
    });
    expect(repeatedInstall.status).toBe(0);
    expect(await readFile(instructionsPath, "utf8")).toBe(installedInstructions);

    const customInstructionsPath = path.join(codexHome, "custom-instructions.md");
    const customized = [
      "# Customized base",
      "",
      "Preserve this prefix.",
      "",
      "## File editing",
      "",
      "Use `functions.hpatch` for local file edits, not `apply_patch`.",
      "Legacy hpatch guidance.",
      "",
      "## File reading, searching, and shell commands",
      "",
      "Legacy private-tool guidance.",
      "",
      "## Customized remainder",
      "",
      "Preserve this suffix.",
      "",
    ].join("\n");
    await writeFile(customInstructionsPath, customized, {mode: 0o600});
    const configured = `model_instructions_file = ${JSON.stringify(customInstructionsPath)}\n`;
    await writeFile(configPath, configured);
    const reinstalled = spawnSync("make", ["install-instructions"], {
      cwd: repositoryRoot,
      encoding: "utf8",
      env: installEnvironment,
    });
    expect(reinstalled.status).toBe(0);
    expect(await readFile(configPath, "utf8")).toBe(configured);
    const patchedCustomInstructions = await readFile(customInstructionsPath, "utf8");
    expect((await stat(customInstructionsPath)).mode & 0o777).toBe(0o600);
    expect(patchedCustomInstructions).toContain("Preserve this prefix.");
    expect(patchedCustomInstructions).toContain("Use `functions.hpatch` for local file edits");
    expect(patchedCustomInstructions).toContain("Acquire target-bearing context once before editing.");
    expect(patchedCustomInstructions).toContain("Preserve this suffix.");
    expect(patchedCustomInstructions).not.toContain("Legacy hpatch guidance.");
    expect(patchedCustomInstructions).not.toContain("Legacy private-tool guidance.");

    const router = spawn(routerPath, ["--mode", "hpatch", "--listen", "127.0.0.1:0"], {
      cwd: repositoryRoot,
      env: {
        ...installEnvironment,
        PATH: `${binaryDirectory}${path.delimiter}${process.env.PATH ?? ""}`,
      },
    });
    const routerExit = new Promise<{code: number | null; signal: NodeJS.Signals | null}>(
      (resolve) => router.once("exit", (code, signal) => resolve({code, signal})),
    );

    try {
      await waitForListening(router);
      expect((await lstat(shellPath)).isSymbolicLink()).toBe(true);
      expect((await lstat(hreadPath)).isSymbolicLink()).toBe(true);
      await writeFile(path.join(installRoot, "rows.txt"), "alpha\nbeta\n", "utf8");
      const read = spawnSync(shellPath, ["bash", "hread rows.txt 1:2"], {
        cwd: installRoot,
        encoding: "utf8",
        env: {
          ...installEnvironment,
          PATH: `${binaryDirectory}${path.delimiter}${process.env.PATH ?? ""}`,
        },
        input: "",
      });
      expect(read.status).toBe(0);
      expect(read.stdout).toBe("1:8ed3 alpha\n2:f44e beta\n");
      expect(read.stderr).toBe("");

      const executed = spawnSync(shellPath, [
        "bash",
        "printf 'frontend:%s' \"$HPATCH_SHELL_E2E\"",
      ], {
        cwd: repositoryRoot,
        encoding: "utf8",
        env: {
          ...installEnvironment,
          HPATCH_SHELL_E2E: "stdin",
          PATH: `${binaryDirectory}${path.delimiter}${process.env.PATH ?? ""}`,
        },
      });
      expect(executed.status).toBe(0);
      expect(executed.stdout).toBe("frontend:stdin");
      expect(executed.stderr).toBe("");

      const piped = spawnSync(shellPath, [
        "bash",
        "IFS= read -r value; printf 'piped:%s' \"$value\"",
      ], {
        cwd: repositoryRoot,
        encoding: "utf8",
        env: {
          ...installEnvironment,
          PATH: `${binaryDirectory}${path.delimiter}${process.env.PATH ?? ""}`,
        },
        input: "program-input\n",
      });
      expect(piped.status).toBe(0);
      expect(piped.stdout).toBe("piped:program-input");
      expect(piped.stderr).toBe("");

      const bunBody = [
        "import {basename} from \"node:path\";",
        "const input = await Bun.stdin.text();",
        "process.stdout.write(`${basename(process.cwd())}|${input.trim()}`);",
      ].join("\n");
      const bunExecuted = spawnSync(shellPath, [process.execPath, bunBody], {
        cwd: repositoryRoot,
        encoding: "utf8",
        env: {
          ...installEnvironment,
          PATH: `${binaryDirectory}${path.delimiter}${process.env.PATH ?? ""}`,
        },
        input: "bun-input\n",
      });
      expect(bunExecuted.status).toBe(0);
      expect(bunExecuted.stdout).toBe(`${path.basename(repositoryRoot)}|bun-input`);
      expect(bunExecuted.stderr).toBe("");

      const nodeBody = [
        "process.stdin.setEncoding(\"utf8\");",
        "let input = \"\";",
        "process.stdin.on(\"data\", (chunk) => { input += chunk; });",
        "process.stdin.on(\"end\", () => process.stdout.write(`node|${input.trim()}`));",
      ].join("\n");
      const nodeExecuted = spawnSync(shellPath, ["node", "--no-warnings", nodeBody], {
        cwd: repositoryRoot,
        encoding: "utf8",
        env: {
          ...installEnvironment,
          PATH: `${binaryDirectory}${path.delimiter}${process.env.PATH ?? ""}`,
        },
        input: "node-input\n",
      });
      expect(nodeExecuted.status).toBe(0);
      expect(nodeExecuted.stdout).toBe("node|node-input");
      expect(nodeExecuted.stderr).toBe("");
    } finally {
      if (router.exitCode === null && router.signalCode === null) {
        router.kill("SIGTERM");
      }
      expect(await routerExit).toEqual({code: 130, signal: null});
    }

    await expect(lstat(shellPath)).rejects.toThrow();
  }, 15000);
});
