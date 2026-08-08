import type {Plugin, Tool} from "../../plugin.d.ts";
import {createHGrepTool} from "./hgrep.ts";
import {createHReadTool} from "./hread.ts";

const hreadDescription = `Use \`hread\` as replacement of \`cat\` or \`sed\`. Plan related reads before calling:
batch already-known paths or ranges in one call, and use explicit ranges after the relevant
locations are known. A bare path intentionally reads the complete file. Read up to 6 workspace
paths, optionally followed by an inclusive \`START:END\` line range. Use a bare path when it has
no whitespace; otherwise use a JSON-quoted path. Unlike plain file output, every complete line
is returned as \`LINE:HASH TEXT\`; copy the current \`LINE:HASH\` directly into an HPATCH/2 target.
`;

const hreadPath = `(?:"(?:\\\\(?:["\\\\/bfnrt]|u[0-9A-Fa-f]{4})|[^\\x00-\\x1F"\\\\]|\\t)*"|[^\\x00-\\x20"]+)`;
const hreadReadSpec = `${hreadPath}(?: [1-9][0-9]*:[1-9][0-9]*)?`;
const hreadRegex = `\\A${hreadReadSpec}(?:\\r?\\n${hreadReadSpec}){0,5}\\z`;

const hgrepDescription = `Use \`hgrep\` as replacement of \`rg\` or \`grep\`. Plan related searches before calling:
combine known patterns and paths in one call. The input is one ripgrep argument line; use repeated
\`-e\` for multiple patterns. For example: \`hgrep -n -e 'RangeStream' -e 'type RaftKV' path...\`.
It accepts familiar ripgrep arguments but returns complete matching and requested context lines as
\`"PATH":LINE:HASH TEXT\`. Copy the current \`LINE:HASH\` directly into an HPATCH/2 target.
`;

const hgrepPart = `(?:'[^'\\r\\n]*'|"(?:\\\\[^\\r\\n]|[^"\\\\\\r\\n])*"|(?:\\\\[^\\r\\n]|[^\\s'"\\\\])+)`;
const hgrepRegex = `\\A[ \\t]*${hgrepPart}+(?:[ \\t]+${hgrepPart}+)*[ \\t]*\\z`;

type BuiltinPlugin = Omit<Plugin, "tools"> & {
  tools: [Tool<string[]>, Tool<string[]>];
};

const plugin: BuiltinPlugin = {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "builtin.hpatch",
  tools: [
    createHReadTool(hreadDescription, hreadRegex),
    createHGrepTool(hgrepDescription, hgrepRegex),
  ],
};

export default plugin;
