import {afterEach, describe, expect, test} from "bun:test";
import {spawn, spawnSync, type ChildProcessWithoutNullStreams} from "node:child_process";
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

  test("rejects malformed or unsafe input before execution", async () => {
    for (const input of [
      "#!",
      "#!   ",
      "#!/usr/bin/env",
      "#!/usr/bin/env -S",
      "#!/usr/bin/env -u python3",
      "printf '\\0'\0",
    ]) {
      expect(() => tool.parse(input)).toThrow();
    }
    expect(() => tool.parse("x".repeat(64 * 1024 + 1))).toThrow(
      "script must not exceed 65536 UTF-8 bytes",
    );
  });

  test("sends the exact body through stdin and inherits cwd and environment", async () => {
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
    const result = await tool.execute(await tool.argv(parsed));

    expect(result).toEqual({
      stdout: `${workingDirectory}|inherited`,
      stderr: "",
      exitCode: 7,
    });
    expect(await readdir(temporaryRoot)).toEqual([]);
  });

  test("reports an unavailable interpreter", async () => {
    const parsed = await tool.parse("#!hpatch-missing-interpreter\nignored");
    const result = await tool.execute(await tool.argv(parsed));

    expect(result.exitCode).toBe(127);
    expect(result.stderr).toContain("not found");
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
    } finally {
      if (router.exitCode === null && router.signalCode === null) {
        router.kill("SIGTERM");
      }
      expect(await routerExit).toEqual({code: 130, signal: null});
    }

    await expect(lstat(shellPath)).rejects.toThrow();
  }, 15000);
});
