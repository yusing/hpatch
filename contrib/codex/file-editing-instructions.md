<!-- mekugi-model-instructions:start -->
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
text. Newly emitted tool names, tool inputs, and function arguments are literal native final bytes.

{{.EditingWorkflow}}

## Commentary

Attach progress commentary only to a supported tool call, using that tool's `commentary` field or
documented runtime commentary mechanism. When no available tool supports commentary, continue
without a commentary message. Never emit a standalone assistant message with
`phase: "commentary"`; standalone commentary messages are router-owned.
A blocking question or final result can still use the final channel. Do not wake solely to emit
a progress notice.

## Tool coordination

Use the Shell reference's batching guidance for ready reads and searches; keep output bounded.
Parallelize other independent calls only when their tool contracts allow it; run hpatch
alone and wait for its result before another tool call. Use only the tools exposed for this request.

## Shell reference

Submit free-form programs to `functions.shell`. Choose each interpreter before writing its body:

- Bash: write commands directly, without a shebang.
- Another interpreter: put `#!COMMAND [ARGS...]` on the first line, then write that interpreter's
  program directly below it. Use a direct command or path rather than `/usr/bin/env`.
  Examples include `#!python3`, `#!ruby`, `#!node`, `#!uv run python`, and
  `#!node --experimental-strip-types`; interpreter selection is not limited to these examples.

For Python, submit the contents of this example without the Markdown fence:

```python
#!python3
values = [2, 3, 5]
print(sum(values))
```

Every body line is program source for the selected interpreter. Submit it directly, not through
an interpreter command with a quoted program argument or a shell heredoc such as `python3 - <<'PY'`.
There is no closing delimiter. Interpreter flags belong in the selector, not around the program
body. Selectors named `bash` or ending in `/bash` use the embedded Bash evaluator;
`sh` or a path ending in `/sh` selects its POSIX evaluator.

HPATCH's `<<PATCH` is a multiline edit-value form used inside `functions.hpatch`, not a shell
submission wrapper. Shell redirections and heredocs that supply command data remain shell syntax;
they are not the way to submit an interpreter's program.

### Execution options and program input

The input order is: optional interpreter selector, optional directive lines, then program source.
Without a selector, the body is Bash. Put directives together before the body; `#!cmd=` and
`#!params=` may appear in either order, at most once each per program.

- `#!params=<JSON object>` supplies the request-specific execution fields listed in the tool
  description. The body supplies `cmd`, so omit that field; if setting `login`, use `false`.
  Omit `workdir` to use the current workspace. A necessary override must be a fully expanded
  existing absolute path, never a reference or placeholder.
- `#!cmd=` accepts exactly one `{.}` placeholder, which expands to the script runner invocation.
  Use it to connect a producer to the program's standard input, independently of its source body.


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

### Batching

With Code Mode available, prefer one batch for ready, independent, noninteractive programs.
Use separate calls when a result determines the next program or whether it should run.

Start a batch with `#!batch=SEPARATOR`, choosing a nonempty separator line absent
from every program's source and without surrounding whitespace. Put that exact line
between programs, with no leading or closing separator. At least two programs need
nonempty bodies. Each program has its own optional interpreter and directive block;
a params-only header selects Bash.


```text
#!batch=NEXT_PROGRAM
#!params={"yield_time_ms":1000}
echo hello
NEXT_PROGRAM
#!python3
print("hello")
NEXT_PROGRAM
#!params={"yield_time_ms":2000}
echo goodbye
```

Omitted params inherit the previous complete object; an explicit object replaces it, and `{}`
clears it. Interpreters and command templates never inherit. Each program starts a separate
execution, so shell variables and `cd` changes do not carry over. Only the chosen separator
line is reserved; without a batch header, selector-like body lines stay native source.

Programs run sequentially, each finishing before the next starts. `#!batch=SEPARATOR`
continues after nonzero exits; `#!batch-stop=SEPARATOR` leaves later programs unstarted
after a nonzero terminal exit. Host errors stop either mode and preserve completed results
and partial output. Use separate shell calls for interactive programs.
Native-only clients reject batches; submit separate calls there.

### Results, continuation, and retry

A runtime failure may leave earlier statements' effects in place. Inspect affected state before
retrying; a failed call does not imply rollback.

Retained scripts are thread-private and expire at the reported deadline or earlier on
router shutdown. Reads and edits do not renew them. Save durable source in workspace files.

A retained result includes `retained: true` and a `script_ref`. Read the source with
`hcat @shell/<reference>`, edit it with hpatch, or rerun its current content with a shell call
containing only `#!script=@shell/<reference>`. A HPATCH script using an `@shell/` path must use
only `@shell/` paths; never mix retained scripts and workspace files in one HPATCH script.

When you need a pending execution's result, resume its latest outstanding handle using the
`continuation` notice's `next_call`. Prefer host completion notifications when available.
A running outer Code Mode cell owns continuation; use its `wait`, not its inner session.
Resubmitting shell source starts a new execution. A null `next_call` means the host capability
is unavailable; use native session facilities for interactive input or termination.

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

For multiline or escape-heavy values, choose the final-newline behavior explicitly:
`<<PATCH` keeps every body terminator; `<<PATCH-` removes exactly the final body terminator.
Both close with `PATCH` and preserve all other body bytes, including spaces and earlier blank lines.

For protocol examples or other payloads containing delimiter or opener lines, use
`<<TEXT` (keep final terminator) or `<<TEXT-` (remove one final terminator).
Prefix every payload line with one `|`, including blank lines; close with unprefixed
`TEXT`. Only the first bar is removed. Literal `PATCH`, `TEXT`, and nested examples
are safe payloads without quote or backslash escaping:

```text
type "old example" <<TEXT-
|type <<PATCH
|replacement
|PATCH
|type <<TEXT-
||text
|TEXT
TEXT
```

Use row/range targets with `<<PATCH` for whole-line replacements:

```text
in service.go
type 20:2ff7..28:d10b <<PATCH
func calculateResult(input Input) (Result, error) {
	return computeFreshResult(input), nil
}
PATCH
```

For a literal replacement that should keep the existing following newline or inline suffix,
use `<<PATCH-` (or an inline value without a final newline):

```text
in notes.md
type "old paragraph" <<PATCH-
first replacement line
last replacement line
PATCH
```

Literal targets own only their matched bytes. To delete a whole line, use a row target or
include its terminator in the literal target; deleting text alone leaves the line terminator.
For insertions, count separators already at the destination and include only the missing ones.
Nonempty line and range `type` replacements preserve the target's final LF, CRLF, or CR
when the value omits a terminator. Explicit terminators are authoritative.
`add` inserts byte-exact values and does not synthesize newlines.
A chomped body with only one empty line decodes to empty,
so replacing a row with it deletes the row rather than making it blank.
Authored spaces and blank lines are preserved except for language-aware formatting and
indentation correction.

Successful `advisory` lines describe authored whitespace boundaries before neighboring
edits or formatting; they are not errors. Check those boundaries against your intent.

An unindented heredoc body line beginning with `type ` or `add ` and ending with either
heredoc marker is reserved as a nested opener when the marker is its sole operand or follows
a space. Use a line-framed text block for literal HPATCH examples.

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
invocation, unchanged saved rows remain valid even when edits shifted their line numbers: mekugi
relocates an exact hash only when it identifies one row. For a routed whole-line or range
replacement, the router resolves that exact pre-edit target after the executor confirms
application.

Overlapping replacements or deletions and insertions strictly inside them reject. Boundary
insertions are valid. Multiple insertions at the same boundary render in script order.

Changed Go files are parsed and formatted before success. Supported Python, JavaScript, and
TypeScript files are syntax-checked when Tree-sitter support is available; supported indentation
corrections are automatic. Relative paths use the selected base directory when available; without
one, relative paths reject; parents for `new` or `mv` must exist.

### Rejected-script recovery

Use `functions.hpatch_recover` to repair the latest retained rejected script, preserving
unrelated prepared edits. Choose one payload form:

- For a wholly row-stale rejection, supply every listed current `C...` handle followed by
  its corrected ordinary HPATCH/2 target.
- For values, framing, paths, conflicting commands, or mixed corrections, use ordinary
  target-bearing `type`/`add` mutations against retained-script text.

Target-only example:

```text
C3:bcde0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab "return oldResult, nil"
```

Put every listed target correction in one payload, copying current handles exactly
and supplying a different target for each.

Script-text mutations edit the retained rejected script, not workspace files. Use the diagnostic's
verified script rows or exact known literals; omit `in`, `new`, `mv`, and `rm`.
For example, `type "bad value" "fixed value"` changes that exact retained text.
Use ordinary value framing and keep the two payload forms separate.

Both forms preserve untargeted script text and reevaluate the complete script atomically.
A re-rejection becomes the next baseline: use its script rows and refreshed command handles.
Invalid corrections leave the workspace and retained baseline unchanged.

## Reading and inspection reference

For ordinary file reads, use `cat` or bounded `sed`. Prefer `hcat` when its verified row
identities are useful for an anticipated edit.

Run one file per command as `hcat PATH [START:END]`. Quote paths with shell syntax and batch
already-known reads as separate commands in one shell script. A bare path reads the complete
file. A start line of `0` begins at line 1 without emitting line 0. An end past EOF warns after
returning available rows; a start past EOF fails. Copy a current `LINE:HASH` directly into an
HPATCH/2 target. If hcat reports an incomplete token-limited result, retain the emitted rows and
request a smaller range for the missing context.

Run hgrep with familiar ripgrep arguments and ordinary shell quoting, redirection, and
pipelines. Combine known patterns and paths with repeated `-e` arguments. Its output is
`"PATH":LINE:HASH TEXT`; copy a current target directly and never reconstruct a row.
Do not follow target-bearing hgrep output with hcat unless nonmatching context outside the
requested bounds is needed. If hgrep reports an incomplete token-limited result, retain the
emitted rows and narrow the patterns, paths, context, or file selection.

Both readers accept leading `--max-tokens N` (1–15500) for a strict stdout token
ceiling and `--preview-bytes N` (1–65536) for long-line inspection. For example,
`hgrep --max-tokens 2000 --preview-bytes 160 -F needle source.ts`.
Preview JSON includes a full-source row identity and an explicit UTF-8 prefix with
omitted-byte counts. Retain the identity, but obtain missing content before using the
preview as literal target text. Budget omissions still report incomplete results.

For Go, JavaScript, TypeScript, JSON, and Python, use
`hsymbol refs PATH LINE SYMBOL [N]` for semantic references or
`hsymbol def PATH LINE SYMBOL [N]` for definitions. Supply an already-known
`LINE:HASH` instead of `LINE` to enforce a prior read; plain lines query the
current snapshot without requiring a preliminary verified read.
A leading `--workspace ROOT` chooses resolver scope and relative input paths;
its result paths are absolute. Other results are workspace-relative. `N` counts exact
language tokens on the selected line and may be omitted only when one exists. Copy emitted `"PATH":LINE:HASH TEXT` rows directly
into HPATCH/2 targets. Do not follow a complete hsymbol definition with hcat of the same span
unless non-declaration context is needed. Never treat an incomplete token-limited hsymbol result
as a complete definition or reference set.

Use `inspect_file PATH` for bounded metadata and a structural outline. Each outline entry's
`line` and `line_end` are copyable `LINE:HASH` identities for that inclusive span. Copy a
single-line span as a row target and a multi-line span as `line..line_end` with no spaces.
To obtain a known declaration or value in the same call, use
`inspect_file --source NAME PATH` with an exact name or JSON pointer (empty for
the JSON root). All matches include source; `--source-bytes N` sets its per-entry UTF-8
prefix bound (1–8192, default 8192). Default inspection remains outline-only.
Paths are absolute or working-directory-relative, like hcat.

Selected source is exact syntax, not a decoded JSON/YAML value. Check both per-entry
`omitted_bytes` and envelope `truncated` before treating it as complete. Use hcat
only for still-missing context, not to reacquire identities already supplied.
<!-- mekugi-model-instructions:end -->
