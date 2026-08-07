import {afterEach, describe, expect, test} from "bun:test";
import {spawn, spawnSync, type ChildProcessWithoutNullStreams} from "node:child_process";
import {closeSync, openSync} from "node:fs";
import {lstat, mkdtemp, mkdir, readFile, readdir, rm, stat} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import {fileURLToPath} from "node:url";

import plugin from "../../../../plugins/shell.mjs";

const originalCWD = process.cwd();
const originalTMPDIR = process.env.TMPDIR;
const originalTestValue = process.env.HPATCH_SHELL_TEST;
const temporaryDirectories: string[] = [];
const tool = plugin.tools[0];
const hostPath = fileURLToPath(new URL("../host.mjs", import.meta.url));
const repositoryRoot = fileURLToPath(new URL("../../../../", import.meta.url));

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
      `#!python3\n!params ${JSON.stringify(params)}\n#!cmd=${template}\nprint('ok')`,
      `#!python3\n#!cmd=${template}\n!params ${JSON.stringify(params)}\nprint('ok')`,
    ]) {
      const parsed = await tool.parse(input);
      expect(await tool.argv(parsed)).toEqual(["python3", "print('ok')"]);
      expect(await tool.translate(parsed, {exec})).toEqual({kind: "exec", template, params});
    }

    const laterDirective = await tool.parse("printf 'body'\n!params {\"tty\":true}");
    expect(await tool.argv(laterDirective)).toEqual(["bash", "printf 'body'\n!params {\"tty\":true}"]);
    expect(await tool.translate(laterDirective, {exec})).toEqual({kind: "exec"});
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
      "#!cmd=printf ok",
      "#!cmd={.} && {.}",
      "!params",
      "!params []",
      "!params {bad}",
      "!params {\"cmd\":\"forbidden\"}",
      "!params {\"login\":true}",
      "!params {\"login\":null}",
      "!params {\"login\":1}",
      "!params {\"tty\":true}\n!params {\"workdir\":\"/tmp\"}\nprintf ok",
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
        id: "example.shell",
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

  test("make install provides a working shell frontend", async () => {
    const installRoot = await temporaryDirectory("shell-plugin-install-");
    const binaryDirectory = path.join(installRoot, "bin");
    const configDirectory = path.join(installRoot, "config");
    const routerPath = path.join(binaryDirectory, "hpatch-router");
    const shellPath = path.join(binaryDirectory, "shell");
    const installedPlugin = path.join(configDirectory, "hpatch", "plugins", "shell.mjs");
    const installEnvironment = {
      ...process.env,
      GOBIN: binaryDirectory,
      GOTELEMETRY: "off",
      XDG_CONFIG_HOME: configDirectory,
    };
    const installed = spawnSync("make", ["install"], {
      cwd: repositoryRoot,
      encoding: "utf8",
      env: installEnvironment,
    });

    expect(installed.status).toBe(0);
    expect((await stat(path.join(binaryDirectory, "hpatch"))).mode & 0o111).not.toBe(0);
    expect((await stat(routerPath)).mode & 0o111).not.toBe(0);
    expect(await readFile(installedPlugin, "utf8")).toBe(
      await readFile(path.join(repositoryRoot, "plugins", "shell.mjs"), "utf8"),
    );

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
