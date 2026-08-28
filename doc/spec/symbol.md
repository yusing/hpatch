# Shell-routed semantic symbol lookup

## REQ-SYMBOL-001 — Shell-routed semantic symbol lookup

The private `hsymbol` command is available only through the model-visible shell tool:

```text
hsymbol def PATH LINE:HASH SYMBOL [N]
hsymbol refs PATH LINE:HASH SYMBOL [N]
```

The canonical workspace is `realpath(process.cwd())`. `PATH` may be relative or absolute, but
its canonical target must remain within that workspace and be a regular UTF-8 supported source
file. Supported sources are Go `.go`; Python `.py` and `.pyi`; JSON `.json`; and the stable TypeScript 7
formats `.ts`, `.tsx`, `.d.ts`, `.mts`, `.d.mts`, `.cts`, `.d.cts`, `.js`, `.jsx`, `.mjs`, and
`.cjs`.
`LINE:HASH` identifies one current logical line under `REQ-READ-001`. Hsymbol verifies the line
and hash before starting a resolver and never searches for another matching hash.

`SYMBOL` selects an exact language token on the verified line. Go accepts non-keyword identifiers;
JavaScript and TypeScript accept their identifier, property, private-name, type-name, and JSX-name
tokens; Python accepts identifier and property tokens; JSON accepts a decoded property-name or
string token. Comments, larger identifiers, and unrelated literal text do not count. `N`, when
present, is a positive base-ten occurrence without leading zeroes. When `N` is absent, exactly
one matching token must exist; multiple matches fail as ambiguous before the resolver starts.

Each invocation starts exactly one semantic query at the selected token. Go uses one
`gopls definition -json` or `gopls references -d` query at a UTF-8 byte offset. JavaScript,
TypeScript, and JSON start `tsc --lsp --stdio`; Python starts `pyright-langserver --stdio`. The LSP
client initializes the canonical workspace, opens the verified source snapshot, negotiates UTF-16
positions, sends one `textDocument/definition` or `textDocument/references` request, and reaps the
server after the response. References request `includeDeclaration: true`. There is no text-search
fallback. A missing resolver, invalid arguments, stale rows, invalid selectors, malformed protocol
result, or failed semantic query returns concise stderr and nonzero status without useful stdout.

Successful stdout contains first-seen complete verified rows:

```text
"PATH":LINE:HASH TEXT
```

`PATH` is the JSON-quoted path from the canonical workspace root to the canonical result file,
without a leading `./`. Each result file is canonical, in-workspace, regular, UTF-8, and owned by
the selected resolver; other returned locations are omitted and counted by reason on stderr.
References are deduplicated by canonical path and logical line. Empty `refs` is successful.
A `def` without an editable workspace location is nonzero. An incomplete token-limited result is
not resumable and does not establish a complete definition or reference set. Location skip counts
still cover the complete resolver result. `def` emits every editable definition returned by the
resolver in first-seen order and deduplicates canonical result rows.

Definition expansion occurs only when the resolver's definition selection exactly matches the
declared name of a complete inspect_file outline entry. Supported package or module declarations,
functions, classes, types, variables, and direct methods emit the entry's inclusive logical-line
range. JSON, imports, fields, parameters, locals, and files with uncertain parsing emit only the
definition line. Hsymbol uses the shared verified-row token admission rule in `REQ-READ-001` and
never emits a partial row.

The stock result is stdout and stderr from the same semantic query. For LSP resolvers, stdout is
the JSON serialization of the definition or references result without initialization and shutdown
traffic. Metrics do not run a second query. Shell replay retains the original shell call and
output. Hsymbol remains private, is not routed as a standalone model-visible tool, and never enters
editable rejected-script recovery.

Acceptance:

1. A verified use-site token resolves through one language-appropriate semantic query, and emitted
   hashes equal hread for the same current lines.
2. Every listed source format is accepted. Omitting `N` selects one unique exact language token and
   rejects an ambiguous line before the resolver starts; comments, unrelated literal text, and
   larger identifiers do not affect the count.
3. `def` expands only supported exact outline declarations; every other valid definition emits its
   one current logical line. Multiple definitions retain resolver order and deduplicate rows.
4. `refs` includes declarations, preserves first-seen order, deduplicates one canonical path and
   line, reports skipped locations, and accepts an empty result.
5. Relative and absolute in-workspace paths work. Lexical escapes, escaping symlinks, stale rows,
   missing resolvers, malformed protocol results, and uneditable definitions fail without useful
   stdout.
6. Router startup validates hsymbol inside the immutable built-in snapshot without adding a
   model-visible tool or executable frontend. Passthrough mode loads no private command, and
   shell history containing hsymbol is not recovery
   ancestry.
