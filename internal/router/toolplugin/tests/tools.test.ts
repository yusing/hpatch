import {afterEach, describe, expect, test} from "bun:test";
import {spawnSync} from "node:child_process";
import {mkdtemp, mkdir, rm, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";

import {formatHashLine} from "../src/builtin/common.ts";
import {createHGrepTool, splitArguments} from "../src/builtin/hgrep.ts";
import {createHReadTool} from "../src/builtin/hread.ts";
import plugin from "../src/builtin/tools.ts";

const originalCWD = process.cwd();
const originalPath = process.env.PATH;
const temporaryDirectories: string[] = [];
const executionContext = {stdinFD: null, scriptReadFD: null, scriptWriteFD: null, outputBudgetBytes: 16 * 1024 * 1024};

async function temporaryDirectory(prefix: string): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

function rustRegexMatches(pattern: string, input: string): boolean {
  const result = spawnSync(
    "rg",
    ["--no-config", "--multiline", "--quiet", "--", pattern, "-"],
    {input, encoding: "utf8"},
  );
  if (result.error !== undefined) {
    throw result.error;
  }
  if (result.status !== 0 && result.status !== 1) {
    throw new Error(`rg regex evaluation failed: ${result.stderr}`);
  }
  return result.status === 0;
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
  test("describes single-file reads batched through shell", () => {
    const description = plugin.tools[0].specification.description.replace(/\s+/g, " ");
    for (const fragment of [
      "Run one file per command as `hread PATH [START:END]`",
      "batch related reads as separate commands in one shell script",
      "A bare path reads the complete file",
    ]) {
      expect(description).toContain(fragment);
    }
  });

  test("declares a single-file regex grammar", () => {
    const format = plugin.tools[0].specification.format;
    expect(format?.syntax).toBe("regex");
    if (format === undefined) {
      throw new Error("hread grammar format is missing");
    }
    for (const input of [
      "plain.txt",
      "plain.txt 2:9",
      "\"second file.txt\" 2:3",
      `"quoted\\"file.txt"`,
    ]) {
      expect(rustRegexMatches(format.definition, input)).toBe(true);
    }
    for (const input of [
      "",
      "\nplain.txt",
      "plain.txt\n",
      "plain.txt\nsecond.txt",
      "plain file.txt",
      "plain.txt 0:2",
      "plain.txt 2:0",
      "plain.txt 2:3 extra",
      "\"unterminated",
    ]) {
      expect(rustRegexMatches(format.definition, input)).toBe(false);
    }
  });

  test("parses one path and optional range into shell arguments", async () => {
    const tool = createHReadTool("description", "start: TEST");

    expect(await tool.parse("plugins/shell.mjs 164:300")).toEqual([
      "plugins/shell.mjs",
      "164:300",
    ]);
    expect(await tool.parse("\"path with spaces.txt\" 2:9")).toEqual([
      "path with spaces.txt",
      "2:9",
    ]);
    expect(() => tool.parse("first.txt\nsecond.txt 2:9")).toThrow(
      "invalid bare hread path",
    );
  });

  test("supplies cat and cat-plus-sed stock exec commands", async () => {
    const tool = createHReadTool("description", "start: TEST");
    const translate = async (input: string) => tool.translate(await tool.parse(input), {
      exec: (_template, _params, stockCommand) => ({kind: "exec", stockCommand}),
    });

    expect((await translate("plain.txt")).stockCommand).toBe("cat plain.txt");
    expect((await translate("plain.txt 2:9")).stockCommand).toBe("cat plain.txt | sed -n '2,9p'");
    expect((await translate("\"path with spaces.txt\" 2:9")).stockCommand).toBe(
      "cat 'path with spaces.txt' | sed -n '2,9p'",
    );
  });

  test("reads one whole file or range", async () => {
    const directory = await temporaryDirectory("hread-plugin-");
    process.chdir(directory);
    await writeFile("plain.txt", "alpha\r\nbeta\rgamma\n", "utf8");
    await writeFile("second file.txt", "one\ntwo\nthree", "utf8");

    const tool = createHReadTool("description", "start: TEST");
    const whole = await tool.execute(["plain.txt"], executionContext);
    expect(whole).toEqual({
      stdout: [
        formatHashLine(1, "alpha"),
        formatHashLine(2, "beta"),
        formatHashLine(3, "gamma"),
      ].join(""),
      stock: {
        stdout: "alpha\nbeta\ngamma\n",
        exitCode: 0,
      },
      exitCode: 0,
    });

    const range = await tool.execute(["plain.txt", "2:3"], executionContext);
    expect(range).toEqual({
      stdout: [
        formatHashLine(2, "beta"),
        formatHashLine(3, "gamma"),
      ].join(""),
      stock: {
        stdout: "beta\ngamma\n",
        exitCode: 0,
      },
      exitCode: 0,
    });

    const missing = await tool.execute(["missing.txt"], executionContext);
    expect(missing).toEqual({
      stderr: "hread: ENOENT: no such file or directory\n",
      exitCode: 1,
    });
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
    for (const [argv, diagnostic] of [
      [["short.txt", "2:3"], "outside file with 1 lines"],
      [["short.txt", "3:2"], "range start exceeds end"],
      [["binary.txt"], "not UTF-8"],
      [["folder"], "not a regular file"],
      ...(process.platform === "win32" ? [] : [[["pipe"], "not a regular file"]]),
    ] as const) {
      const result = await tool.execute([...argv], executionContext);
      expect(result.exitCode).toBe(1);
      expect(result.stderr).toContain(diagnostic);
    }
  });
});

describe("hgrep built-in plugin", () => {
  test("describes shell searches and repeated patterns", () => {
    const description = plugin.tools[1].specification.description.replace(/\s+/g, " ");
    for (const fragment of [
      "Use `hgrep` through `shell`",
      "ordinary shell quoting, redirection, and pipelines",
      "use repeated `-e` for multiple patterns",
    ]) {
      expect(description).toContain(fragment);
    }
  });

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

  test("declares a strict Rust regex and accepts one transport newline", async () => {
    const format = plugin.tools[1].specification.format;
    expect(format?.syntax).toBe("regex");
    if (format === undefined) {
      throw new Error("hgrep grammar format is missing");
    }
    const tool = createHGrepTool("description", format.definition);
    const accepted = [
      "needle",
      "  -F 'two words' \"path with spaces.txt\"\t",
      "empty''argument",
      "escaped\\ space",
      "\"\"",
    ];
    for (const input of accepted) {
      expect(rustRegexMatches(format.definition, input)).toBe(true);
      const parsed = await tool.parse(input);
      expect(await tool.argv(parsed)).toEqual(splitArguments(input));
    }
    for (const input of ["needle\n", "needle\r\n"]) {
      expect(rustRegexMatches(format.definition, input)).toBe(false);
      const parsed = await tool.parse(input);
      expect(await tool.argv(parsed)).toEqual(["needle"]);
    }
    for (const input of [
      "",
      " \t",
      "\nneedle",
      "needle\n\n",
      "needle\r",
      "one\r\ntwo",
      "'unterminated",
      "\"escape\\",
      "'one\nsecond'",
    ]) {
      expect(rustRegexMatches(format.definition, input)).toBe(false);
      expect(() => tool.parse(input)).toThrow();
    }
  });

  test("supplies an rg stock exec command", async () => {
    const tool = createHGrepTool("description", "start: TEST");
    const parsed = await tool.parse("-F 'two words' \"path with spaces.txt\" semi;colon");
    const translated = await tool.translate(parsed, {
      exec: (_template, _params, stockCommand) => ({kind: "exec", stockCommand}),
    });
    expect(translated.stockCommand).toBe("rg -F 'two words' 'path with spaces.txt' 'semi;colon'");
  });

  test("runs ripgrep and emits verified complete rows", async () => {
    const directory = await temporaryDirectory("hgrep-plugin-");
    process.chdir(directory);
    await writeFile("path with spaces.txt", "before\nneedle\nafter\n", "utf8");

    const tool = createHGrepTool("description", "start: TEST");
    const result = await tool.execute(["-A1", "-F", "needle", "path with spaces.txt"], executionContext);
    expect(result).toEqual({
      stdout: `${JSON.stringify("path with spaces.txt")}:${formatHashLine(2, "needle")}`
        + `${JSON.stringify("path with spaces.txt")}:${formatHashLine(3, "after")}`,
      stock: {
        stdout: "\"path with spaces.txt\":needle\n\"path with spaces.txt\":after\n",
        stderr: "",
        exitCode: 0,
      },
      stderr: "",
      exitCode: 0,
    });

    await writeFile("carriage.txt", "needle\r", "utf8");
    const carriage = await tool.execute(["-F", "needle", "carriage.txt"], executionContext);
    expect(carriage).toEqual({
      stdout: `${JSON.stringify("carriage.txt")}:${formatHashLine(1, "needle")}`,
      stock: {
        stdout: "\"carriage.txt\":needle\n",
        stderr: "",
        exitCode: 0,
      },
      stderr: "",
      exitCode: 0,
    });

    const missing = await tool.execute(["-F", "needle", "missing.txt"], executionContext);
    expect(missing.exitCode).toBe(1);
    expect(missing.stderr).toMatch(
      /^hgrep: (?!rg:)(?!.*missing\.txt)(?!.*IO error for operation on ).+\n$/u,
    );
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
      const result = await Promise.race([tool.execute(["needle", "."], executionContext), timeout]);
      expect(result).toEqual({
        stdout: "hgrep: output limit reached; retry with a narrower search\n",
        stock: {
          stdout: "hgrep: output limit reached; retry with a narrower search\n",
          stderr: "",
          exitCode: 0,
        },
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
    const ignored = await tool.execute(["--color", "always", "-F", "needle", "file.txt"], executionContext);
    expect(ignored.exitCode).toBe(0);
    expect(ignored.stderr).toContain("ignoring ripgrep options --color");

    const rejected = await tool.execute(["--multiline", "needle", "file.txt"], executionContext);
    expect(rejected.exitCode).toBe(1);
    expect(rejected.stderr).toContain("--multiline is incompatible");
  });
});
