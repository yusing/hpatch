import type {Plugin, Tool} from "../../plugin.d.ts";
import {createHGrepTool} from "./hgrep.ts";
import {createHReadTool} from "./hread.ts";

const hreadDescription = `Use \`hread\` as replacement of \`cat\` or \`sed\`. Read up to 6 workspace paths, optionally
followed by an inclusive \`START:END\` line range. Use a bare path when it has no whitespace;
otherwise use a JSON-quoted path. Unlike plain file output, every complete line is returned
as \`LINE:HASH TEXT\`; copy the current \`LINE:HASH\` directly into an HPATCH/2 target.
`;

const hreadPath = `(?:"(?:\\\\(?:["\\\\/bfnrt]|u[0-9A-Fa-f]{4})|[^\\x00-\\x1F"\\\\]|\\t)*"|[^\\x00-\\x20"]+)`;
const hreadReadSpec = `${hreadPath}(?: [1-9][0-9]*:[1-9][0-9]*)?`;
const hreadRegex = `^${hreadReadSpec}(?:\\r?\\n${hreadReadSpec}){0,5}$`;

const hgrepDescription = `Use \`hgrep\` as replacement of \`rg\` or \`grep\`. It is a ripgrep wrapper that accepts familiar
arguments but returns complete matching and requested context lines as
\`"PATH":LINE:HASH TEXT\`. Copy the current \`LINE:HASH\` directly into an HPATCH/2 target.
`;

const hgrepGrammar = `start: WS? argument (WS argument)* WS?
argument: part+
?part: SINGLE_QUOTED | DOUBLE_QUOTED | BARE

WS: /[ \\t]+/
SINGLE_QUOTED: /'[^'\\r\\n]*'/
DOUBLE_QUOTED: /"(?:\\\\[^\\r\\n]|[^"\\\\\\r\\n])*"/
BARE: /(?:\\\\[^\\r\\n]|[^\\s'"\\\\])+/
`;

type BuiltinPlugin = Omit<Plugin, "tools"> & {
  tools: [Tool<string>, Tool<string>];
};

const plugin: BuiltinPlugin = {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "builtin.hpatch",
  tools: [
    createHReadTool(hreadDescription, hreadRegex),
    createHGrepTool(hgrepDescription, hgrepGrammar),
  ],
};

export default plugin;
