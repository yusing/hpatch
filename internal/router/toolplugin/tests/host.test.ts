import {afterEach, describe, expect, test} from "bun:test";
import {spawnSync} from "node:child_process";
import {mkdtemp, rm, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import {fileURLToPath} from "node:url";

type HostResponse = {
  errors: string[];
  plugins: Array<{
    id: string;
    module: string;
    tools: Array<{
      specification: Record<string, unknown>;
    }>;
  }>;
};

const hostPath = fileURLToPath(new URL("../host.mjs", import.meta.url));
const temporaryDirectories: string[] = [];

async function temporaryDirectory(): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), "tool-plugin-host-"));
  temporaryDirectories.push(directory);
  return directory;
}

afterEach(async () => {
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) => rm(directory, {recursive: true, force: true})),
  );
});

function invokeHost(
  snapshotRoot: string,
  request: Record<string, unknown>,
): {status: number | null; stdout: string; stderr: string} {
  const result = spawnSync("node", [hostPath], {
    cwd: snapshotRoot,
    encoding: "utf8",
    env: {...process.env, NODE_NO_WARNINGS: "1"},
    input: JSON.stringify({...request, snapshotRoot}),
  });
  return {
    status: result.status,
    stdout: result.stdout,
    stderr: result.stderr,
  };
}

function pluginDeclaration(format?: {
  type: string;
  syntax: string;
  definition: string;
}): string {
  const formatField = format === undefined ? "" : `, format: ${JSON.stringify(format)}`;
  return `export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "grammar.test",
  tools: [{
    specification: {
      type: "custom",
      name: "grammar_test",
      description: "test tool"${formatField}
    },
    parse(input) { return input; },
    argv(input) { return [input]; },
    translate(_input, api) { return api.exec(); },
    execute() { return {stdout: "", exitCode: 0}; }
  }]
};
`;
}

async function validateDeclaration(
  declaration: string,
): Promise<{result: ReturnType<typeof invokeHost>; response: HostResponse}> {
  const directory = await temporaryDirectory();
  await writeFile(path.join(directory, "plugin.mjs"), declaration, "utf8");
  const result = invokeHost(directory, {
    operation: "validate",
    modules: ["plugin.mjs"],
  });
  expect(result.status).toBe(0);
  return {
    result,
    response: JSON.parse(result.stdout) as HostResponse,
  };
}

describe("plugin declaration validation", () => {
  test.each([
    [
      "lark",
      "start: (\n  WORD\n  | \"ok\"\n)\n%import common.WORD\n%import common.WS\n%ignore WS",
    ],
    ["regex", "^(?:[a-z]+|[0-9]{1,3})$"],
  ])("accepts the supported %s grammar subset", async (syntax, definition) => {
    const {response} = await validateDeclaration(pluginDeclaration({
      type: "grammar",
      syntax,
      definition,
    }));
    expect(response.errors).toEqual([]);
    expect(response.plugins).toHaveLength(1);
    expect(response.plugins[0]?.tools[0]?.specification).toMatchObject({
      name: "grammar_test",
      format: {type: "grammar", syntax, definition},
    });
  });

  test.each([
    ["invalid token", "lark", "start: @", "unexpected token"],
    ["priority", "lark", "start.2: \"ok\"", "unsupported priority"],
    ["template", "lark", "template{x}: x\nstart: template{\"ok\"}", "invalid rule declaration"],
    ["non-common import", "lark", "%import other.WORD\nstart: WORD", "imports outside common"],
    ["declare", "lark", "%declare WORD\nstart: WORD", "unsupported %declare"],
    ["duplicate rule", "lark", "start: \"a\"\nstart: \"b\"", "defined more than once"],
    ["duplicate terminal", "lark", "TOKEN: \"a\"\nTOKEN: \"b\"\nstart: TOKEN", "defined more than once"],
    ["newline", "regex", "one\ntwo", "must be one line"],
    ["lookaround", "regex", "(?=ok)ok", "unsupported construct"],
    ["lazy quantifier", "regex", "ok+?", "unsupported construct"],
    ["backreference", "regex", String.raw`(ok)\1`, "unsupported backreference"],
  ])("rejects %s", async (_name, syntax, definition, diagnostic) => {
    const {response} = await validateDeclaration(pluginDeclaration({
      type: "grammar",
      syntax,
      definition,
    }));
    expect(response.plugins).toEqual([]);
    expect(response.errors.join("\n")).toContain(diagnostic);
  });

  test("reports independent declaration errors together", async () => {
    const directory = await temporaryDirectory();
    await Promise.all([
      writeFile(
        path.join(directory, "bad-default.mjs"),
        "export default {apiVersion: 'wrong'};\n",
        "utf8",
      ),
      writeFile(
        path.join(directory, "bad-tool.mjs"),
        pluginDeclaration().replace('name: "grammar_test"', 'name: "eval"'),
        "utf8",
      ),
    ]);
    const result = invokeHost(directory, {
      operation: "validate",
      modules: ["bad-default.mjs", "bad-tool.mjs"],
    });
    expect(result.status).toBe(0);
    const response = JSON.parse(result.stdout) as HostResponse;
    expect(response.plugins).toEqual([]);
    expect(response.errors.join("\n")).toContain("default export must contain only");
    expect(response.errors.join("\n")).toContain("collides with a shell keyword or built-in");
  });
});

describe("plugin translation and execution", () => {
  test("applies parse, argv, and carrier contracts", async () => {
    const directory = await temporaryDirectory();
    await writeFile(
      path.join(directory, "plugin.mjs"),
      `export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "translation.test",
  tools: [{
    specification: {type: "custom", name: "translation_test", description: "test tool"},
    parse(input) {
      if (input === "reject") throw new Error("input rejected");
      return input;
    },
    argv(input) { return ["--fixed", input]; },
    translate(input, api) {
      if (input === "custom") return api.custom("exec", "custom payload");
      if (input === "function") return api.function("lookup", "{\\"value\\":1}");
      if (input === "template") return api.exec("before | {.} | after");
      if (input === "params") return api.exec(undefined, {workdir: "/tmp"});
      if (input === "stock") return api.exec(undefined, undefined, "python3 -c 'print(1)'");
      if (input === "invalid-params") return api.exec(undefined, {cmd: "forbidden"});
      if (input === "invalid-login") return api.exec(undefined, {login: true});
      if (input === "invalid-undefined") return api.exec(undefined, {workdir: undefined});
      if (input === "invalid-function") return api.exec(undefined, {tty: () => true});
      if (input === "invalid-symbol") return api.exec(undefined, {tty: Symbol("tty")});
      if (input === "invalid-nan") return api.exec(undefined, {tty: Number.NaN});
      if (input === "invalid-template") return api.exec("missing placeholder");
      if (input === "invalid-stock") return api.exec(undefined, undefined, "");
      if (input === "malformed") return {kind: "exec", payload: "unexpected"};
      return api.exec();
    },
    execute(argv) {
      return {stdout: argv.join("|"), stderr: "fixture stderr", stock: {stdout: "stock output", exitCode: 0}, exitCode: 7};
    }
  }]
};
`,
      "utf8",
    );

    for (const [input, carrier] of [
      ["exec", {kind: "exec"}],
      ["template", {kind: "exec", template: "before | {.} | after"}],
      ["params", {kind: "exec", params: {workdir: "/tmp"}}],
      ["stock", {kind: "exec", stockCommand: "python3 -c 'print(1)'"}],
      ["custom", {kind: "custom", name: "exec", payload: "custom payload"}],
      ["function", {kind: "function", name: "lookup", payload: "{\"value\":1}"}],
    ] as const) {
      const translated = invokeHost(directory, {
        operation: "translate",
        module: "plugin.mjs",
        index: 0,
        input,
      });
      expect(translated.status).toBe(0);
      expect(JSON.parse(translated.stdout)).toEqual({
        rejected: false,
        diagnostic: "",
        arguments: ["--fixed", input],
        carrier,
      });
    }

    const rejected = invokeHost(directory, {
      operation: "translate",
      module: "plugin.mjs",
      index: 0,
      input: "reject",
    });
    expect(rejected.status).toBe(0);
    expect(JSON.parse(rejected.stdout)).toMatchObject({
      rejected: true,
      diagnostic: "input rejected",
      arguments: [],
    });

    const malformed = invokeHost(directory, {
      operation: "translate",
      module: "plugin.mjs",
      index: 0,
      input: "malformed",
    });
    expect(malformed.status).toBe(1);
    expect(malformed.stderr).toContain("translator returned a malformed carrier");

    const invalidTemplate = invokeHost(directory, {
      operation: "translate",
      module: "plugin.mjs",
      index: 0,
      input: "invalid-template",
    });
    expect(invalidTemplate.status).toBe(1);
    expect(invalidTemplate.stderr).toContain("translator returned a malformed carrier");

    const invalidStock = invokeHost(directory, {
      operation: "translate",
      module: "plugin.mjs",
      index: 0,
      input: "invalid-stock",
    });
    expect(invalidStock.status).toBe(1);
    expect(invalidStock.stderr).toContain("translator returned a malformed carrier");

    const invalidParams = invokeHost(directory, {
      operation: "translate",
      module: "plugin.mjs",
      index: 0,
      input: "invalid-params",
    });
    expect(invalidParams.status).toBe(1);
    expect(invalidParams.stderr).toContain("exec carrier params must be an object without cmd");

    const invalidLogin = invokeHost(directory, {
      operation: "translate",
      module: "plugin.mjs",
      index: 0,
      input: "invalid-login",
    });
    expect(invalidLogin.status).toBe(1);
    expect(invalidLogin.stderr).toContain("exec carrier params login must be false");

    for (const input of ["invalid-undefined", "invalid-function", "invalid-symbol", "invalid-nan"]) {
      const invalidJSONValue = invokeHost(directory, {
        operation: "translate",
        module: "plugin.mjs",
        index: 0,
        input,
      });
      expect(invalidJSONValue.status).toBe(1);
      expect(invalidJSONValue.stderr).toContain("exec carrier params must contain only JSON-native values");
    }

    const executed = invokeHost(directory, {
      operation: "execute",
      outputBudgetBytes: 16 * 1024 * 1024,
      module: "plugin.mjs",
      index: 0,
      arguments: ["one", "two words"],
    });
    expect(executed.status).toBe(0);
    expect(JSON.parse(executed.stdout)).toEqual({
      stdout: "one|two words",
      stderr: "fixture stderr",
      stock: {stdout: "stock output", stderr: "", exitCode: 0},
      exitCode: 7,
    });
  });

  test("rejects invalid executor results", async () => {
    const directory = await temporaryDirectory();
    await writeFile(
      path.join(directory, "plugin.mjs"),
      pluginDeclaration().replace(
        'execute() { return {stdout: "", exitCode: 0}; }',
        'execute() { return {stdout: 1, exitCode: 300}; }',
      ),
      "utf8",
    );
    const result = invokeHost(directory, {
      operation: "execute",
      outputBudgetBytes: 16 * 1024 * 1024,
      module: "plugin.mjs",
      index: 0,
      arguments: [],
    });
    expect(result.status).toBe(1);
    expect(result.stderr).toContain(
      "executor must return stdout/stderr strings and an exitCode from 0 through 255",
    );
  });

  test("ignores invalid or throwing optional stock evidence", async () => {
    for (const execute of [
      'execute() { return {stdout: "current", stock: {stdout: 1}, exitCode: 0}; }',
      'execute() { return {stdout: "current", get stock() { throw new Error("stock"); }, exitCode: 0}; }',
      'execute() { return {stdout: "current", stock: {get stdout() { throw new Error("stdout"); }, exitCode: 0}, exitCode: 0}; }',
      'execute() { return {stdout: "current", stock: {stdout: "stock", exitCode: 0}, exitCode: 0}; }',
    ]) {
      const directory = await temporaryDirectory();
      await writeFile(
        path.join(directory, "plugin.mjs"),
        pluginDeclaration().replace(
          'execute() { return {stdout: "", exitCode: 0}; }',
          execute,
        ),
        "utf8",
      );
      const result = invokeHost(directory, {
        operation: "execute",
        outputBudgetBytes: Buffer.byteLength("current", "utf8"),
        module: "plugin.mjs",
        index: 0,
        arguments: [],
      });
      expect(result.status).toBe(0);
      expect(JSON.parse(result.stdout)).toEqual({
        stdout: "current",
        stderr: "",
        exitCode: 0,
      });
    }
  });
});
