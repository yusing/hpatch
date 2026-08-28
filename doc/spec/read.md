# Shell-routed verified-row reader

## REQ-READ-001 — Shell-routed verified-row reader

In hpatch router mode, the model receives `hpatch` and `shell` as standalone custom tools.
All persistent hread, hgrep, hsymbol, inspect_file, shell-execution, and HPATCH workflow guidance comes
from `contrib/codex/file-editing-instructions.md`. The router injects a protocol-specific projection
of that source into the top-level Responses `instructions` value in memory and never changes an
instruction file. Native model protocol omits the leading CTP/2 section and stops after the ordinary
guidance and tool rewrite. CTP/2 injects the complete source, preserves the selected top-level or
first textual developer-message carrier, and transforms eligible strings under `REQ-CTP-001`.
Hread, hgrep, hsymbol, and inspect_file remain private executor contributions inside the
authenticated shell worker; their custom-tool specifications are not sent to the model, direct
model calls to their names are not routed, and no executable frontend is installed for them.

The private `hread` command accepts exactly one file:

```text
hread PATH [START:END]
```

The shell owns quoting and argument separation. A path containing whitespace is therefore one
ordinary quoted shell argument. `START:END`, when present, is an inclusive logical-line range
whose positive one-based base-ten endpoints must be ordered. The start line must exist. An end
past EOF returns through the final line. One `hread` invocation never accepts a second path or a
newline-delimited batch. The model batches related reads as separate hread commands in one
shell script.

The shell carrier invokes the fixed `shell` helper from the executor's trusted `PATH`. The
router stores the current authenticated shell worker at
`$HPATCH_RUNTIME_DIR/hpatch-$CODEX_THREAD_ID/.runtime`; the helper reads that path and replaces
itself with the worker. Its `mvdan/sh` Bash and POSIX evaluators intercept the exact command
names `hread`, `hgrep`, `hsymbol`, and `inspect_file` after ordinary shell expansion, then call the matching
immutable snapshot implementation directly. These private names are not filesystem entries and
do not use `PATH`. A deployment that isolates router and executor filesystems must expose the
thread runtime path, router executable, and authenticated snapshot at the same
absolute paths, separately from the user workspace.

Hread runs in the shell carrier's actual working directory. Relative and absolute paths keep
their ordinary process meaning. Codex, not the router or hread, owns sandbox and filesystem
permissions. The worker accepts only regular UTF-8 files and never mutates them. It emits only
the requested logical lines:

```text
LINE:HASH TEXT
```

`LINE` is the positive one-based logical line number. `TEXT` is exact logical-line content
without its terminator. `HASH` is lowercase hexadecimal for the first two bytes of SHA-256
over that exact content, including leading spaces and tabs. A trailing file terminator does
not create an additional empty line. Missing, inaccessible, non-regular, non-UTF-8,
reversed-range, and start-past-EOF reads return concise stderr and nonzero status.

Verified-row commands count exact formatted current stdout with the GPT-5 tokenizer. They admit
rows through 15,000 tokens. One next complete row may raise the result to at most 15,500 tokens;
admitting that row seals the result. EOF at that point is complete. A later row, or any row that
would exceed 15,500 tokens, is omitted together with every later row. Omission preserves already
admitted complete rows on stdout, writes an incomplete-result diagnostic to stderr, and returns
nonzero. It never cuts a row.

For input metrics, hread produces its current and stock results from the same read. The stock
result preserves selected `TEXT` and one LF per returned logical line while omitting the
`LINE:HASH ` prefix. The comparison does not read a file twice.

Acceptance:

1. A whole-file or bounded read emits exact UTF-8 rows. Equal lines at different positions
   have distinct row references, and indentation changes the hash.
2. `hread PATH`, `hread PATH START:END`, and a shell-quoted path containing whitespace work.
   Extra path or range arguments fail instead of being interpreted as a batch.
3. Several hread commands in one shell call execute in authored shell order without an
   hread-owned batch format, buffer, header, or partial-success policy.
4. Reading and whole-file UTF-8 validation use bounded streaming storage and observe
   cancellation. Token-limited output retains only admitted complete rows, and current and stock
   results retain the same source rows without a second read.
5. Success and failure reach Codex through the model-visible shell carrier. Replay retains
   the original shell call and output; it never synthesizes a model-visible hread call or
   includes the shell call in editable rejected-script recovery history.
6. Router startup validates hread inside the immutable built-in snapshot without installing a
   frontend. Passthrough mode loads and exposes none of these replacement surfaces.
