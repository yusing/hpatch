<!-- hpatch-model-instructions:start -->
## CTP/2 transport

CTP/2 is an inline representation used in some model-visible strings. Decode it while reading, then
continue the task's ordinary workflow. CTP itself requires no inspection or tool call. Strings
without one of the exact prefixes below are already native, including all CTP/1 text.

A content-local dictionary and its reference body occupy one string:

```text
!ctp2 D
0="an exact repeated string"
END
!ctp2 R
Reuse @{0} and @{0}.
```

Each `ID=VALUE` line defines one exact nonrecursive JSON string under a lowercase base-36 `ID`.
`END` closes the dictionary. Expand `@{ID}` in the following `!ctp2 R` body; `@@{ID}` is literal
`@{ID}`, and every other `@` is literal. The dictionary is local to that one string and is not
inherited by another string.

A visible-line representation may reuse exact lines from preceding custom-tool or function outputs
in the current request:

```text
!V=7fa,12,3
+"literal tail"
```

Each newline-terminated operation after `!V` appends text in order. `=SUFFIX,START,COUNT` appends
`COUNT` exact lines beginning at one-based line `START` from the one preceding tool output whose
call ID, or `call-ID/part-index` for multipart output, uniquely ends with `SUFFIX` at that point.
`+JSON_STRING` appends its exact JSON string value. Resolve references only against earlier visible
tool outputs; compaction removes sources that are no longer visible.

`!ctp2 L` plus a line feed starts literal text and removes only that tag. Use it when native text
begins with `!ctp2 D`, `!ctp2 R`, `!ctp2 L`, or `!V`. Every decoded byte is final text, including
leading, trailing, and final line feeds.

Emit novel assistant prose natively. When clearly smaller, assistant text may use one content-local
dictionary or visible-line references to preceding tool outputs. Emit CTP syntax only in assistant
text. Newly emitted tool names, tool inputs, and function arguments are literal native final bytes. In
`functions.shell`, omit `workdir` so execution uses the current workspace; a necessary override is
a fully expanded existing absolute path, never a reference or placeholder.

{{.EditingWorkflow}}

## Shell reference

The default interpreter is Bash. For another interpreter, put its command and arguments in a
compact shebang on the first line, such as `#!uv run python` or
`#!node --experimental-strip-types`. Use a direct command or path rather than `/usr/bin/env`.
Selectors named `bash` or ending in `/bash` use the embedded Bash evaluator; selectors named
`sh` or ending in `/sh` use its POSIX evaluator.
Omit Bash shebangs, and pass the script directly instead of wrapping it in Bash, `-c` or `-e`
command-string quoting, or a heredoc.

Optional `#!key=value` directives follow the interpreter shebang or appear first. `#!cmd=`
accepts exactly one `{.}` placeholder, which expands to the normalized shell helper command
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
add DESTINATION VALUE
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
terminators owned by line and range targets. `add` inserts before a line or text destination;
`add EOF` appends. Ranges are not add destinations. A text target defaults to one match; every
requested non-overlapping match must exist or the script rejects.
When exact known target text spans logical lines or includes a trailing LF, encode that LF as
`\n` (or an equivalent `\u000A`) inside the quoted anchored or unanchored target. Keep the target
on one physical command line. Literal tab is accepted; carriage returns and other controls are
not.
Before writing `N > 1`, count the exact literal occurrences in the acquired immutable baseline;
never infer `N` from the number of intended replacements. If the count is not already visible,
use hgrep or separate verified row anchors instead of guessing it.

Use inline JSON-compatible strings for short or single-line values. Include `\n` when an
insertion must form a complete new line:

```text
in parser.go
add 37:8c2f "// parseCommand parses one physical script line.\n"
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

An unindented heredoc body line that begins with `type ` or `add ` and then
contains only `<<PATCH` or ends with ` <<PATCH` is reserved as a nested opener. Close the
current frame first; use an inline value or indent literal HPATCH examples.

Existing-file edits require a target. Targetless `type VALUE` is valid only immediately after
`new`; create a file with at most one such initializer:

```text
new internal/target.go
type "package internal\n"
```

Every existing file has one immutable baseline for the complete invocation. Pending edits
do not shift later targets. Preserve required indentation prefixes in indentation-sensitive
languages such as Python.

Content introduced by a mutation is not targetable in the same call. After every successful
invocation, unchanged saved rows remain valid even when edits shifted their line numbers: hpatch
relocates an exact hash only when it identifies one row. For a routed whole-line or range
replacement, the router resolves that exact pre-edit target after the executor confirms
application.

Nonempty line and range `type` replacements preserve the target's final LF, CRLF, or CR
when the value omits a terminator. Explicit terminators are authoritative. An empty
target-bearing `type` value removes owned terminators. `add` inserts byte-exact values and
does not synthesize newlines.

Overlapping replacements or deletions and insertions strictly inside them reject. Boundary
insertions are valid. Multiple insertions at the same boundary render in script order.

Changed Go files are parsed and formatted before success. Supported Python, JavaScript, and
TypeScript files are syntax-checked when Tree-sitter support is available; supported indentation
corrections are automatic. Relative paths use the selected base directory when available; without
one, relative paths reject; parents for `new` or `mv` must exist. A wholly row-stale routed
rejection lists current `C...` command handles. Correct
only those targets with `functions.hpatch_recover`, one handle and ordinary HPATCH/2 target per
line:

```text
C2:abcd 37:8c2f
C3:bcde "return oldResult, nil"
```

Put every listed correction in one payload and use the current handles exactly. Recovery changes
targets only; it preserves operations, values, command order, and file context before reevaluating
the complete script. Each corrected target must differ from its rejected target; equivalent
spellings of the same target reject before reevaluation. A re-rejection replaces the baseline and makes every earlier handle stale.
Use one complete HPATCH/2 script for every non-target or mixed correction. A malformed, stale,
conflicting, or incomplete recovery changes neither the workspace nor the retained rejected
script. Ordinary `functions.hpatch` and root APIs have no recovery mode.

## Reading and inspection reference

Run one file per command as `hread PATH [START:END]`. Quote paths with shell syntax and batch
already-known reads as separate commands in one shell script. A bare path reads the complete
file. A start line of `0` begins at line 1 without emitting line 0. An end past EOF warns after
returning available rows; a start past EOF fails. Copy a current `LINE:HASH` directly into an
HPATCH/2 target. If hread reports an incomplete token-limited result, retain the emitted rows and
request a smaller range for the missing context.

Run hgrep with familiar ripgrep arguments and ordinary shell quoting, redirection, and
pipelines. Combine known patterns and paths with repeated `-e` arguments. Its output is
`"PATH":LINE:HASH TEXT`; copy a current target directly and never reconstruct a row.
Do not follow target-bearing hgrep output with hread unless nonmatching context outside the
requested bounds is needed. If hgrep reports an incomplete token-limited result, retain the
emitted rows and narrow the patterns, paths, context, or file selection.

For Go, JavaScript, TypeScript, JSON, and Python, use
`hsymbol refs PATH LINE:HASH SYMBOL [N]` when an exact symbol must be renamed, audited, or changed
at every reference. Use `hsymbol def PATH LINE:HASH SYMBOL [N]` when the next edit is the symbol's
declaration. `N` counts exact language tokens on the verified input line and may be omitted only
when one exists. Copy emitted `"PATH":LINE:HASH TEXT` rows directly
into HPATCH/2 targets. Do not follow a complete hsymbol definition with hread of the same span
unless non-declaration context is needed. Never treat an incomplete token-limited hsymbol result
as a complete definition or reference set.

Use `inspect_file PATH` for bounded metadata and a structural outline. Each outline entry's
`line` and `line_end` are copyable `LINE:HASH` identities for that inclusive span. Copy a
single-line span as a row target and a multi-line span as `line..line_end` with no spaces.
Inspect_file never returns source bodies; use hread only when replacement needs unseen text
rather than to obtain the target.
It returns one JSON envelope shaped as follows:

```text
success: {ok:true,data:{path,kind,language,size_bytes,line_count,parse_complete,outline},
          truncated,truncation}
failure: {ok:false,path,error:{code,message}}
outline entries: import, constant, variable, type, class, function, method, heading,
                 frontmatter, or JSON pointer records with LINE:HASH span identities
```

Inspect_file never returns raw excerpts, bodies, field definitions, frontmatter values, or
JSON scalar values.
<!-- hpatch-model-instructions:end -->
