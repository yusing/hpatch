# hpatch

Apply compact, editor-like text and file operations from a command stream.

`hpatch` reads commands from standard input and commits their complete multi-file
result. `hpatch translate` reads the same commands but only prints an OpenAI
`apply_patch` envelope. `hpatch gain` reports cumulative estimated output-token usage and the
reduction achieved by hpatch.

## Build

Go 1.26 or newer is required.

```sh
go build -o bin/hpatch ./cmd/hpatch
```

## Quick start

Normal mode writes its final active-file state report to stderr on success and changes
files only after the full script has been parsed, evaluated, and staged:

```sh
bin/hpatch <<'EOF'
in src/app.go
tsel 12 -1 "oldName"
type "newName"
EOF
```

To inspect or forward the equivalent patch without changing files:

```sh
bin/hpatch translate <<'EOF'
new message.txt
type "hello world\n"
EOF
```

Use `--root` to select the trusted workspace boundary and `--cwd` to resolve
relative script paths from a directory beneath it:

```sh
bin/hpatch translate --root /workspace --cwd bin/block-graph-worktree <<'EOF'
in main.go
tsel 1 1 "package main"
type "package graph"
EOF
```

That command reads `/workspace/bin/block-graph-worktree/main.go` and emits the patch
path `bin/block-graph-worktree/main.go`. Apply the translated patch from `/workspace`.
Both options have CLI defaults: root is the process current directory and cwd is `.`.

The second command writes an `Add File` patch to stdout. Successful translation keeps
stdout patch-only and writes its pending final-state report to stderr. Errors return
nonzero and go to stderr; evaluation diagnostics identify the command index, source line,
operation, relevant path, and failure category. Normal mode preserves existing file
permission bits and creates files from `new` with mode `0644`.

Translate mode emits LF-only logical-line patches. OpenAI `apply_patch` cannot preserve
CRLF bytes when such a patch is applied, so applying translated output to a CRLF file
may normalize that file to LF. Normal mode itself preserves existing line endings
outside text explicitly inserted by the script.

## Gain metrics

Successful changing scripts in normal and translate modes record cumulative paired
estimates of the GPT-5 output tokens needed for the complete hpatch tool call and for the
equivalent direct `apply_patch` call. Failed normal or translate invocations record only
the hpatch call as ineffective output. A failure whose cause a direct `apply_patch` call
would also have hit (edit conflict, missing file, file conflict, path) credits the baseline
one mean effective payload, so shared retry cost is not charged to hpatch alone; selector,
anchor, and syntax failures earn no such credit because a context-hunk format does not make
them. Successfully emitted final-state reports add their exact estimated input-token
overhead to a separate counter. The same metrics store tracks invocations and command-caused
errors separately for every supported patch command, including successful no-op scripts,
and attributes each error to the command that raised it.

When a caller rebuilt the script on stdin from a shorter payload, such as a correction
that replaces named commands of a rejected script, set `HPATCH_CHARGED_SCRIPT` to that
payload. Evaluation still uses stdin; only output accounting uses the variable, so a
repair is not measured as costing the complete retry it replaced.

Tool definitions are model input too. Set `HPATCH_SESSION_ID` plus `HPATCH_TOOL_DEFINITION`
and hpatch counts its definition once per session rather than once per call, matching how
prompt caching bills it. Set `HPATCH_BASELINE_TOOL_DEFINITION` to the native patch tool
definition hpatch displaces and only the difference counts against hpatch. Without these,
definition counters stay zero and the report says so instead of implying the tool is free.

```sh
bin/hpatch gain
```

hpatch v1 estimates the current tool-call payloads. Both effective and ineffective
hpatch estimates count the `functions.hpatch` tool name followed by the free-form
editing script. The successful direct estimate counts the `functions.exec` tool name
and a free-form program that passes the serialized patch envelope to
`tools.apply_patch`. The patch is counted only as that nested tool's model-authored
input.

These are reproducible estimates, not API billing totals. They exclude provider-hidden
protocol and reasoning tokens, assistant commentary, server-generated identifiers,
and tool results. Host formatting or a different host tool schema can change actual
usage.

The report labels effective and ineffective hpatch output-token estimates separately
and shows their calculated total. Effective-only reduction compares against raw
`apply_patch` output; overall and weighted reductions compare against the baseline
including its credited retries, and weighted figures price the state report plus net tool
definition at five or six times output tokens. Stable tables separate aggregate command
errors, `sel`, `tsel`, and `rsel` selectors, single and multiple `tsel` spans, exact and
whitespace-recovered block selections, terminal failure reasons, and per-command failure
reasons. Percentages are zero when their denominator is zero. Metrics persist in the
platform user-configuration directory. Only the latest metrics format is decoded. A valid,
checksummed slot with another `HPATCH` version resets totals when no current-format slot
exists; malformed slots do not count as version mismatches. Collection failures warn but
do not change the success or failure of the requested edit or translated output.

## Editing language

Run the built-in reference for stdin usage, process commands, and the complete
editing-command summary:

```sh
bin/hpatch --help
bin/hpatch translate --help
bin/hpatch --tool-help
bin/hpatch --version
```

The first `in` of an existing file captures an immutable baseline. Every later
selector for that logical file resolves against that baseline, including after `mv`
or a repeated `in`; inserted text is not selectable. A selector overlapping baseline
content already replaced or deleted by an earlier command is rejected. Disjoint edits
are materialized together after validation, so independent selectors keep their
original meaning regardless of command order. Overlapping replacements or deletions,
insertions inside a replaced span, and multiple insertions at one baseline position fail
atomically. New files have an empty baseline and accept one effective complete-content
`type`.

`rsel` selects complete baseline logical lines; linewise replacement inherits the
selected final line terminator unless the replacement supplies one.
`bsel "START" "END"` searches the complete active-file baseline, independent of cursor
or selection. `bsel_next "START" "END"` explicitly searches inside the current baseline
selection when one exists, or from the current baseline cursor to end-of-file otherwise,
and never wraps. Each command resolves `START` uniquely in its scope, then resolves `END`
uniquely after that start. Exact anchors are authoritative; when an exact anchor is
missing, nonempty runs of ASCII spaces and tabs match interchangeably. Ambiguous,
reversed, or overlapping anchors fail.

Every editing invocation has one root. Relative script paths resolve from cwd within
that root. Absolute script paths must use the canonical root spelling and remain inside
it. Root escapes through `..`, absolute paths, or symlinks fail before staging.
Translation always emits root-relative paths, so its downstream patch consumer must
use the same root.

Library callers can pass an already-opened root capability directly:

```go
root, err := os.OpenRoot("/workspace")
if err != nil {
	return err
}
defer root.Close()

workspace := hpatch.Workspace{Root: root, CWD: "bin/block-graph-worktree"}
patch, err := hpatch.Translate(ctx, workspace, script)
```

`Workspace.CWD` is root-relative and defaults to `.`. The caller owns the `*os.Root`
and must keep it open for the operation. Hosts should canonicalize and authorize the
absolute root before opening it; the standalone CLI does this for `--root` and also
accepts an absolute `--cwd` only when it canonicalizes beneath root. Absolute script
operands must use that canonical root spelling; equivalent symlink aliases are not
resolved outside the root capability. Paths through absolute symlink targets are also
rejected by Go's `os.Root`, even when the target points back inside root. Prefer
root-relative script operands.

The complete behavior and failure contract is in
[`doc/spec/interface.md`](doc/spec/interface.md). Agents should read `hpatch --help`,
which is the authoritative editing reference.

## Token comparison

The comparison executable first proves that each independently authored hpatch and
`apply_patch` input produces the same path-to-content map. It then counts both inputs
with the Go tokenizer library's GPT-5 model mapping:

```sh
go run ./compare
```

The report includes the encoding selected for GPT-5, per-scenario token counts,
absolute savings, percentage reduction, and totals. Comparison runs isolate their
metrics and do not contribute to `hpatch gain`.

## Validation

```sh
go test ./...
go vet ./...
```
