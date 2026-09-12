import {afterEach, expect, test} from "bun:test";
import {mkdtemp, rm, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";

import {createHGrepTool} from "../../../../plugins/hgrep.ts";

const originalPath = process.env.PATH;
const directories: string[] = [];

afterEach(async () => {
  if (originalPath === undefined) {
    delete process.env.PATH;
  } else {
    process.env.PATH = originalPath;
  }
  await Promise.all(directories.splice(0).map((directory) => rm(directory, {recursive: true, force: true})));
});

test.each([
  ["rg: missing.txt: No such file or directory (os error 2)", "No such file or directory (os error 2)"],
  ["rg: private: path.txt: Permission denied (os error 13)", "Permission denied (os error 13)"],
  ["rg: missing.txt: IO error for operation on missing.txt: No such file or directory (os error 2)", "No such file or directory (os error 2)"],
  ["regex parse error:\nerror: unclosed character class", "error: unclosed character class"],
])("preserves useful ripgrep diagnostics without redundant paths: %s", async (diagnostic, expected) => {
  const directory = await mkdtemp(path.join(tmpdir(), "hgrep-diagnostic-"));
  directories.push(directory);
  const executable = path.join(directory, "rg");
  await writeFile(executable, '#!/bin/sh\ncat "${0}.stderr" >&2\nexit 2\n', {mode: 0o700});
  await writeFile(`${executable}.stderr`, `${diagnostic}\n`, "utf8");
  process.env.PATH = `${directory}${path.delimiter}${originalPath ?? ""}`;

  const result = await createHGrepTool("description", "start: TEST").execute(["needle", "missing.txt"], {
    stdinFD: null,
    scriptReadFD: null,
    scriptWriteFD: null,
    outputBudgetBytes: 16 * 1024 * 1024,
  });
  expect(result).toEqual({stderr: `hgrep: ${expected}\n`, exitCode: 1, failureClass: "search_error"});
});
