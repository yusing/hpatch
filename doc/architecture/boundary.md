# Filesystem and output boundary

## CTR-BOUNDARY-001 — Filesystem and output boundary

The root library boundary owns workspace authorization, evaluation diagnostics, completed
results, atomic commit coordination, and translation for
`REQ-GUIDE-001` and `REQ-OUTPUT-001`. Persistent Codex edit, shell, read, search, and inspection
guidance and CTP/2 representation guidance share `contrib/codex/file-editing-instructions.md` as their source.
Tool descriptions retain only call-local contracts and request-specific schemas. The router
renders dynamic rejected-script references and the recovery instruction from the adjacent
recovery template only for actionable evaluator rejection diagnostics.

The root-scoped workspace boundary used by authorized library callers owns a pinned `*os.Root`,
a root-relative cwd, root-scoped reads, staging, commit, and rollback. Relative script paths
resolve from cwd; absolute paths become root-relative identities only when within root. Lexical
and symlink escapes fail. Initial inputs cross into that boundary only after a regular-file check
and strict UTF-8 decoding.

Normal router translation is outside that confinement boundary. It supplies an optional canonical
metadata directory to `TranslateForHostAt`, performs ordinary host path resolution without a
router-owned filesystem capability, and never falls back to router cwd. Without a selected
directory, only absolute operands are valid. Retained private `@shell` application is the confined
router exception and uses `ApplyForHostRoot`.

The router chooses retained-state identity from an explicit `session-id`, then a stable
`prompt_cache_key`, and only then a request-scoped client request ID. Retained history is
additionally scoped to the selected canonical metadata directory, or to the explicit no-directory
state, preventing a reused cache key from exposing recovery or replay state across worktrees.
Retained recovery and replay history is bounded: the oldest calls within a session and the
least-recently used inactive sessions are evicted before capacity can reject new completed work.
Retained history is also reconciled against each request: because truncation only removes a
suffix of a conversation, every retained call newer than the newest one the request's input still
shows belongs to a discarded turn and is dropped, releasing its call and byte budget. A request
the router rejects mutates no retained state, and a session with a second in-flight turn is not
reconciled because that turn's calls are committed only at response completion.
An active request protects its session throughout replay restoration and response transformation.
Background Responses requests reject before upstream forwarding because
the router has no retrieval boundary for their eventual result. Malformed SSE state is
sticky and cannot be overwritten by a later terminal event.

Transport capture is auxiliary: tokenization or durable-write failures cannot replace a successful
tool result, rejection diagnostic, read or search result, or response, while request cancellation
still propagates. An explicitly requested capture file that cannot be opened fails startup.

The router exposes only hpatch, hpatch_recover, and shell beside the displaced Code Mode `exec` carrier.
Hpatch remains the native engine contribution. Hpatch_recover is a router-owned recovery contribution. Shell, hread, hgrep, hsymbol, and inspect_file are
JavaScript- and TypeScript-authored built-in plugin contributions compiled by Bun into one
embedded JavaScript module with the reserved `builtin.shell` identity. Shell is model-visible;
hread, hgrep, hsymbol, and inspect_file retain snapshot-backed implementations but their
specifications are private and they have no executable frontends. Configured user tools remain
model-visible. Portable verified-row, compact-syntax, source-classification, Go-lexical, and
shell-header mechanisms come from the same authenticated Go-built shared core available to
configured plugins under `REQ-PLUGIN-001`; filesystem, process, parser-coordinate, and carrier
policy remain with the owners described here.

The plugin runtime snapshots and validates the immutable built-in module before user
declarations, then applies one normalized registry path to all executable
contributions. The registry projects only model-visible tool definitions. Configured
executor-backed plugins retain wrapper and frontend dispatch. Built-in shell uses the fixed
executor-side locator and a direct per-thread runtime path; its private commands execute from
that worker.
Request rewriting installs the projected hpatch, hpatch_recover, and shell definitions, removes the native
exec-command contract, and rewrites received Responses instructions from the central guidance
source. Native model protocol omits its leading CTP/2 section and stops after that ordinary rewrite;
CTP/2 injects the complete source and transforms eligible model-visible strings under
`REQ-CTP-001`.
Private contribution descriptions are execution contracts, not a prompt source. Passthrough
mode loads no registry.

The shell carrier preserves one physical line containing one static external implicit-default-Bash
command, with no shebang or directive, as the direct Codex exec command. Every other program emits
`shell <interpreter> <program>` in Codex's exec context. The fixed `cmd/shell` locator reads the path
`$HPATCH_RUNTIME_DIR/hpatch-$CODEX_THREAD_ID/.runtime` and replaces itself with the authenticated
snapshot worker stored there by the router. For Bash and sh selectors, a router-owned `mvdan/sh`
runner
parses `LangBash` or `LangPOSIX`, preserves shell-owned expansion and composition, and intercepts
private command argv without launching another router worker. Other interpreters retain the
plugin executor path. The worker derives relative-path resolution from its actual current
directory and leaves sandbox and permission enforcement to Codex. Absent request-specific exec
parameters, the shell carrier sets neither an environment override nor a working directory. A
direct `apply_patch` owner, missing Node.js runtime, invalid embedded declaration, or configured
wrapper failure rejects startup or rewriting before forwarding.

The hread built-in accepts one path argv and an optional separate inclusive range argv.
Shell quoting owns whitespace and metacharacters in paths. Hread has no multi-file input or
batch result format; the model batches reads as separate commands in one shell script.
Rendering streams fixed-size chunks, validates UTF-8 across the complete regular file,
and buffers only selected lines. The shared verified-row accumulator counts exact formatted
current output with the pinned GPT-5 tokenizer, admits through the 15,000-token soft limit with
one complete-row overshoot through 15,500, and retains current and stock rows as one pair.
An omitted row seals output growth while hread continues the existing stream for file and range
validation.

The hgrep built-in receives ordinary shell-produced argv. Its TypeScript implementation
rejects incompatible source and output modes and invokes installed ripgrep through
`--json --no-config`. Ripgrep alone owns search selection. The implementation consumes match
and context events, renders complete verified rows, deduplicates only identical path-and-line
results, and provides no fallback search implementation. Shell, rather than hgrep, owns
pipelines, redirection, and command composition. Hgrep uses the same verified-row accumulator
after result deduplication and terminates ripgrep when the accumulator rejects a row.

The hsymbol built-in owns verified language-token selection and verified-row rendering around one
installed semantic query. It canonicalizes the executor cwd as its workspace, confines input and
returned files to that workspace, and uses the same pinned parsers as inspect_file to select an
exact token before invoking the resolver. Gopls owns Go resolution at a UTF-8 byte offset;
TypeScript 7's `tsc --lsp --stdio` owns JavaScript, TypeScript, and JSON resolution; and
`pyright-langserver --stdio` owns Python resolution. The shared LSP client owns one process-scoped
initialize, document-open, query, and cleanup lifecycle with UTF-16 positions. Hsymbol deduplicates
returned rows by canonical path and line and renders canonical targets relative to the workspace
before applying the shared verified-row accumulator. It provides the same query's semantic response
as stock metric evidence. Definition expansion reuses inspect_file's language outline projection
and requires the returned definition selection to match an exact supported declared-name token;
every other definition remains one line.

The inspect_file built-in owns bounded structural inspection of one workspace-relative regular
file. It canonicalizes the executor cwd and symlink target, uses pinned Lezer parsers for Go,
Python, Markdown, JSON, and every stable TypeScript 7 source format, and projects only navigation
metadata. Outline `line` and `line_end` are shared verified-row identities for the inclusive span
and are copyable HPATCH targets; the renderer still omits source text. Unsupported extensions stop after
file metadata. A concise result shape schema supplies the embedded private guidance.
The renderer owns the 64 KiB complete-document budget and truncates only at outline-entry
boundaries; parser recovery remains an independent result flag.

The generated built-in JavaScript and runtime host are materialized inside the authenticated
process snapshot. The directly launched shell child verifies that snapshot before loading an
implementation; configured plugin children retain symlink-based verification. The router never
pre-reads files and never fabricates an `apply_patch` result for
read, search, symbol lookup, or inspection. Model history retains shell calls, private commands stay outside
response routing and edit recovery ancestry. Transport capture observes the resulting call once.
Registry shutdown removes configured frontends and the snapshot.

Library callers pass an already-authorized root and a root-relative cwd. Absolute operands
are matched against the canonical root name; equivalent aliases are not resolved outside the
capability. Translation and commit consume the same identities, so cwd affects relative operands
without changing the workspace boundary.

The root library validates and formats the state report before an external effect. Apply stages
the complete engine result and commits atomically; translation completely renders the patch
without mutation. No script command crosses the external commit boundary. The transaction
coordinator owns backups, ordered operations, rollback attempts, and honest reporting of external
commit or rollback failure.
