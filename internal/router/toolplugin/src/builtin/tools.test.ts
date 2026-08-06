import {afterEach, describe, expect, test} from "bun:test";
import {spawnSync} from "node:child_process";
import {mkdtemp, mkdir, rm, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";

import {formatHashLine} from "./common.ts";
import {createHGrepTool, splitArguments} from "./hgrep.ts";
import {createHReadTool} from "./hread.ts";

const originalCWD = process.cwd();
const originalPath = process.env.PATH;
const temporaryDirectories: string[] = [];

async function temporaryDirectory(prefix: string): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

afterEach(async () => {
  process.chdir(originalCWD);
  if (originalPath === undefined) {
    delete process.env.PATH;
  } else {
    process.env.PATH = originalPath;
  }
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) => rm(directory, {recursive: true, force: true})),
  );
});

describe("hread built-in plugin", () => {
  test("reads whole files, ranges, and ordered batches", async () => {
    const directory = await temporaryDirectory("hread-plugin-");
    process.chdir(directory);
    await writeFile("plain.txt", "alpha\r\nbeta\rgamma\n", "utf8");
    await writeFile("second file.txt", "one\ntwo\nthree", "utf8");

    const tool = createHReadTool("description", "start: TEST");
    const whole = await tool.execute(["plain.txt"]);
    expect(whole).toEqual({
      stdout: [
        formatHashLine(1, "alpha"),
        formatHashLine(2, "beta"),
        formatHashLine(3, "gamma"),
      ].join(""),
      exitCode: 0,
    });

    const batch = await tool.execute([
      "plain.txt 2:9\n\"second file.txt\" 2:3\nmissing.txt",
    ]);
    expect(batch.exitCode).toBe(0);
    expect(batch.stdout).toContain("==> plain.txt 2:9 <==\n");
    expect(batch.stdout).toContain(formatHashLine(2, "beta"));
    expect(batch.stdout).toContain("==> \"second file.txt\" 2:3 <==\n");
    expect(batch.stdout).toContain(formatHashLine(3, "three"));
    expect(batch.stdout).toContain("==> missing.txt <==\nhread: reading missing.txt:");
  });

  test("rejects invalid ranges, non-regular files, and invalid UTF-8", async () => {
    const directory = await temporaryDirectory("hread-plugin-");
    process.chdir(directory);
    await writeFile("short.txt", "one\n", "utf8");
    await writeFile("binary.txt", Uint8Array.from([0xff]));
    await mkdir("folder");
    if (process.platform !== "win32") {
      const created = spawnSync("mkfifo", ["pipe"]);
      expect(created.status).toBe(0);
    }

    const tool = createHReadTool("description", "start: TEST");
    for (const [input, diagnostic] of [
      ["short.txt 2:3", "outside file with 1 lines"],
      ["short.txt 3:2", "range start exceeds end"],
      ["binary.txt", "is not UTF-8"],
      ["folder", "is not a regular file"],
      ...(process.platform === "win32" ? [] : [["pipe", "is not a regular file"]]),
    ]) {
      const result = await tool.execute([input]);
      expect(result.exitCode).toBe(1);
      expect(result.stderr).toContain(diagnostic);
    }
  });
});

describe("hgrep built-in plugin", () => {
  test("splits literal arguments without shell evaluation", () => {
    expect(splitArguments(
      `-F 'two words' "path with spaces.txt" semi;colon dollar$(value) back\\\\slash`,
    )).toEqual([
      "-F",
      "two words",
      "path with spaces.txt",
      "semi;colon",
      "dollar$(value)",
      "back\\slash",
    ]);
    for (const input of ["", "'unterminated", "\"escape\\", "one\nsecond"]) {
      expect(() => splitArguments(input)).toThrow();
    }
  });

  test("runs ripgrep and emits verified complete rows", async () => {
    const directory = await temporaryDirectory("hgrep-plugin-");
    process.chdir(directory);
    await writeFile("path with spaces.txt", "before\nneedle\nafter\n", "utf8");

    const tool = createHGrepTool("description", "start: TEST");
    const result = await tool.execute(["-A1", "-F", "needle", "path with spaces.txt"]);
    expect(result).toEqual({
      stdout: `${JSON.stringify("path with spaces.txt")}:${formatHashLine(2, "needle")}`
        + `${JSON.stringify("path with spaces.txt")}:${formatHashLine(3, "after")}`,
      stderr: "",
      exitCode: 0,
    });

    await writeFile("carriage.txt", "needle\r", "utf8");
    const carriage = await tool.execute(["-F", "needle", "carriage.txt"]);
    expect(carriage).toEqual({
      stdout: `${JSON.stringify("carriage.txt")}:${formatHashLine(1, "needle")}`,
      stderr: "",
      exitCode: 0,
    });
  });

  test("stops immediately on an oversized unterminated rg event", async () => {
    const directory = await temporaryDirectory("hgrep-plugin-");
    const fakeRG = path.join(directory, "rg");
    await writeFile(
      fakeRG,
      "#!/usr/bin/env bun\n"
        + "process.stdout.write(\"x\".repeat(17 * 1024 * 1024));\n"
        + "setInterval(() => {}, 1000);\n",
      {mode: 0o700},
    );
    process.env.PATH = `${directory}${path.delimiter}${originalPath ?? ""}`;

    const tool = createHGrepTool("description", "start: TEST");
    let timer: ReturnType<typeof setTimeout>;
    const timeout = new Promise<never>((_resolve, reject) => {
      timer = setTimeout(() => reject(new Error("hgrep did not stop after its event limit")), 3000);
    });
    try {
      const result = await Promise.race([tool.execute(["needle", "."]), timeout]);
      expect(result).toEqual({
        stdout: "hgrep: output limit reached; retry with a narrower search\n",
        stderr: "",
        exitCode: 0,
      });
    } finally {
      clearTimeout(timer!);
    }
  });

  test("normalizes presentation options and rejects incompatible modes", async () => {
    const directory = await temporaryDirectory("hgrep-plugin-");
    process.chdir(directory);
    await writeFile("file.txt", "needle\n", "utf8");

    const tool = createHGrepTool("description", "start: TEST");
    const ignored = await tool.execute(["--color", "always", "-F", "needle", "file.txt"]);
    expect(ignored.exitCode).toBe(0);
    expect(ignored.stderr).toContain("ignoring ripgrep options --color");

    const rejected = await tool.execute(["--multiline", "needle", "file.txt"]);
    expect(rejected.exitCode).toBe(1);
    expect(rejected.stderr).toContain("--multiline is incompatible");
  });
});
