import type {Plugin, Tool} from "../internal/router/toolplugin/plugin.d.ts";
import {createHGrepTool} from "./hgrep.ts";
import {createHReadTool} from "./hread.ts";
import {createInspectFileTool, inspectFileDescription} from "./inspect_file.ts";
import {shellTool} from "./shell.mjs";

const hreadDescription = `Use \`hread\` through \`shell\` only when you expect its returned \`LINE:HASH\` rows to
become HPATCH targets. For exploration, diagnosis, or validation—including checking a named
diagnostic—use ordinary read commands. When target-bearing context is needed, use \`hread\`
instead of \`cat\` or \`sed\`. Run one file per command as \`hread PATH [START:END]\`; quote paths
with shell syntax and batch related reads as separate commands in one shell script. A bare path
reads the complete file. A start line of \`0\` begins at line 1 without emitting line 0. Missing
lines beyond EOF produce a warning after any available rows and do not fail the command. Copy a
current \`LINE:HASH\` directly into an HPATCH/2 target.
Reason carefully about the command and make sure it matches the \`hread PATH [START:END]\` syntax.`;

const hreadPath = `(?:"(?:\\\\(?:["\\\\/bfnrt]|u[0-9A-Fa-f]{4})|[^\\x00-\\x1F"\\\\]|\\t)*"|[^\\x00-\\x20"]+)`;
const hreadReadSpec = `${hreadPath}(?: (?:0|[1-9][0-9]*):[1-9][0-9]*)?`;
const hreadRegex = `\\A${hreadReadSpec}\\z`;
const inspectFileRegex = `\\A${hreadPath}\\z`;


const hgrepDescription = `Use \`hgrep\` through \`shell\` only when you expect its returned matches to become
HPATCH targets. For exploration, diagnosis, validation, or owner discovery, use ordinary search
commands. When target-bearing matches are needed, use \`hgrep\` instead of \`rg\` or \`grep\`. It
accepts familiar ripgrep arguments and ordinary shell quoting, redirection, and pipelines.
Combine known patterns and paths in one command and use repeated \`-e\` for multiple patterns.
Output is \`"PATH":LINE:HASH TEXT\`; copy a current \`LINE:HASH\` directly into an HPATCH/2 target.
Never guess or reconstruct a row.
Reason carefully about the command and make sure it matches hgrep's stated syntax.`;

const hgrepPart = `(?:'[^'\\r\\n]*'|"(?:\\\\[^\\r\\n]|[^"\\\\\\r\\n])*"|(?:\\\\[^\\r\\n]|[^\\s'"\\\\])+)`;
const hgrepRegex = `\\A[ \\t]*${hgrepPart}+(?:[ \\t]+${hgrepPart}+)*[ \\t]*\\z`;

type BuiltinPlugin = Omit<Plugin, "tools"> & {
  tools: [Tool<string[]>, Tool<string[]>, Tool<string[]>, typeof shellTool];
};

const plugin: BuiltinPlugin = {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "builtin.shell",
  tools: [
    createHReadTool(hreadDescription, hreadRegex),
    createHGrepTool(hgrepDescription, hgrepRegex),
    createInspectFileTool(inspectFileDescription, inspectFileRegex),
    shellTool,
  ],
};

export default plugin;
