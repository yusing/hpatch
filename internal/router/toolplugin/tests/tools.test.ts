import {afterEach, describe, expect, test} from "bun:test";
import {spawnSync} from "node:child_process";
import {mkdtemp, mkdir, rm, symlink, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";

import {formatHashLine} from "../../../../plugins/common.ts";
import {createHGrepTool, splitArguments} from "../../../../plugins/hgrep.ts";
import {createHReadTool} from "../../../../plugins/hread.ts";
import {createInspectFileTool, inspectFileDescription} from "../../../../plugins/inspect_file.ts";
import plugin from "../../../../plugins/tools.ts";

const originalCWD = process.cwd();
const originalPath = process.env.PATH;
const originalSessionID = process.env.CODEX_THREAD_ID;
const temporaryDirectories: string[] = [];
const executionContext = {stdinFD: null, scriptReadFD: null, scriptWriteFD: null, outputBudgetBytes: 16 * 1024 * 1024};

async function temporaryDirectory(prefix: string): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

async function inspect(...argv: string[]) {
  const tool = createInspectFileTool("description", String.raw`\A.+\z`);
  const execution = await tool.execute(argv, executionContext);
  return {...execution, raw: execution.stdout ?? "", result: JSON.parse(execution.stdout ?? "")};
}

async function inspectOutline(name: string): Promise<Record<string, unknown>[]> {
  return (await inspect(name)).result.data.outline;
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
  if (originalSessionID === undefined) {
    delete process.env.CODEX_THREAD_ID;
  } else {
    process.env.CODEX_THREAD_ID = originalSessionID;
  }
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) => rm(directory, {recursive: true, force: true})),
  );
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

  test("supplies cat and cat-plus-sed stock exec commands", async () => {
    const tool = createHReadTool("description", "start: TEST");
    const context = {resolvePath: (path: string) => path};
    const translate = async (input: string) => tool.translate(await tool.parse(input, context), {
      exec: (_template, _params, stockCommand) => ({kind: "exec", stockCommand}),
    });

    expect((await translate("plain.txt")).stockCommand).toBe("cat plain.txt");
    expect((await translate("plain.txt 2:9")).stockCommand).toBe("cat plain.txt | sed -n '2,9p'");
    expect((await translate("plain.txt 0:9")).stockCommand).toBe("cat plain.txt | sed -n '1,9p'");
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

    const zeroRange = await tool.execute(["plain.txt", "0:3"], executionContext);
    expect(zeroRange).toEqual(whole);

    const overrun = await tool.execute(["plain.txt", "2:5"], executionContext);
    expect(overrun).toEqual({
      stdout: [
        formatHashLine(2, "beta"),
        formatHashLine(3, "gamma"),
      ].join(""),
      stderr: "hread: 4-5: [out of range]\n",
      stock: {
        stdout: "beta\ngamma\n",
        exitCode: 0,
      },
      exitCode: 0,
    });

    const outside = await tool.execute(["plain.txt", "4:5"], executionContext);
    expect(outside).toEqual({
      stdout: "",
      stderr: "hread: 4-5: [out of range]\n",
      stock: {
        stdout: "",
        exitCode: 0,
      },
      exitCode: 0,
    });

    const missing = await tool.execute(["missing.txt"], executionContext);
    expect(missing).toEqual({
      stderr: "hread: ENOENT: no such file or directory\n",
      exitCode: 1,
    });

    const retainedDirectory = await temporaryDirectory("hpatch-");
    const sessionID = path.basename(retainedDirectory).slice("hpatch-".length);
    await writeFile(path.join(retainedDirectory, "call-id"), "retained\n", "utf8");
    process.env.CODEX_THREAD_ID = sessionID;
    const retained = await tool.execute(["@shell/call-id"], executionContext);
    expect(retained).toEqual({
      stdout: formatHashLine(1, "retained"),
      stock: {stdout: "retained\n", exitCode: 0},
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
    expect(python.map((entry) => [entry.kind, entry.name, entry.receiver, entry.line])).toEqual([
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
});

describe("inspect_file bounds and confinement", () => {

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
