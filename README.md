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

Normal mode is silent on success and changes files only after the full script has been
parsed, evaluated, and staged:

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
type "hello"
type " world\n"
EOF
```

The second command writes an `Add File` patch to stdout. Successful translation keeps
stdout patch-only. Errors return nonzero and go to stderr; evaluation diagnostics
identify the command index, source line, operation, relevant path, and failure category.
Normal mode preserves existing file permission bits and creates files from `new` with
mode `0644`.

Translate mode emits LF-only logical-line patches. OpenAI `apply_patch` cannot preserve
CRLF bytes when such a patch is applied, so applying translated output to a CRLF file
may normalize that file to LF. Normal mode itself preserves existing line endings
outside text explicitly inserted by the script.

## Gain metrics

For each changing script, normal and translate modes record cumulative estimates of
the GPT-5 output tokens needed for the complete hpatch tool call and for the equivalent
direct `apply_patch` call:

```sh
bin/hpatch gain
```

hpatch v1 assumes the OpenAI `apply_patch` schema and an execution tool with a
native stdin field. The hpatch estimate counts the `functions.exec` tool name, a fixed
translate-and-apply orchestration template, `cmd: "hpatch translate"`, the serialized
stdin script, and the working directory. The direct estimate counts the `apply_patch`
tool name and patch envelope. The internally forwarded translated patch is not counted
again as model output.

These are reproducible estimates, not API billing totals. They exclude provider-hidden
protocol and reasoning tokens, assistant commentary, server-generated identifiers,
and tool results. Host formatting or a different host tool schema can change actual
usage.

The report writes estimated hpatch output tokens, estimated direct `apply_patch`
output tokens, and their estimated percentage reduction. With no current estimates,
all values are zero. Metrics persist in the platform user-configuration directory.
Counters produced by the earlier raw-script/raw-patch or shell-wrapper calculations
are not mixed with native-stdin estimates; they report zero until the next changing
invocation replaces them. Collection failures warn but do not prevent the requested
edit or translated output.

## Editing language

Run the built-in reference for stdin usage, process commands, and the complete
editing-command summary:

```sh
bin/hpatch --help
bin/hpatch translate --help
bin/hpatch --version
```

Commands execute sequentially against current in-memory content, including across
repeated `in` commands. `rsel` selects complete logical lines; linewise replacement
inherits the selected final line terminator unless the replacement supplies one.
`bsel "START" "END"` searches inside the current selection when one exists,
or from the current cursor to end-of-file otherwise. It never wraps. Each anchor must
be unique within that scope; ambiguous, reversed, or overlapping anchors fail.

Paths use normal host filesystem semantics during translation: relative paths resolve
from hpatch's current directory and absolute paths remain absolute. The translated text
contains paths but no current-directory metadata. A downstream patch tool independently
chooses its application root, so its root must be aligned with the paths hpatch emits.

The complete behavior and failure contract is in
[`doc/spec/interface.md`](doc/spec/interface.md).
[`AGENT_INSTRUCTIONS.md`](AGENT_INSTRUCTIONS.md) is the repository agent entry point
and directs agents to the built-in help.

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
