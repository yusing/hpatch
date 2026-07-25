# hpatch

Apply compact, editor-like text and file operations from a command stream.

`hpatch` reads commands from standard input and commits their complete multi-file
result. `hpatch translate` reads the same commands but only prints an OpenAI
`apply_patch` envelope.

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

The second command writes an `Add File` patch to stdout. Errors go to stderr and return
a nonzero status. Normal mode preserves existing file permission bits and creates files
from `new` with mode `0644`.

Translate mode emits LF-only logical-line patches. OpenAI `apply_patch` cannot preserve
CRLF bytes when such a patch is applied, so applying translated output to a CRLF file
may normalize that file to LF. Normal mode itself preserves existing line endings
outside text explicitly inserted by the script.

## Commands

```text
in PATH
new PATH
mv PATH
rm
sel LINE START:END
tsel LINE OCCURRENCE "JSON STRING"
rsel START:END
type "JSON STRING"
del
dup
```

`in` and `new` select a file at the cursor position before its first character.
`type` inserts at the cursor or replaces a selection, then advances the cursor. Lines
and inclusive columns are one-based and count Unicode code points. String operands use
JSON syntax. Commands execute sequentially, including across repeated `in` commands.
Paths use normal host filesystem semantics: relative paths resolve from the current
directory and absolute paths remain absolute.

The complete behavior and failure contract is in
[`doc/spec/interface.md`](doc/spec/interface.md). A shorter instruction sheet for
coding agents is in [`AGENT_INSTRUCTIONS.md`](AGENT_INSTRUCTIONS.md).

## Token comparison

The comparison executable first proves that each independently authored hpatch and
`apply_patch` input produces the same path-to-content map. It then counts both inputs
with the Go tokenizer library's GPT-5 model mapping:

```sh
go run ./compare
```

The report includes the encoding selected for GPT-5, per-scenario token counts,
absolute savings, percentage reduction, and totals.

## Validation

```sh
go test ./...
go vet ./...
```
