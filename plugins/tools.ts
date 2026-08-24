import type {Plugin, Tool} from "../internal/router/toolplugin/plugin.d.ts";
import {createHGrepTool} from "./hgrep.ts";
import {createHReadTool} from "./hread.ts";
import {createHSymbolTool} from "./hsymbol.ts";
import {createInspectFileTool, inspectFileDescription} from "./inspect_file.ts";
import {shellTool} from "./shell.mjs";

const verifiedRowLimitDescription = "An incomplete token-limited result retains complete rows, writes stderr, and exits nonzero.";
const hreadDescription = `Read one UTF-8 file or inclusive logical-line range and emit verified \`LINE:HASH TEXT\` rows. Usage: \`hread PATH [START:END]\`. ${verifiedRowLimitDescription}`;

const hreadPath = `(?:"(?:\\\\(?:["\\\\/bfnrt]|u[0-9A-Fa-f]{4})|[^\\x00-\\x1F"\\\\]|\\t)*"|[^\\x00-\\x20"]+)`;
const hreadReadSpec = `${hreadPath}(?: (?:0|[1-9][0-9]*):[1-9][0-9]*)?`;
const hreadRegex = `\\A${hreadReadSpec}\\z`;
const inspectFileRegex = `\\A${hreadPath}\\z`;

const hgrepDescription = `Search files with supported ripgrep arguments and emit verified complete rows as \`"PATH":LINE:HASH TEXT\`. ${verifiedRowLimitDescription}`;

const hgrepPart = `(?:'[^'\\r\\n]*'|"(?:\\\\[^\\r\\n]|[^"\\\\\\r\\n])*"|(?:\\\\[^\\r\\n]|[^\\s'"\\\\])+)`;
const hgrepRegex = `\\A[ \\t]*${hgrepPart}+(?:[ \\t]+${hgrepPart}+)*[ \\t]*\\z`;

const hsymbolDescription = `Resolve one verified Go, JavaScript, TypeScript, JSON, or Python symbol and emit complete rows as \`"PATH":LINE:HASH TEXT\`. Usage: \`hsymbol (def|refs) PATH LINE:HASH SYMBOL [N]\`. N selects an exact language-token occurrence. Stale rows, ambiguous selectors, unavailable language servers, and definitions without an editable workspace location fail without stdout rows. ${verifiedRowLimitDescription}`;
const hsymbolRegex = `\\A(?:def|refs) ${hreadPath} [1-9][0-9]*:[0-9a-f]{4} [^\\x00-\\x20]+(?: [1-9][0-9]*)?\\z`;

type BuiltinPlugin = Omit<Plugin, "tools"> & {
  tools: [Tool<string[]>, Tool<string[]>, Tool<string[]>, Tool<string[]>, typeof shellTool];
};

const plugin: BuiltinPlugin = {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "builtin.shell",
  tools: [
    createHReadTool(hreadDescription, hreadRegex),
    createHGrepTool(hgrepDescription, hgrepRegex),
    createHSymbolTool(hsymbolDescription, hsymbolRegex),
    createInspectFileTool(inspectFileDescription, inspectFileRegex),
    shellTool,
  ],
};

export default plugin;
