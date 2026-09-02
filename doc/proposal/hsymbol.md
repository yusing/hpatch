# Private `hsymbol` frontend

Status: implemented. `REQ-SYMBOL-001` owns the normative interface.

## Evidence

Recent non-hpatch long-running sessions (Codex and Claude, 1 August 2026 onward) show a
repeated acquire loop that `hread`, `hgrep`, and `inspect_file` do not close.

- One polymarket-ev-daemon Codex session issued `hread daemon.go` 57 times, `health.go` 60
  times, and `daemon_test.go` 69 times, plus 240 `hgrep` calls. The names were unique enough
  to search, but each textual hit still forced a later range read of the same files.
- A godoxy OIDC session called `gopls definition` once and `gopls references` five times
  with `PATH:LINE:COL` positions (`internal/auth/oidc.go`, `provider.go`, `auth.go`). Those
  calls are the language-server operation `hgrep` cannot express: this identifier, not every
  spelling of the name.
- `inspect_file` ran 7 times across the same long-session sample that ran `hread` 1009 times.
  Its outlines now give copyable `LINE:HASH` span targets for file-local declarations, but
  they do not distinguish semantic identifiers or find callers and cross-file references.

The local failure is not “the agent forgot a `gopls` flag”. `gopls definition` and
`gopls references` already return `file:line:col`, while `inspect_file` owns structural
navigation within one file. The missing product is language-server resolution rendered as
verified complete source rows for exact definitions and references.

## Why this is not `hgrep` or `gopls`

| Stock path | What it already does | What it does not do |
| --- | --- | --- |
| `hgrep -e Name --type go` | Text matches as verified rows | Distinguishes definition from mention, or two methods with the same name |
| `inspect_file PATH` | File-local outline with copyable span targets | Callers, cross-file refs, or source rows |
| `gopls definition` / `gopls references` | Precise identifier locations | `LINE:HASH` text; the agent still `hread`s the body |

`hsymbol` is therefore the `hgrep` shape applied to a language server: the server remains
the resolver; the frontend is the only row emitter. A wrapper that reprints `file:line:col`
would fail this test the way `hlog` failed it.

## Owner

Builtin private frontend, same family as `hread` / `hgrep` / `inspect_file`.

| Piece | Owner |
| --- | --- |
| Declaration, argv, executor | `plugins/` (`hsymbol.ts` plus `tools.ts`) |
| Bundle and frontend symlink | `internal/router/toolplugin` and router startup |
| Verified-row identity | Existing `LINE:HASH` helper used by `hread` / `hgrep` |
| Language resolution | Installed `gopls`, TypeScript 7 `tsc`, or `pyright-langserver` on the Codex executor `PATH` |
| Model-visible catalog | Unchanged: still `functions.hpatch` and `functions.shell` |
| Guidance | `contrib/codex/file-editing-instructions.md` |
| Recovery ancestry | None. Ordinary plugins do not enter `REQ-CORRECT-001` |

Passthrough mode installs nothing. Hsymbol selects gopls for Go, the native TypeScript 7 LSP for
JavaScript, TypeScript, and JSON, and Pyright for Python.

## Command

Private executable `hsymbol`, invoked only through `functions.shell`. One invocation, one
query. Shell quoting owns path whitespace. Batch several queries as separate `hsymbol`
commands in one shell script, the same way `hread` batches.

```text
hsymbol def PATH LINE:HASH SYMBOL [N]
hsymbol refs PATH LINE:HASH SYMBOL [N]
```

`PATH` is one existing regular UTF-8 supported source file. Relative and absolute inputs resolve from the
canonical shell process cwd, and their canonical target must remain inside that workspace.
`LINE:HASH` is `REQ-READ-001` identity for one current logical line of that file. Lookup is
exact: a stale or missing hash fails and does not search for another match. `SYMBOL` matches one
exact language token on that verified line; comments, unrelated literal text, and larger
identifiers do not count. `N` is a positive token-occurrence
count. It may be omitted only when exactly one matching token exists. Multiple matches without
`N` fail as ambiguous before the resolver starts.

This selector is intentionally stricter than `hgrep`. Text search is allowed to return comments,
strings, and substrings because its result is a set of textual matches. A language-server query
must identify one token. Refusing an ambiguous omitted `N` prevents the frontend from silently
asking the resolver about a different same-spelled symbol on the line.

`def` and `refs` are the only modes. Extra arguments, missing arguments, unknown modes, and
a zero, leading-zero, or non-decimal `N` fail before the resolver starts.

## Output

Stdout contains only complete verified rows, each terminated by LF, in first-seen order. It uses
the shared verified-row GPT-5 token admission rule in `REQ-READ-001`: rows are admitted through
15,000 tokens, one complete next row may raise the result through 15,500 and seal it, and a later
row is omitted with every row after it. An incomplete result retains admitted rows, writes its
diagnostic to stderr, and returns nonzero.
An incomplete result is not resumable and does not establish a complete definition or reference
set.

Row shape is the `hgrep` row:

```text
"PATH":LINE:HASH TEXT
```

`PATH` is the JSON-quoted path from the canonical workspace root to the canonical result file,
without a leading `./`. `LINE:HASH TEXT` has the identity and complete-line semantics of
`REQ-READ-001`. No match highlighting or identifier-only fragments.

`def` expands only when the definition selection returned by the resolver is the declared name of
an existing complete inspect_file outline entry. Supported variables, types, classes, functions,
and direct methods emit that individual entry's inclusive range. Imports, fields, parameters,
locals, JSON, and uncertain parses emit only the definition line.

`refs` asks the resolver to include declarations and emits one row per returned reference location.
Several references on one canonical path and line emit that row once.

Rows whose canonical file escapes the workspace, is not a regular UTF-8 file, or is not owned by
the selected resolver are
omitted from stdout. Stderr reports how many locations were skipped and why. A `def` without an
editable workspace location fails.

Empty `refs` is a successful empty stdout, matching `hgrep` with no matches.

Stderr may include resolver diagnostics and skip counts. It never carries substitute rows.
Progress is not required: one semantic query is short.

Exit status is zero when the query ran and stdout holds the rows described above, including
the empty-`refs` case. Nonzero status is for usage, missing file, stale hash, missing
`SYMBOL` occurrence, missing resolver, cancellation, and workspace-escaping `def`.

## Stock metrics

The executor runs one resolver query. The current result is the verified-row stdout. The stock
result is gopls output or the LSP semantic response and resolver stderr from the same position.
Metrics do not start a second query.

## Non-goals

- Model-visible `functions.hsymbol`. Keep it private, like `hgrep`.
- A grep fallback when a resolver is missing. That is `hgrep`.
- Workspace-wide search by name alone. Start from a verified row.
- Implementations, hover, rename, or completion.
- Mixing symbol rows and hpatch recovery.

## Guidance

The router-injected `contrib/codex/file-editing-instructions.md` guidance teaches agents to:

- After `hgrep` has a current row for an identifier that must be renamed, audited, or
  replaced at every call site, use `hsymbol refs` on that row instead of repeating `hgrep`
  and whole-file `hread`.
- Use `hsymbol def` when the next edit is the declaration body and the current row is only
  a use or signature line.
- Copy emitted rows directly into HPATCH/2 targets. Do not reconstruct hashes.
- Do not follow `hsymbol def` with `hread` of the same span unless non-declaration context
  is required.

The tool description stays behavioral: modes, operands, row shape, and failure cases. No
workflow instructions in the specification string.

## Acceptance

1. `hsymbol def` on a verified use-site row of a supported top-level declaration emits the complete
   current declaration as `hgrep`-shaped rows whose hashes match `hread` of that span.
2. `hsymbol refs` on a verified language-token row emits each workspace reference once as a
   complete current line. A textual `hgrep` of the same name may return additional
   non-identifier hits; those extra hits are not a `refs` failure.
3. A stale `LINE:HASH`, a missing occurrence `N`, or a missing resolver fails before any
   stdout rows. The command does not search for another hash.
4. Standard-library or module-cache locations are skipped with a stderr count. A `def`
   that resolves only there is nonzero and emits no rows.
5. The model-visible shell call is replayed unchanged. No standalone `hsymbol` call is
   routed or admitted to rejected-script recovery. Passthrough mode installs no frontend.

## Validation

- Plugin unit tests next to `plugins/hsymbol.ts` for argv, stale-hash, occurrence, protocol,
  format coverage, skip, and token-admission behavior, using fake resolvers plus pinned real
  TypeScript and Pyright integration fixtures.
- Router registry tests that the builtin bundle exposes the private frontend and does not
  add a model-visible tool.
- One focused `go test ./internal/router` plus `bun test` for the plugin package after
  `go generate ./internal/router/toolplugin`.

## Complexity gate

The owner is the builtin private-tool bundle. The local failure is a precise identifier
query whose stock CLI result cannot be copied into HPATCH/2, which then causes repeated
`hread` of the same files. The protection is one resolver-backed frontend that reuses
`REQ-READ-001` row identity. It does not add a second edit engine, a model-visible tool, or
a search fallback.
