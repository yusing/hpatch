<!-- hpatch-model-instructions:start -->
## File editing

Use `functions.hpatch` for local file edits, not `apply_patch`.
Hpatch is translated internally, so `apply_patch` can still appear in execution history.
Use shell formatting commands for bulk mechanical rewrites, but do not create or edit files
with shell write tricks or Python when hpatch is sufficient.

## Shell execution

Use `functions.shell` for shell commands. Submit one free-form script without an outer heredoc
or command-string wrapper. The selected interpreter receives the exact script body, and frontend
standard input remains available as program data.

The default interpreter is Bash. For another interpreter, put its command and arguments in a
compact shebang on the first line, such as `#!uv run python` or
`#!node --experimental-strip-types`. Use a direct command or path rather than `/usr/bin/env`.
Omit Bash shebangs, and pass the script directly instead of wrapping it in Bash, `-c` or `-e`
command-string quoting, or a heredoc.

Optional `#!key=value` directives follow the interpreter shebang or appear first. `#!cmd=`
accepts exactly one `{.}` placeholder, which expands to the normalized shell frontend command
while leaving standard input available to the script. `#!params=<JSON object>` supplies
request-specific outer execution arguments and may appear before or after `#!cmd=`. The body
supplies `cmd`, so omit it from params; when `login` is present, it must be `false`.

For example, keep a producer in `#!cmd=` and write the consumer directly as the body:

```python
#!python3
#!cmd=curl -fsS https://example.com/data.json | {.}

import json
import sys

records = json.load(sys.stdin)
print("count", len(records))
for record in records:
    print(record["name"])
```

A retained result includes `retained: true` and a `script_ref`. Read the source with
`hread @shell/<reference>`, edit it with hpatch, or rerun its current content with a shell call
containing only `#!script=@shell/<reference>`. An hpatch script using an `@shell/` path must use
only `@shell/` paths; never mix retained scripts and workspace files in one hpatch script.

Shell can start PTY-backed, interactive, and long-running programs. When execution yields a
session handle, use the native session facilities to send input, poll output, resize the PTY, or
terminate the process. Each shell call starts a new execution.

## HPATCH/2

HPATCH/2 applies one complete target-bearing edit script atomically. Do not call this tool
in parallel with other tools. Rejection or cancellation changes nothing.

Commands:

```text
in PATH
new PATH
mv PATH
rm
type TARGET VALUE
type- TARGET VALUE
type+ TARGET VALUE
```

`in` selects an existing file. `new` selects a pending empty file. `mv` moves the active
file and preserves its baseline and pending edits. `rm` deletes the active file and clears
the selection. Repeat `in PATH` when switching existing files.

Targets:

```text
LINE:HASH                         complete logical line
LINE:HASH..LINE:HASH              inclusive complete-line range
LINE:HASH "TEXT" [N]              first N exact matches from that row through EOF
"TEXT" [N]                        first N exact matches in the immutable baseline
```

`type` replaces. An empty target-bearing `type` value deletes every target span, including
terminators owned by line and range targets. `type-` inserts before while preserving the
target; `type+` inserts after while preserving it. A text target defaults to one match;
every requested non-overlapping match must exist or the script rejects.

Use inline JSON-compatible strings for short or single-line values. Include `\n` when a
before/after insertion must form a complete new line:

```text
in parser.go
type- 37:8c2f "// parseCommand parses one physical script line.\n"
type "return oldResult, nil" "return newResult, nil"
```

Use the fixed `<<PATCH` frame for multiline or escape-heavy values:

```text
in service.go
type 20:2ff7..28:d10b <<PATCH
func calculateResult(input Input) (Result, error) {
	return computeFreshResult(input), nil
}
PATCH
```

An unindented heredoc body line that begins with `type `, `type- `, or `type+ ` and then
contains only `<<PATCH` or ends with ` <<PATCH` is reserved as a nested opener. Close the
current frame first; use an inline value or indent literal HPATCH examples.

Create a file with at most one immediately following targetless initializer:

```text
new internal/target.go
type "package internal\n"
```

Every existing file has one immutable baseline for the complete invocation. Pending edits
do not shift later targets. When inspected files are ready, submit every known related edit
in one atomic script, including related multiline declarations and repeated `in PATH`
sections. Split only when a later edit depends on validation or information unavailable
before the current call. Keep unrelated large `<<PATCH` values in separate failure-domain
calls. Prefer the smallest mutation that expresses the semantic change. When a formatter
owns formatting, alignment, or indentation, do not replace surrounding lines merely to
reproduce its output; let the formatter apply those changes. For example, add one struct
field with one insertion rather than replacing the declaration. Preserve required
indentation prefixes in indentation-sensitive languages such as Python.

When an exact current literal is already known but its row is absent or stale, target the literal
without a row to verify it against the complete immutable baseline. This avoids a read solely to
refresh location. Use a row anchor when repeated text makes the intended position significant.

Content introduced by a mutation is not targetable in the same call. After every successful
invocation, discard every saved row for each changed path. For a follow-up edit, use a returned
final-state row or target exact known current text without a row. Never submit a pre-edit row for
recovery to refresh. Use focused hread or hgrep only when neither form identifies the target, and
never reconstruct a row or range endpoint.

A successful hpatch report and its language validation are authoritative: do not hread changed
paths merely to verify the edit. Run the focused behavioral check instead. If another edit is
needed, use returned final-state rows directly and acquire only an exact missing target.

Nonempty line and range `type` replacements preserve the target's final LF, CRLF, or CR
when the value omits a terminator. Explicit terminators are authoritative. An empty
target-bearing `type` value removes owned terminators. `type-` and `type+` insert
byte-exact values and do not synthesize newlines.

Overlapping replacements or deletions and insertions strictly inside them reject. Boundary
insertions are valid. Multiple insertions at the same boundary render in script order.

Changed Go files are parsed and formatted before success; do not run redundant `gofmt`. Add Go
imports inside the existing import declaration, not adjacent to it; formatting cannot repair an
invalid import placement.
Supported Python, JavaScript, and TypeScript files are syntax-checked when Tree-sitter support
is available; supported indentation corrections are automatic. Relative paths use the selected
base directory when available; without one, relative paths reject; parents for `new` or `mv`
must exist. Routed rejection diagnostics expose a complete current `C...` command manifest and
bounded `V...` value-row context. Use `functions.hpatch_recover` for the smallest correction
against that immutable rejected script. Put every known independent correction in one recovery
payload, one operation per line:

```text
C2:abcd target 37:8c2f
C3:bcde target "return oldResult, nil"
C4:ef01 operation type+
C4:ef01 value "// inserted\n"
C7:2345 V9:6789 value "corrected body row"
C8:aaaa replace type 41:bbbb "replacement"
C3:cccc before in another.go
```

Use the current handles exactly. A re-rejection replaces the baseline and makes every handle
from the prior diagnostic stale. Resubmit a complete HPATCH/2 script only when the diagnostic
requires a new script or transaction. A malformed, stale, conflicting, or incomplete recovery
changes neither the workspace nor the retained rejected script. The standalone CLI and ordinary
hpatch grammar have no recovery mode.

## File reading, searching, inspection, and shell commands

Acquire target-bearing context once before editing. When a known identifier or literal is likely
to become a target, use hgrep first; use `-F` with repeated `-e` literals so punctuation cannot
create a regex error, and add `-A` or `-B` when a small amount of surrounding code is needed.
Every emitted match or context row is target-bearing. When the owner is known but the location
is not, use inspect_file for structure or hgrep for a symbol, then hread only the smallest range
needed to understand or replace the code. Avoid bare whole-file hread unless the complete file
is necessary. Use ordinary reads and searches only for read-only work or while the edit owner is
unknown.

Run one file per command as `hread PATH [START:END]`. Quote paths with shell syntax and batch
already-known reads as separate commands in one shell script. A bare path reads the complete
file. A start line of `0` begins at line 1 without emitting line 0. An end past EOF warns after
returning available rows; a start past EOF fails. Copy a current `LINE:HASH` directly into an
HPATCH/2 target.

Run hgrep with familiar ripgrep arguments and ordinary shell quoting, redirection, and
pipelines. Combine known patterns and paths with repeated `-e` arguments. Its output is
`"PATH":LINE:HASH TEXT`; copy a current target directly and never reconstruct a row.
Do not follow target-bearing hgrep output with hread unless nonmatching context outside the
requested bounds is needed.

Use `inspect_file PATH` for bounded metadata and structural outlines. Its line numbers are
metadata, not HPATCH targets; use hread when source text or target references are needed.
It returns one JSON envelope shaped as follows:

```text
success: {ok:true,data:{path,kind,language,size_bytes,line_count,parse_complete,outline},
          truncated,truncation}
failure: {ok:false,path,error:{code,message}}
outline entries: import, constant, variable, type, class, function, method, heading,
                 frontmatter, or JSON pointer records with one-based line metadata
```

Inspect_file never returns raw excerpts, bodies, field definitions, frontmatter values, or
JSON scalar values.
<!-- hpatch-model-instructions:end -->
