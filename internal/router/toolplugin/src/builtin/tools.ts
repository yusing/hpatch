import type {Plugin, Tool} from "../../plugin.d.ts";
import {createHGrepTool} from "./hgrep.ts";
import {createHReadTool} from "./hread.ts";
import {shellTool} from "../../../../../plugins/shell.mjs";

const hreadDescription = `Use \`hread\` through \`shell\` instead of \`cat\` or \`sed\` when source rows may
become HPATCH targets. Run one file per command as \`hread PATH [START:END]\`; quote paths
with shell syntax and batch related reads as separate commands in one shell script. A bare
path reads the complete file. Output is \`LINE:HASH TEXT\`; copy the current \`LINE:HASH\`
directly into an HPATCH/2 target.`;

const hreadPath = `(?:"(?:\\\\(?:["\\\\/bfnrt]|u[0-9A-Fa-f]{4})|[^\\x00-\\x1F"\\\\]|\\t)*"|[^\\x00-\\x20"]+)`;
const hreadReadSpec = `${hreadPath}(?: [1-9][0-9]*:[1-9][0-9]*)?`;
const hreadRegex = `\\A${hreadReadSpec}\\z`;

const hgrepDescription = `Use \`hgrep\` through \`shell\` instead of \`rg\` or \`grep\` when search results may
become HPATCH targets. It accepts familiar ripgrep arguments and ordinary shell quoting,
redirection, and pipelines. Combine known patterns and paths in one command and use repeated
\`-e\` for multiple patterns. Output is \`"PATH":LINE:HASH TEXT\`; copy the current
\`LINE:HASH\` directly into an HPATCH/2 target. Never guess or reconstruct a row.`;

const hgrepPart = `(?:'[^'\\r\\n]*'|"(?:\\\\[^\\r\\n]|[^"\\\\\\r\\n])*"|(?:\\\\[^\\r\\n]|[^\\s'"\\\\])+)`;
const hgrepRegex = `\\A[ \\t]*${hgrepPart}+(?:[ \\t]+${hgrepPart}+)*[ \\t]*\\z`;

type BuiltinPlugin = Omit<Plugin, "tools"> & {
  tools: [Tool<string[]>, Tool<string[]>, typeof shellTool];
};

const plugin: BuiltinPlugin = {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "builtin.shell",
  tools: [
    createHReadTool(hreadDescription, hreadRegex),
    createHGrepTool(hgrepDescription, hgrepRegex),
    shellTool,
  ],
};

export default plugin;
