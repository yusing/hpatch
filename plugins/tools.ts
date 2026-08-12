import type {Plugin, Tool} from "../internal/router/toolplugin/plugin.d.ts";
import {createHGrepTool} from "./hgrep.ts";
import {createHReadTool} from "./hread.ts";
import {createInspectFileTool, inspectFileDescription} from "./inspect_file.ts";
import {shellTool} from "./shell.mjs";

const hreadDescription = `Read one UTF-8 file or inclusive logical-line range and emit verified \`LINE:HASH TEXT\` rows. Usage: \`hread PATH [START:END]\`.`;

const hreadPath = `(?:"(?:\\\\(?:["\\\\/bfnrt]|u[0-9A-Fa-f]{4})|[^\\x00-\\x1F"\\\\]|\\t)*"|[^\\x00-\\x20"]+)`;
const hreadReadSpec = `${hreadPath}(?: (?:0|[1-9][0-9]*):[1-9][0-9]*)?`;
const hreadRegex = `\\A${hreadReadSpec}\\z`;
const inspectFileRegex = `\\A${hreadPath}\\z`;

const hgrepDescription = `Search files with supported ripgrep arguments and emit verified complete rows as \`"PATH":LINE:HASH TEXT\`.`;

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
