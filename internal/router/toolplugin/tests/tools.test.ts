import {afterEach, describe, expect, test} from "bun:test";
import {spawnSync} from "node:child_process";
import {chmod, mkdtemp, mkdir, readFile, rm, symlink, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import {pathToFileURL} from "node:url";

import {countGPT5Tokens, formatHashLine, hashLine, VerifiedRowOutput} from "../../../../plugins/common.ts";
import {createHGrepTool, splitArguments} from "../../../../plugins/hgrep.ts";
import {createHReadTool} from "../../../../plugins/hread.ts";
import {createHSymbolTool} from "../../../../plugins/hsymbol.ts";
import {runLSPQuery} from "../../../../plugins/lsp.ts";
import {
  createInspectFileTool,
  goDeclarationRange,
  inspectFileDescription,
  LineMap,
} from "../../../../plugins/inspect_file.ts";
import plugin from "../../../../plugins/tools.ts";

const originalCWD = process.cwd();
const originalPath = process.env.PATH;
const originalRuntimeDirectory = process.env.HPATCH_RUNTIME_DIR;
const originalThreadID = process.env.CODEX_THREAD_ID;
const pluginBin = path.resolve(import.meta.dir, "../../../../plugins/node_modules/.bin");
const temporaryDirectories: string[] = [];
const executionContext = {stdinFD: null, scriptReadFD: null, scriptWriteFD: null, outputBudgetBytes: 16 * 1024 * 1024};

async function temporaryDirectory(prefix: string): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

type FakeGopls = {
  callsPath: string;
  respond(stdout: string, stderr?: string, exitCode?: number): Promise<void>;
  mutateBeforeResponse(target: string, source: string): Promise<void>;
};

async function installFakeGopls(): Promise<FakeGopls> {
  const directory = await temporaryDirectory("hsymbol-gopls-");
  const executable = path.join(directory, "gopls");
  const callsPath = path.join(directory, "calls");
  await Promise.all([
    writeFile(callsPath, "", "utf8"),
    writeFile(path.join(directory, "stdout"), "", "utf8"),
    writeFile(path.join(directory, "stderr"), "", "utf8"),
    writeFile(path.join(directory, "exit-code"), "0\n", "utf8"),
    writeFile(path.join(directory, "mutate-path"), "", "utf8"),
    writeFile(path.join(directory, "mutate-source"), "", "utf8"),
    writeFile(executable, `#!/bin/sh
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
printf '%s\\n' "$*" >> "$root/calls"
if [ -s "$root/mutate-path" ]; then
  target=$(cat "$root/mutate-path")
  cat "$root/mutate-source" > "$target"
fi
cat "$root/stdout"
cat "$root/stderr" >&2
exit "$(cat "$root/exit-code")"
`, "utf8"),
  ]);
  await chmod(executable, 0o700);
  process.env.PATH = `${directory}${path.delimiter}${originalPath ?? ""}`;
  return {
    callsPath,
    async respond(stdout, stderr = "", exitCode = 0) {
      await Promise.all([
        writeFile(path.join(directory, "stdout"), stdout, "utf8"),
        writeFile(path.join(directory, "stderr"), stderr, "utf8"),
        writeFile(path.join(directory, "exit-code"), `${exitCode}\n`, "utf8"),
      ]);
    },
    async mutateBeforeResponse(target, source) {
      await Promise.all([
        writeFile(path.join(directory, "mutate-path"), target, "utf8"),
        writeFile(path.join(directory, "mutate-source"), source, "utf8"),
      ]);
    },
  };
}

function definitionJSON(filePath: string, source: string, nameOffset: number, name: string): string {
  const lines = new LineMap(source);
  return `${JSON.stringify({
    span: {
      uri: pathToFileURL(filePath).href,
      start: {
        line: lines.lineAt(nameOffset),
        column: 1,
        offset: Buffer.byteLength(source.slice(0, nameOffset), "utf8"),
      },
      end: {
        line: lines.lineAt(nameOffset),
        column: 1 + name.length,
        offset: Buffer.byteLength(source.slice(0, nameOffset + name.length), "utf8"),
      },
    },
    description: name,
  }, null, 2)}\n`;
}

async function inspect(...argv: string[]) {
  const tool = createInspectFileTool("description", String.raw`\A.+\z`);
  const execution = await tool.execute(argv, executionContext);
  return {...execution, raw: execution.stdout ?? "", result: JSON.parse(execution.stdout ?? "")};
}

async function inspectOutline(name: string): Promise<Record<string, unknown>[]> {
  return (await inspect(name)).result.data.outline;
}

const hashedRowPattern = /^(\d+):[0-9a-f]{4}$/u;

function outlineLine(entry: Record<string, unknown>, field = "line"): number {
  const value = String(entry[field]);
  const match = hashedRowPattern.exec(value);
  if (match === null) {
    throw new Error(`invalid ${field} identity ${value}`);
  }
  return Number(match[1]);
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

function contentWithFormattedTokenCount(
  tokens: number,
  format: (content: string) => string,
): string {
  for (let variant = 0; variant < 16; variant += 1) {
    let low = 0;
    let high = tokens + 1;
    const candidate = (repetitions: number) => `row${variant}${" x".repeat(repetitions)}`;
    while (low <= high) {
      const repetitions = Math.floor((low + high) / 2);
      const content = candidate(repetitions);
      const count = countGPT5Tokens(format(content));
      if (count === tokens) {
        return content;
      }
      if (count < tokens) {
        low = repetitions + 1;
      } else {
        high = repetitions - 1;
      }
    }
    for (
      let repetitions = Math.max(0, low - 64);
      repetitions <= Math.min(tokens + 1, low + 64);
      repetitions += 1
    ) {
      const content = candidate(repetitions);
      if (countGPT5Tokens(format(content)) === tokens) {
        return content;
      }
    }
  }
  throw new Error(`cannot construct ${tokens}-token formatted row fixture`);
}

afterEach(async () => {
  process.chdir(originalCWD);
  if (originalPath === undefined) {
    delete process.env.PATH;
  } else {
    process.env.PATH = originalPath;
  }
  if (originalRuntimeDirectory === undefined) {
    delete process.env.HPATCH_RUNTIME_DIR;
  } else {
    process.env.HPATCH_RUNTIME_DIR = originalRuntimeDirectory;
  }
  if (originalThreadID === undefined) {
    delete process.env.CODEX_THREAD_ID;
  } else {
    process.env.CODEX_THREAD_ID = originalThreadID;
  }
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) => rm(directory, {recursive: true, force: true})),
  );
});

describe("verified-row output", () => {
  test("matches the shared Go GPT-5 token fixtures", async () => {
    const fixtures = JSON.parse(await readFile(
      new URL("./testdata/gpt5_tokens.json", import.meta.url),
      "utf8",
    )) as {text: string; tokens: number}[];
    for (const fixture of fixtures) {
      expect(countGPT5Tokens(fixture.text)).toBe(fixture.tokens);
    }
  });

  test("uses GPT-5 tokens with one bounded whole-row overshoot", () => {
    const exact = new VerifiedRowOutput();
    const atSoftLimit = contentWithFormattedTokenCount(15_000, (content) => `${content}\n`);
    expect(exact.append(atSoftLimit)).toBe(true);
    expect(exact.incomplete).toBe(false);

    const overshootContent = contentWithFormattedTokenCount(
      15_500,
      (content) => `${atSoftLimit}${content}\n`,
    );
    const overshootRow = `${overshootContent}\n`;
    expect(countGPT5Tokens(atSoftLimit + overshootRow)).toBe(15_500);
    expect(exact.append(overshootRow)).toBe(true);
    expect(exact.incomplete).toBe(false);
    expect(exact.append("later\n")).toBe(false);
    expect(exact.incomplete).toBe(true);
    expect(exact.current).toBe(atSoftLimit + overshootRow);

    const tooLarge = new VerifiedRowOutput();
    const aboveMaximum = contentWithFormattedTokenCount(15_501, (content) => `${content}\n`);
    expect(tooLarge.append(`${aboveMaximum}\n`)).toBe(false);
    expect(tooLarge.current).toBe("");
    expect(tooLarge.incomplete).toBe(true);
  });
});

describe("hread built-in plugin", () => {
  test("keeps the private description call-local", () => {
    const description = plugin.tools[0].specification.description.replace(/\s+/g, " ");
    expect(description).toContain("Read one UTF-8 file or inclusive logical-line range");
    expect(description).toContain("`LINE:HASH TEXT`");
    expect(description).toContain("`hread PATH [START:END]`");
    for (const persistent of ["authorized edit", "ordinary read", "HPATCH targets", "through `shell`"]) {
      expect(description).not.toContain(persistent);
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
      "plain.txt 0:9",
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
      "plain.txt 2:0",
      "plain.txt 2:3 extra",
      "\"unterminated",
    ]) {
      expect(rustRegexMatches(format.definition, input)).toBe(false);
    }
  });

  test("parses one path and optional range into shell arguments", async () => {
    const tool = createHReadTool("description", "start: TEST");
    const context = {resolvePath: (path: string) => path};
    const parse = (input: string) => tool.parse(input, context);


    expect(await parse("plugins/shell.mjs 164:300")).toEqual([
      "plugins/shell.mjs",
      "164:300",
    ]);
    expect(await parse("plugins/shell.mjs 0:300")).toEqual([
      "plugins/shell.mjs",
      "1:300",
    ]);
    expect(await parse("\"path with spaces.txt\" 2:9")).toEqual([
      "path with spaces.txt",
      "2:9",
    ]);
    expect(() => parse("first.txt\nsecond.txt 2:9")).toThrow(
      "invalid bare hread path",
    );
  });


  test("reads one whole file or range", async () => {
    const directory = await temporaryDirectory("hread-plugin-");
    process.chdir(directory);
    await writeFile("plain.txt", "alpha\r\nbeta\rgamma\n", "utf8");
    await writeFile("second file.txt", "one\ntwo\nthree", "utf8");
    await writeFile("token-spellings.txt", "<|endoftext|> <|im_start|> <|fim_prefix|>\n", "utf8");

    const tool = createHReadTool("description", "start: TEST");
    const whole = await tool.execute(["plain.txt"], executionContext);
    expect(whole).toEqual({
      stdout: [
        formatHashLine(1, "alpha"),
        formatHashLine(2, "beta"),
        formatHashLine(3, "gamma"),
      ].join(""),
      exitCode: 0,
    });

    const range = await tool.execute(["plain.txt", "2:3"], executionContext);
    expect(range).toEqual({
      stdout: [
        formatHashLine(2, "beta"),
        formatHashLine(3, "gamma"),
      ].join(""),
      exitCode: 0,
    });

    const zeroRange = await tool.execute(["plain.txt", "0:3"], executionContext);
    expect(zeroRange).toEqual(whole);

    const overrun = await tool.execute(["plain.txt", "2:5"], executionContext);
    expect(overrun).toEqual({
      stdout: [
        formatHashLine(2, "beta"),
        formatHashLine(3, "gamma"),
      ].join(""),
      stderr: "hread: 4-5: [out of range]\n",
      exitCode: 0,
    });

    const tokenSpellings = await tool.execute(["token-spellings.txt"], executionContext);
    expect(tokenSpellings).toEqual({
      stdout: formatHashLine(1, "<|endoftext|> <|im_start|> <|fim_prefix|>"),
      exitCode: 0,
    });

    const outside = await tool.execute(["plain.txt", "4:5"], executionContext);
    expect(outside).toEqual({
      stderr: "hread: start line 4 is past EOF (3 lines)\n",
      exitCode: 1,
    });

    const missing = await tool.execute(["missing.txt"], executionContext);
    expect(missing).toEqual({
      stderr: "hread: ENOENT: no such file or directory\n",
      exitCode: 1,
    });

    const runtimeDirectory = await temporaryDirectory("hpatch-runtime-");
    const retainedDirectory = path.join(runtimeDirectory, "hpatch-thread-id");
    await mkdir(retainedDirectory);
    await writeFile(path.join(retainedDirectory, "call-id"), "retained\n", "utf8");
    process.env.HPATCH_RUNTIME_DIR = runtimeDirectory;
    process.env.CODEX_THREAD_ID = "thread-id";
    const retained = await tool.execute(["@shell/call-id"], executionContext);
    expect(retained).toEqual({
      stdout: formatHashLine(1, "retained"),
      exitCode: 0,
    });
  });

  test("rejects malformed ranges, non-regular files, and invalid UTF-8", async () => {
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

  test("retains whole admitted rows and fails when later rows exceed the token limit", async () => {
    const directory = await temporaryDirectory("hread-limit-");
    process.chdir(directory);
    const first = contentWithFormattedTokenCount(15_000, (content) => formatHashLine(1, content));
    await writeFile("large.txt", `${first}\nsecond\nthird\n`, "utf8");

    const tool = createHReadTool("description", "start: TEST");
    const result = await tool.execute(["large.txt"], executionContext);
    expect(result).toEqual({
      stdout: formatHashLine(1, first) + formatHashLine(2, "second"),
      stderr: "hread: output incomplete: 15,000-token limit reached\n",
      exitCode: 1,
    });
  });

  test("discards an unavoidably over-limit row while streaming", async () => {
    const directory = await temporaryDirectory("hread-oversized-row-");
    process.chdir(directory);
    await writeFile("large.txt", " ".repeat(15_500 * 128 + 1), "utf8");

    const tool = createHReadTool("description", "start: TEST");
    const result = await tool.execute(["large.txt"], executionContext);
    expect(result).toEqual({
      stdout: "",
      stderr: "hread: output incomplete: 15,000-token limit reached\n",
      exitCode: 1,
    });
  });

});

describe("hgrep built-in plugin", () => {
  test("keeps the private description call-local", () => {
    const description = plugin.tools[1].specification.description.replace(/\s+/g, " ");
    expect(description).toContain("Search files with supported ripgrep arguments");
    expect(description).toContain("`\"PATH\":LINE:HASH TEXT`");
    for (const persistent of ["authorized edit", "ordinary search", "HPATCH targets", "through `shell`"]) {
      expect(description).not.toContain(persistent);
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


  test("runs ripgrep and emits verified complete rows", async () => {
    const directory = await temporaryDirectory("hgrep-plugin-");
    process.chdir(directory);
    await writeFile("path with spaces.txt", "before\nneedle\nafter\n", "utf8");
    await writeFile("token-spellings.txt", "<|endoftext|> <|im_start|> <|fim_prefix|>\n", "utf8");

    const tool = createHGrepTool("description", "start: TEST");
    const result = await tool.execute(["-A1", "-F", "needle", "path with spaces.txt"], executionContext);
    expect(result).toEqual({
      stdout: `${JSON.stringify("path with spaces.txt")}:${formatHashLine(2, "needle")}`
        + `${JSON.stringify("path with spaces.txt")}:${formatHashLine(3, "after")}`,
      stderr: "",
      exitCode: 0,
    });

    await writeFile("carriage.txt", "needle\r", "utf8");
    const carriage = await tool.execute(["-F", "needle", "carriage.txt"], executionContext);
    expect(carriage).toEqual({
      stdout: `${JSON.stringify("carriage.txt")}:${formatHashLine(1, "needle")}`,
      stderr: "",
      exitCode: 0,
    });

    const tokenSpellings = await tool.execute(["-F", "<|im_start|>", "token-spellings.txt"], executionContext);
    expect(tokenSpellings).toEqual({
      stdout: `${JSON.stringify("token-spellings.txt")}:${formatHashLine(
        1,
        "<|endoftext|> <|im_start|> <|fim_prefix|>",
      )}`,
      stderr: "",
      exitCode: 0,
    });

    const missing = await tool.execute(["-F", "needle", "missing.txt"], executionContext);
    expect(missing.exitCode).toBe(1);
    expect(missing.stderr).toMatch(
      /^hgrep: (?!rg:)(?!.*missing\.txt)(?!.*IO error for operation on ).+\n$/u,
    );
  });

  test("retains whole admitted matches and fails when later matches exceed the token limit", async () => {
    const directory = await temporaryDirectory("hgrep-limit-");
    process.chdir(directory);
    const prefix = `${JSON.stringify("large.txt")}:`;
    const first = contentWithFormattedTokenCount(
      15_000,
      (content) => `${prefix}${formatHashLine(1, content)}`,
    );
    await writeFile("large.txt", `${first}\nneedle second\nneedle third\n`, "utf8");

    const tool = createHGrepTool("description", "start: TEST");
    const result = await tool.execute(["-F", "needle", "large.txt"], executionContext);
    expect(result).toEqual({
      stdout: `${prefix}${formatHashLine(2, "needle second")}`
        + `${prefix}${formatHashLine(3, "needle third")}`,
      stderr: "",
      exitCode: 0,
    });

    await writeFile("large.txt", `needle ${first}\nneedle second\nneedle third\n`, "utf8");
    const limited = await tool.execute(["-F", "needle", "large.txt"], executionContext);
    expect(limited.exitCode).toBe(1);
    expect(limited.stderr).toBe("hgrep: output incomplete: 15,000-token limit reached\n");
    expect(limited.stdout).toContain(`needle ${first}`);
    expect(limited.stdout).not.toContain("needle second");
    expect(limited.stdout).not.toContain("needle third");
  });

  test("accepts GNU grep and ripgrep search options while rejecting incompatible modes", async () => {
    const directory = await temporaryDirectory("hgrep-plugin-");
    process.chdir(directory);
    await writeFile("file.txt", "needle\n", "utf8");

    const tool = createHGrepTool("description", "start: TEST");
    const gnuGrep = await tool.execute(["-R", "-n", "-F", "needle", "file.txt"], executionContext);
    const ripgrep = await tool.execute(["--glob", "*.txt", "-F", "needle", "file.txt"], executionContext);
    expect(gnuGrep).toEqual(ripgrep);
    expect(gnuGrep.stderr).toBe("");

    const ignored = await tool.execute(["--color", "always", "-F", "needle", "file.txt"], executionContext);
    expect(ignored.exitCode).toBe(0);
    expect(ignored.stderr).toContain("ignoring ripgrep options --color");

    const rejected = await tool.execute(["--multiline", "needle", "file.txt"], executionContext);
    expect(rejected.exitCode).toBe(1);
    expect(rejected.stderr).toContain("--multiline is incompatible");
  });
});

describe("hsymbol built-in plugin", () => {
  test("keeps its private contract behavioral", () => {
    const description = plugin.tools[2].specification.description.replace(/\s+/g, " ");
    expect(description).toContain("hsymbol (def|refs) PATH LINE:HASH SYMBOL [N]");
    expect(description).toContain('"PATH":LINE:HASH TEXT');
    expect(description).toContain("ambiguous selectors");
    for (const persistent of ["rename", "audit", "before editing", "functions.hpatch"]) {
      expect(description).not.toContain(persistent);
    }
  });

  test("validates the verified Go token selector before starting gopls", async () => {
    const directory = await temporaryDirectory("hsymbol-plugin-");
    process.chdir(directory);
    const source = [
      "package sample",
      "func Use() {",
      '  名稱 := 1; _ = 名稱; _ = "名稱" // 名稱',
      "}",
      "",
    ].join("\n");
    await writeFile("path with spaces.go", source, "utf8");
    const fake = await installFakeGopls();
    const tool = createHSymbolTool("description", "start: TEST");
    const line = new LineMap(source).logicalLine(3)?.text;
    if (line === undefined) {
      throw new Error("selector fixture line is missing");
    }
    const reference = `3:${hashLine(line)}`;

    for (const argv of [
      ["refs", "path with spaces.go", "3:ffff", "名稱"],
      ["refs", "path with spaces.go", reference, "名稱"],
      ["refs", "path with spaces.go", reference, "名稱", "3"],
      ["refs", "path with spaces.go", reference, "名稱", "01"],
      ["refs", "path with spaces.go", reference, "func"],
      ["refs", "path with spaces.go", reference, "Name"],
    ]) {
      const result = await tool.execute(argv, executionContext);
      expect(result.exitCode).toBe(1);
      expect(result.stdout).toBeUndefined();
    }
    expect(await readFile(fake.callsPath, "utf8")).toBe("");

    const selected = await tool.execute(
      ["refs", "path with spaces.go", reference, "名稱", "2"],
      executionContext,
    );
    expect(selected).toEqual({
      stdout: "",
      exitCode: 0,
    });
    const selectedOffset = source.indexOf("名稱", source.indexOf("名稱") + 1);
    const calls = await readFile(fake.callsPath, "utf8");
    expect(calls).toContain("references -d");
    expect(calls).toContain(`:#${Buffer.byteLength(source.slice(0, selectedOffset), "utf8")}`);
    expect(calls.trim().split("\n")).toHaveLength(1);
  });

  test("rejects JavaScript labels before starting TypeScript", async () => {
    const directory = await temporaryDirectory("hsymbol-label-");
    process.chdir(directory);
    const source = "target: while (false) break target;\n";
    await writeFile("input.js", source);
    process.env.PATH = await temporaryDirectory("hsymbol-label-empty-path-");
    const result = await createHSymbolTool("description", "start: TEST").execute(
      ["refs", "input.js", `1:${hashLine(source.trimEnd())}`, "target"],
      executionContext,
    );
    expect(result).toEqual({
      stderr: "hsymbol: target is not a symbol token on the verified line\n",
      exitCode: 1,
    });
  });

  test("accepts canonical in-workspace paths and rejects workspace escapes before gopls", async () => {
    const directory = await temporaryDirectory("hsymbol-path-");
    process.chdir(directory);
    const source = "package sample\nfunc Use() { Target() }\n";
    const inputPath = path.join(directory, "input.go");
    await writeFile(inputPath, source, "utf8");
    const inputLine = new LineMap(source).logicalLine(2)?.text;
    if (inputLine === undefined) {
      throw new Error("path input line is missing");
    }
    const reference = `2:${hashLine(inputLine)}`;
    const fake = await installFakeGopls();
    const tool = createHSymbolTool("description", "start: TEST");
    expect(await tool.execute(["refs", inputPath, reference, "Target"], executionContext)).toEqual({
      stdout: "",
      exitCode: 0,
    });

    const externalDirectory = await temporaryDirectory("hsymbol-external-");
    const externalPath = path.join(externalDirectory, "external.go");
    await writeFile(externalPath, source, "utf8");
    await symlink(externalPath, "escaped.go");
    for (const escapedPath of [externalPath, "escaped.go"]) {
      const result = await tool.execute(["refs", escapedPath, reference, "Target"], executionContext);
      expect(result.exitCode).toBe(1);
      expect(result.stderr).toContain("outside the workspace");
      expect(result.stdout).toBeUndefined();
    }
    expect((await readFile(fake.callsPath, "utf8")).trim().split("\n")).toHaveLength(1);
  });

  test("expands only exact top-level and direct-method outline declarations", async () => {
    const directory = await temporaryDirectory("hsymbol-def-");
    process.chdir(directory);
    const source = [
      "package sample",
      "const (",
      "  A = 1",
      ")",
      "var B = struct {",
      "  X int",
      "}{}",
      "type C struct {",
      "  Y int",
      "}",
      "func D() {",
      "  _ = A",
      "}",
      "type R struct{}",
      "func (R) M() {",
      "  D()",
      "}",
      "func Use(r R) { _ = A; _ = B; _ = C{}; D(); r.M() }",
      "",
    ].join("\n");
    const filePath = path.join(directory, "declarations.go");
    await writeFile(filePath, source, "utf8");
    const fake = await installFakeGopls();
    const tool = createHSymbolTool("description", "start: TEST");
    const useLine = new LineMap(source).logicalLine(18)?.text;
    if (useLine === undefined) {
      throw new Error("definition fixture line is missing");
    }
    const reference = `18:${hashLine(useLine)}`;
    const cases = [
      {name: "A", from: 3, to: 3},
      {name: "B", from: 5, to: 7},
      {name: "C", from: 8, to: 10},
      {name: "D", from: 11, to: 13},
      {name: "M", from: 15, to: 17},
    ];
    const lines = new LineMap(source);

    for (const testCase of cases) {
      const nameOffset = source.indexOf(testCase.name, lines.logicalLine(testCase.from)?.from);
      const response = definitionJSON(filePath, source, nameOffset, testCase.name);
      await fake.respond(response);
      const result = await tool.execute(
        ["def", "declarations.go", reference, testCase.name],
        executionContext,
      );
      let expected = "";
      for (let lineNumber = testCase.from; lineNumber <= testCase.to; lineNumber += 1) {
        const text = lines.logicalLine(lineNumber)?.text;
        if (text === undefined) {
          throw new Error(`missing fixture line ${lineNumber}`);
        }
        expected += `${JSON.stringify("declarations.go")}:${formatHashLine(lineNumber, text)}`;
      }
      expect(result).toEqual({
        stdout: expected,
        exitCode: 0,
      });
    }
  });

  test("falls back to the definition line for unowned or uncertain declarations", async () => {
    const directory = await temporaryDirectory("hsymbol-def-");
    process.chdir(directory);
    const source = [
      "package sample",
      "type R struct {",
      "  Field int",
      "}",
      "func Use(r R) { _ = r.Field }",
      "",
    ].join("\n");
    const filePath = path.join(directory, "field.go");
    await writeFile(filePath, source, "utf8");
    const fieldOffset = source.indexOf("Field");
    expect(goDeclarationRange(
      source,
      Buffer.byteLength(source.slice(0, fieldOffset), "utf8"),
      Buffer.byteLength(source.slice(0, fieldOffset + "Field".length), "utf8"),
    )).toBeNull();
    const broken = source + "func Broken( {\n";
    const useOffset = broken.indexOf("Use");
    expect(goDeclarationRange(
      broken,
      Buffer.byteLength(broken.slice(0, useOffset), "utf8"),
      Buffer.byteLength(broken.slice(0, useOffset + "Use".length), "utf8"),
    )).toBeNull();

    const fake = await installFakeGopls();
    const response = definitionJSON(filePath, source, fieldOffset, "Field");
    await fake.respond(response);
    const useLine = new LineMap(source).logicalLine(5)?.text;
    if (useLine === undefined) {
      throw new Error("field use line is missing");
    }
    const result = await createHSymbolTool("description", "start: TEST").execute(
      ["def", "field.go", `5:${hashLine(useLine)}`, "Field"],
      executionContext,
    );
    expect(result).toEqual({
      stdout: `${JSON.stringify("field.go")}:${formatHashLine(3, "  Field int")}`,
      exitCode: 0,
    });
  });

  test("deduplicates canonical reference rows and reports skipped locations", async () => {
    const directory = await temporaryDirectory("hsymbol-refs-");
    process.chdir(directory);
    const inputSource = "package sample\nfunc Use() { Target() }\n";
    const resultSource = "package sample\nfunc Target() {}\n";
    await Promise.all([
      writeFile("input.go", inputSource, "utf8"),
      writeFile("result.go", resultSource, "utf8"),
      writeFile("not-go.txt", "Target\n", "utf8"),
      writeFile("invalid.go", Uint8Array.from([0xff])),
      mkdir("folder.go"),
    ]);
    await symlink("result.go", "alias.go");
    const externalDirectory = await temporaryDirectory("hsymbol-external-");
    const externalPath = path.join(externalDirectory, "external.go");
    await writeFile(externalPath, "package external\nfunc Target() {}\n", "utf8");
    const resultPath = path.join(directory, "result.go");
    const inputPath = path.join(directory, "input.go");
    const rows = [
      `${resultPath}:2:6-12`,
      `${path.join(directory, "alias.go")}:2:6-12`,
      `${inputPath}:2:14-20`,
      `${externalPath}:2:6-12`,
      `${path.join(directory, "not-go.txt")}:1:1-7`,
      `${path.join(directory, "folder.go")}:1:1-7`,
      `${path.join(directory, "invalid.go")}:1:1-7`,
      `${path.join(directory, "missing.go")}:1:1-7`,
    ];
    const goplsStdout = `${rows.join("\n")}\n`;
    const fake = await installFakeGopls();
    await fake.respond(goplsStdout, "gopls note\n");
    const inputLine = new LineMap(inputSource).logicalLine(2)?.text;
    if (inputLine === undefined) {
      throw new Error("reference input line is missing");
    }
    const result = await createHSymbolTool("description", "start: TEST").execute(
      ["refs", "input.go", `2:${hashLine(inputLine)}`, "Target"],
      executionContext,
    );
    expect(result).toEqual({
      stdout: `${JSON.stringify("result.go")}:${formatHashLine(2, "func Target() {}")}`
        + `${JSON.stringify("input.go")}:${formatHashLine(2, "func Use() { Target() }")}`,
      stderr: "gopls note\nhsymbol: skipped 1 location outside workspace, 1 location not Go, 1 location not regular, 1 location not UTF-8, 1 location unavailable\n",
      exitCode: 0,
    });
    expect(await readFile(fake.callsPath, "utf8")).toContain("references -d");
  });

  test("fails an uneditable definition and a missing gopls without useful stdout", async () => {
    const directory = await temporaryDirectory("hsymbol-failure-");
    process.chdir(directory);
    const source = "package sample\nfunc Use() { Target() }\n";
    await writeFile("input.go", source, "utf8");
    const inputLine = new LineMap(source).logicalLine(2)?.text;
    if (inputLine === undefined) {
      throw new Error("failure input line is missing");
    }
    const reference = `2:${hashLine(inputLine)}`;
    const externalDirectory = await temporaryDirectory("hsymbol-external-");
    const externalSource = "package external\nfunc Target() {}\n";
    const externalPath = path.join(externalDirectory, "external.go");
    await writeFile(externalPath, externalSource, "utf8");
    const response = definitionJSON(externalPath, externalSource, externalSource.indexOf("Target"), "Target");
    const fake = await installFakeGopls();
    await fake.respond(response);
    const tool = createHSymbolTool("description", "start: TEST");
    const external = await tool.execute(["def", "input.go", reference, "Target"], executionContext);
    expect(external).toEqual({
      stderr: "hsymbol: skipped 1 location outside workspace\nhsymbol: definition has no editable workspace location\n",
      exitCode: 1,
    });

    await fake.respond("", "query failed\n", 2);
    const failed = await tool.execute(["refs", "input.go", reference, "Target"], executionContext);
    expect(failed).toEqual({stderr: "hsymbol: query failed\n", exitCode: 1});

    const emptyPath = await temporaryDirectory("hsymbol-empty-path-");
    process.env.PATH = emptyPath;
    const unavailable = await tool.execute(["refs", "input.go", reference, "Target"], executionContext);
    expect(unavailable).toEqual({stderr: "hsymbol: gopls is unavailable\n", exitCode: 1});
  });

  test("fails without query output when the selected input changes during gopls", async () => {
    const directory = await temporaryDirectory("hsymbol-changing-input-");
    process.chdir(directory);
    const source = "package sample\nfunc Use() { Target() }\n";
    const inputPath = path.join(directory, "input.go");
    await writeFile(inputPath, source, "utf8");
    const inputLine = new LineMap(source).logicalLine(2)?.text;
    if (inputLine === undefined) {
      throw new Error("changing input line is missing");
    }
    const fake = await installFakeGopls();
    await fake.respond(`${inputPath}:2:14-20\n`);
    await fake.mutateBeforeResponse(inputPath, `package sample\n\n${inputLine}\n`);

    const result = await createHSymbolTool("description", "start: TEST").execute(
      ["refs", "input.go", `2:${hashLine(inputLine)}`, "Target"],
      executionContext,
    );
    expect(result).toEqual({stderr: "hsymbol: input changed during query\n", exitCode: 1});
  });

  test("applies the shared whole-row token admission to references", async () => {
    const directory = await temporaryDirectory("hsymbol-limit-");
    process.chdir(directory);
    const inputSource = "package sample\nfunc Use() { Target() }\n";
    await writeFile("input.go", inputSource, "utf8");
    const resultPath = path.join(directory, "large.go");
    const prefix = `${JSON.stringify("large.go")}:`;
    const first = contentWithFormattedTokenCount(
      15_000,
      (content) => `${prefix}${formatHashLine(1, content)}`,
    );
    await writeFile(resultPath, `${first}\nsecond\nthird\n`, "utf8");
    const externalDirectory = await temporaryDirectory("hsymbol-limit-external-");
    const externalPath = path.join(externalDirectory, "external.go");
    await writeFile(externalPath, "package external\n", "utf8");
    const goplsStdout = [
      ...[1, 2, 3].map((line) => `${resultPath}:${line}:1-2`),
      `${externalPath}:1:1-2`,
    ].join("\n") + "\n";
    const fake = await installFakeGopls();
    await fake.respond(goplsStdout);
    const inputLine = new LineMap(inputSource).logicalLine(2)?.text;
    if (inputLine === undefined) {
      throw new Error("limit input line is missing");
    }
    const result = await createHSymbolTool("description", "start: TEST").execute(
      ["refs", "input.go", `2:${hashLine(inputLine)}`, "Target"],
      executionContext,
    );
    expect(result).toEqual({
      stdout: `${prefix}${formatHashLine(1, first)}${prefix}${formatHashLine(2, "second")}`,
      stderr: "hsymbol: skipped 1 location outside workspace\n"
        + "hsymbol: output incomplete: 15,000-token limit reached\n",
      exitCode: 1,
    });
  });

  test("resolves TypeScript 7 and Python definitions through their LSP servers", async () => {
    const directory = await temporaryDirectory("hsymbol-lsp-");
    process.chdir(directory);
    process.env.PATH = `${pluginBin}${path.delimiter}${originalPath ?? ""}`;
    // TypeScript supports a version probe; Pyright is validated by the LSP query below.
    const tscCheck = spawnSync("tsc", ["--version"], {encoding: "utf8"});
    if (tscCheck.status !== 0) {
      throw new Error(`tsc is not available: ${tscCheck.error?.message ?? tscCheck.stderr}`);
    }
    const typescriptTarget = [
      "export function target(value: number) {",
      "  return value + 1;",
      "}",
      "",
    ].join("\n");
    const typescriptInput = [
      'import {target} from "./target";',
      'export const emoji = "😀"; export const answer = target(1);',
      "",
    ].join("\n");
    const pythonTarget = [
      "def target(value: int) -> int:",
      "    return value + 1",
      "",
    ].join("\n");
    const pythonInput = [
      "from target import target",
      "answer = target(1)",
      "",
    ].join("\n");
    const pythonStub = "def stub_target(value: int) -> int: ...\n";
    const ambientTarget = [
      "export declare function ambientTarget(",
      "  value: number,",
      "): number;",
      "",
    ].join("\n");
    const ambientInput = [
      'import {ambientTarget} from "./ambient";',
      "export const ambientAnswer = ambientTarget(1);",
      "",
    ].join("\n");
    await Promise.all([
      writeFile("tsconfig.json", JSON.stringify({compilerOptions: {allowJs: true, jsx: "react-jsx"}})),
      writeFile("target.ts", typescriptTarget),
      writeFile("input.ts", typescriptInput),
      writeFile("target.py", pythonTarget),
      writeFile("input.py", pythonInput),
      writeFile("sample.pyi", pythonStub),
      writeFile("ambient.d.ts", ambientTarget),
      writeFile("ambient_input.ts", ambientInput),
    ]);
    const tool = createHSymbolTool("description", "start: TEST");
    const typescriptLine = new LineMap(typescriptInput).logicalLine(2)?.text;
    const pythonLine = new LineMap(pythonInput).logicalLine(2)?.text;
    if (typescriptLine === undefined || pythonLine === undefined) {
      throw new Error("LSP fixture line is missing");
    }

    const typescript = await tool.execute(
      ["def", "input.ts", `2:${hashLine(typescriptLine)}`, "target"],
      executionContext,
    );
    expect(typescript).toMatchObject({exitCode: 0});
    expect(typescript.stdout).toContain(`${JSON.stringify("target.ts")}:${formatHashLine(1, "export function target(value: number) {")}`);
    expect(typescript.stdout).toContain(`${JSON.stringify("target.ts")}:${formatHashLine(3, "}")}`);

    const ambient = await tool.execute(
      ["def", "ambient_input.ts", `2:${hashLine(ambientInput.split("\n")[1])}`, "ambientTarget"],
      executionContext,
    );
    expect(ambient).toMatchObject({exitCode: 0});
    for (const [line, text] of ambientTarget.trimEnd().split("\n").entries()) {
      expect(ambient.stdout).toContain(`${JSON.stringify("ambient.d.ts")}:${formatHashLine(line + 1, text)}`);
    }

    const python = await tool.execute(
      ["def", "input.py", `2:${hashLine(pythonLine)}`, "target"],
      executionContext,
    );
    expect(python).toMatchObject({exitCode: 0});
    expect(python.stdout).toContain(`${JSON.stringify("target.py")}:${formatHashLine(1, "def target(value: int) -> int:")}`);
    expect(python.stdout).toContain(`${JSON.stringify("target.py")}:${formatHashLine(2, "    return value + 1")}`);
    const stub = await tool.execute(
      ["refs", "sample.pyi", `1:${hashLine(pythonStub.trimEnd())}`, "stub_target"],
      executionContext,
    );
    expect(stub).toMatchObject({exitCode: 0});

    process.env.PATH = await temporaryDirectory("hsymbol-empty-lsp-path-");
    expect(await tool.execute(
      ["refs", "input.ts", `2:${hashLine(typescriptLine)}`, "target"],
      executionContext,
    )).toEqual({stderr: "hsymbol: tsc is unavailable\n", exitCode: 1});
    expect(await tool.execute(
      ["refs", "input.py", `2:${hashLine(pythonLine)}`, "target"],
      executionContext,
    )).toEqual({stderr: "hsymbol: pyright-langserver is unavailable\n", exitCode: 1});
  });

  test("reaps a language server that ignores shutdown", async () => {
    const directory = await temporaryDirectory("hsymbol-lsp-shutdown-");
    const server = path.join(directory, "server.mjs");
    await writeFile(server, String.raw`
let input = Buffer.alloc(0);
function respond(id, result) {
  const body = JSON.stringify({jsonrpc: "2.0", id, result});
  process.stdout.write("Content-Length: " + Buffer.byteLength(body) + "\r\n\r\n" + body);
}
function receive(message) {
  if (message.method === "initialize") {
    respond(message.id, {capabilities: {positionEncoding: "utf-16"}});
  } else if (message.method === "textDocument/definition") {
    respond(message.id, []);
  }
}
process.stdin.on("data", (chunk) => {
  input = Buffer.concat([input, chunk]);
  while (true) {
    const headerEnd = input.indexOf("\r\n\r\n");
    if (headerEnd < 0) break;
    const header = input.subarray(0, headerEnd).toString();
    const length = Number(/Content-Length: ([0-9]+)/iu.exec(header)?.[1]);
    if (input.length < headerEnd + 4 + length) break;
    const bodyStart = headerEnd + 4;
    receive(JSON.parse(input.subarray(bodyStart, bodyStart + length).toString()));
    input = input.subarray(bodyStart + length);
  }
});
`);
    const started = performance.now();
    const result = await runLSPQuery({
      command: process.execPath,
      args: [server],
      workspace: directory,
      path: path.join(directory, "input.ts"),
      languageID: "typescript",
      source: "const value = 1;\n",
      position: {line: 0, character: 6},
      mode: "def",
    });
    expect(result.locations).toEqual([]);
    expect(performance.now() - started).toBeLessThan(3_000);
  });

  test("accepts every stable TypeScript 7 source format", async () => {
    const directory = await temporaryDirectory("hsymbol-typescript-formats-");
    process.chdir(directory);
    process.env.PATH = `${pluginBin}${path.delimiter}${originalPath ?? ""}`;
    await writeFile("tsconfig.json", JSON.stringify({
      compilerOptions: {allowJs: true, checkJs: true, jsx: "react-jsx"},
      include: ["*"],
    }));
    const fixtures = [
      ["sample.ts", "export const target = 1; console.log(target);"],
      ["sample.tsx", "export const target = 1; const view = <div>{target}</div>;"],
      ["sample.d.ts", "export declare const target: number;"],
      ["sample.mts", "export const target = 1; console.log(target);"],
      ["sample.d.mts", "export declare const target: number;"],
      ["sample.cts", "export const target = 1; console.log(target);"],
      ["sample.d.cts", "export declare const target: number;"],
      ["sample.js", "export const target = 1; console.log(target);"],
      ["sample.jsx", "export const target = 1; const view = <div>{target}</div>;"],
      ["sample.mjs", "export const target = 1; console.log(target);"],
      ["sample.cjs", "const target = 1; module.exports = target;"],
      ["sample.json", '{"target": 1}'],
    ] as const;
    await Promise.all(fixtures.map(([name, source]) => writeFile(name, `${source}\n`)));
    const tool = createHSymbolTool("description", "start: TEST");

    for (const [name, source] of fixtures) {
      const result = await tool.execute(
        ["refs", name, `1:${hashLine(source)}`, "target", "1"],
        executionContext,
      );
      expect(result).toMatchObject({exitCode: 0});
      expect(result.stderr).toBeUndefined();
    }
  });
});

describe("inspect_file built-in plugin", () => {

  test("embeds its shape schema and omits source values from structural results", async () => {
    const marker = "Result shape schema:\n";
    const schema = JSON.parse(inspectFileDescription.slice(
      inspectFileDescription.indexOf(marker) + marker.length,
    ));
    expect(schema.success.data.outline).toBe("outline_entry[]");
    expect(schema.failure.error.code).toContain("outside_workspace");
    for (const persistent of ["hread", "before editing", "Reason carefully"]) {
      expect(inspectFileDescription).not.toContain(persistent);
    }

    const directory = await temporaryDirectory("inspect-file-");
    process.chdir(directory);
    await writeFile("sample.go", [
      "package p", "import alias \"example.com/a\"", "import `raw/path`",
      "import rawalias `aliased/raw`", "import (_ `grouped/raw`)",
      "const (A, B = 1, 2)", "var C int", "type T[P any] struct { Secret string }",
      "func F() { local := \"body-secret\" }", "func (receiver *T[P]) M() {}", "",
    ].join("\n"));
    await writeFile("sample.md", [
      "---", "title: do-not-return", "\"quoted key\": hidden-value",
      "summary: |", "  # secret scalar", "meta:", "  nested: excluded", "---",
      "# Main *source* #", "```", "## hidden", "```", "### Visible", "",
    ].join("\r\n"));
    await writeFile("sample.json", "{\"a/b\":{\"~key\":[true,null,123,\"never-return\"]}}");
    await writeFile("recovered.json", "{\"a\" \"x\"}");
    await writeFile("duplicate.md", "---\na: 1\na: 2\n---\n");

    const go = await inspect("sample.go");
    expect(go.result.data.outline.map((entry: Record<string, unknown>) =>
      [entry.kind, entry.name, entry.receiver],
    )).toEqual([
      ["import", "example.com/a", undefined], ["import", "raw/path", undefined],
      ["import", "aliased/raw", undefined], ["import", "grouped/raw", undefined],
      ["constant", "A", undefined], ["constant", "B", undefined],
      ["variable", "C", undefined], ["type", "T", undefined],
      ["function", "F", undefined], ["method", "M", "*T[P]"],
    ]);
    expect(JSON.stringify(go.result)).not.toMatch(/Secret|body-secret|local/u);

    const markdown = await inspect("sample.md");
    expect(markdown.result.data.outline.map((entry: Record<string, unknown>) =>
      [entry.kind, entry.name, entry.level],
    )).toEqual([
      ["frontmatter", "title", undefined], ["frontmatter", "quoted key", undefined],
      ["frontmatter", "summary", undefined], ["frontmatter", "meta", undefined],
      ["heading", "Main *source*", 1], ["heading", "Visible", 3],
    ]);
    expect(JSON.stringify(markdown.result)).not.toMatch(
      /do-not-return|hidden-value|secret scalar|nested|excluded|hidden/u,
    );
    const duplicate = await inspect("duplicate.md");
    expect(duplicate.result.data.parse_complete).toBe(false);
    expect(duplicate.result.data.outline.map((entry: Record<string, unknown>) => entry.name)).toEqual(["a", "a"]);

    const json = await inspect("sample.json");
    expect(json.result.data.outline.map((entry: Record<string, unknown>) =>
      [entry.pointer, entry.value_type],
    )).toEqual([
      ["", "object"], ["/a~1b", "object"], ["/a~1b/~0key", "array"],
      ["/a~1b/~0key/0", "boolean"], ["/a~1b/~0key/1", "null"],
      ["/a~1b/~0key/2", "number"], ["/a~1b/~0key/3", "string"],
    ]);
    expect(JSON.stringify(json.result)).not.toContain("never-return");
    const recovered = await inspect("recovered.json");
    expect(recovered.result.data.parse_complete).toBe(false);
    expect(recovered.result.data.outline.map((entry: Record<string, unknown>) => entry.pointer)).toEqual([""]);
  });
});

describe("inspect_file language projections", () => {

  test("normalizes JavaScript, TypeScript, and Python declarations", async () => {
    const directory = await temporaryDirectory("inspect-file-");
    process.chdir(directory);
    await writeFile("sample.js", [
      "import primary, {remote as local} from \"pkg\"; import \"side\";",
      "export const callable = () => 1, value = 2; let mutable = 3;",
      "function run() { const hidden = 1; }",
      "class Box { field = \"secret\"; method() {} #private() {} 1() {} \"quoted\"() {} [\"literal\"]() {} [name]() {} static async *gen() {} }",
    ].join("\n"));
    await writeFile("sample.ts", [
      "interface Shape { field: string }", "type Name = string;",
      "enum Choice { One }",
      "class Typed { method(): void {} #private() {} 1() {} \"quoted\"() {} [\"literal\"]() {} [name]() {} static async *gen(): void {} }",
    ].join("\n"));
    await writeFile("sample.py", [
      "import package.module, second as alias",
      "from source.module import member as local",
      "from pkg import (",
      "    first, # inline",
      "    second as second_alias,",
      ")",
      "value = 1", "@decorate", "def run():", "    nested = \"secret\"",
      "@decorate", "class Box:", "    field = 1",
      "    @decorate", "    def method(self):", "        pass", "",
    ].join("\n"));
    await writeFile("assignments.py", [
      "a = b = 1",
      "obj.attr = 2",
      "items[0] = 3",
      "annotated: Type = 4",
      "left, (middle, right) = source_value",
      "[first_item, second_item] = source_value",
      "",
    ].join("\n"));

    expect((await inspectOutline("sample.js")).map((entry) =>
      [entry.kind, entry.name, entry.receiver],
    )).toEqual([
      ["import", "primary", undefined], ["import", "local", undefined],
      ["import", "side", undefined], ["constant", "callable", undefined],
      ["constant", "value", undefined], ["variable", "mutable", undefined],
      ["function", "run", undefined], ["class", "Box", undefined],
      ["method", "method", "Box"], ["method", "#private", "Box"],
      ["method", "1", "Box"], ["method", "\"quoted\"", "Box"],
      ["method", "[\"literal\"]", "Box"], ["method", "[name]", "Box"],
      ["method", "gen", "Box"],
    ]);
    expect((await inspectOutline("sample.ts")).map((entry) =>
      [entry.kind, entry.name, entry.receiver],
    )).toEqual([
      ["type", "Shape", undefined], ["type", "Name", undefined],
      ["type", "Choice", undefined], ["class", "Typed", undefined],
      ["method", "method", "Typed"], ["method", "#private", "Typed"],
      ["method", "1", "Typed"], ["method", "\"quoted\"", "Typed"],
      ["method", "[\"literal\"]", "Typed"], ["method", "[name]", "Typed"],
      ["method", "gen", "Typed"],
    ]);
    const python = await inspectOutline("sample.py");
    expect(python.map((entry) => [entry.kind, entry.name, entry.receiver, outlineLine(entry)])).toEqual([
      ["import", "package", undefined, 1], ["import", "alias", undefined, 1],
      ["import", "local", undefined, 2], ["import", "first", undefined, 3],
      ["import", "second_alias", undefined, 3], ["variable", "value", undefined, 7],
      ["function", "run", undefined, 8], ["class", "Box", undefined, 11],
      ["method", "method", "Box", 14],
    ]);
    expect(JSON.stringify(python)).not.toMatch(/nested|field|secret/u);
    const assignments = await inspectOutline("assignments.py");
    expect(assignments.map((entry) => entry.name)).toEqual([
      "a", "b", "annotated", "left", "middle", "right", "first_item", "second_item",
    ]);
    expect(JSON.stringify(assignments)).not.toMatch(/obj|items|Type|source_value/u);
  });

  test("recognizes every stable TypeScript 7 source format", async () => {
    const directory = await temporaryDirectory("inspect-typescript-formats-");
    process.chdir(directory);
    const fixtures = [
      ["sample.ts", "typescript", "export const value = 1;"],
      ["sample.tsx", "typescript", "export const value = <div />;"],
      ["sample.d.ts", "typescript", "export declare function value(\n  input: number,\n): number;"],
      ["sample.mts", "typescript", "export const value = 1;"],
      ["sample.d.mts", "typescript", "export declare function value(\n  input: number,\n): number;"],
      ["sample.cts", "typescript", "export const value = 1;"],
      ["sample.d.cts", "typescript", "export declare function value(\n  input: number,\n): number;"],
      ["sample.js", "javascript", "export const value = 1;"],
      ["sample.jsx", "javascript", "export const value = <div />;"],
      ["sample.mjs", "javascript", "export const value = 1;"],
      ["sample.cjs", "javascript", "exports.value = 1;"],
      ["sample.pyi", "python", "value: int"],
    ] as const;
    await Promise.all(fixtures.map(([name, , source]) => writeFile(name, `${source}\n`)));

    for (const [name, language] of fixtures) {
      const result = (await inspect(name)).result;
      expect(result.data.kind).toBe("code");
      expect(result.data.language).toBe(language);
      expect(result.data.parse_complete).toBe(true);
    }
    for (const name of ["sample.d.ts", "sample.d.mts", "sample.d.cts"]) {
      expect((await inspectOutline(name)).map((entry) => [
        entry.kind,
        entry.name,
        outlineLine(entry),
        outlineLine(entry, "line_end"),
      ]))
        .toEqual([["function", "value", 1, 3]]);
    }
    expect((await inspectOutline("sample.pyi")).map((entry) => [entry.kind, entry.name]))
      .toEqual([["variable", "value"]]);
  });
});

describe("inspect_file bounds and confinement", () => {

  test("emits LINE:HASH span identities without source bodies", async () => {
    const directory = await temporaryDirectory("inspect-file-hash-");
    process.chdir(directory);
    const source = [
      "package p",
      "func Visible() {",
      "	secret := \"body-secret\"",
      "}",
      "",
    ].join("\n");
    await writeFile("sample.go", source);
    const outline = await inspectOutline("sample.go");
    const lines = new LineMap(source);
    expect(outline.map((entry) => [entry.kind, entry.name, entry.line, entry.line_end])).toEqual([
      [
        "function",
        "Visible",
        `2:${hashLine(lines.logicalLine(2)?.text ?? "")}`,
        `4:${hashLine(lines.logicalLine(4)?.text ?? "")}`,
      ],
    ]);
    expect(JSON.stringify(outline)).not.toMatch(/body-secret|secret/u);
  });

  test("reports recovery, exact line counts, and unsupported metadata", async () => {
    const directory = await temporaryDirectory("inspect-file-");
    process.chdir(directory);
    await writeFile("broken.go", "package p\nfunc broken( {\n");
    expect((await inspect("broken.go")).result.data.parse_complete).toBe(false);
    for (const [name, content, count] of [
      ["empty.go", "", 0], ["plain.go", "a", 1], ["lf.go", "a\n", 1],
      ["crlf.go", "a\r\n", 1], ["cr.go", "a\rb", 2],
      ["only.go", "\n", 1], ["twice.go", "\n\n", 2],
    ] as const) {
      await writeFile(name, content);
      expect((await inspect(name)).result.data.line_count).toBe(count);
    }
    await writeFile("blob.bin", Uint8Array.from([0xff, 0xfe, 0xfd]));
    expect((await inspect("blob.bin")).result).toEqual({
      ok: true,
      data: {
        path: "blob.bin", kind: "none", language: null, size_bytes: 3,
        line_count: null, parse_complete: true, outline: [],
      },
      truncated: false,
      truncation: null,
    });
  });
});
