---
pjdoc:
  version: 1
  kind: architecture
  scope: root
  status: draft
  revision: "29"
  files:
    []
---
# hpatch architecture contract

## CTR-CTP-001 — Router-owned compact provider representation

One CTP owner in `internal/router` sits after Hpatch request projection and before provider
serialization. It receives ordinary parsed Responses fields, appends one request-local exact
dictionary to the existing top-level or developer-message instruction carrier only when the stable
admission projection is token-positive, and returns a response
transformer for that admitted representation. It does not parse HPATCH/2, change the tool registry,
own provider usage, retain session history, define model instructions, or define another executor
carrier. Persistent CTP interpretation and emission guidance belongs to
`contrib/codex/file-editing-instructions.md`; the router emits only request-local dictionary data.
Request-local discovery uses the current tool descriptions and immutable pre-model input prefix so
appended model history cannot churn the provider-cache prefix; applying the resulting definitions
to later eligible history remains part of the same stateless representation step.

On responses, the CTP transformer runs before the existing Hpatch transformer. It restores compact
references only in assistant text for complete JSON output and SSE terminal text, while tool names,
inputs, and arguments remain native for ordinary registry routing, translation, history, recovery,
and carrier rendering. The transport owns the minimal response-transformer composition needed to
preserve that order and to finish or discard both request-local states on every terminal path.

The CTP owner derives auxiliary native-versus-compact token pairs from the same tokenizer used for
admission. Input counts cover each admitted whole request after Hpatch projection; output counts
cover decoded assistant text once per logical content item, using each terminal
`response.output_text.done` event for streams while excluding repeated projections. The in-memory
router metrics retain aggregates plus bounded per-session request and CTP input/output observations,
including bytes, dictionary size, codec timing, and dropped-observation counters. Counting failure
cannot replace a successful request, response, or provider usage record. Aggregate admission
decisions expose why a request stayed native without retaining candidate or prompt text.

CTP operates inside native Responses envelopes and may rewrite only the representation identified
by `REQ-CTP-001`; that requirement retains the provider-owned fields and native fallback contract.

## CTR-SYNTAX-001 — Shared compact-script framing

One internal lexical owner implements the inline quoted-operand and fixed `<<PATCH`
heredoc framing required by `REQ-SCRIPT-001`. The root parser consumes decoded operands
and command frames for ordinary HPATCH/2. The router reuses the lexical owner only to decode
ordinary quoted targets while rebuilding a target-corrected rejected script. The root
`EditText` primitive owns immutable text-target mutation. The
lexical owner performs no filesystem access, target resolution, command evaluation,
rejected-script ancestry, or output rendering.

## CTR-CORE-001 — Virtual workspace and immutable-baseline edit planning

One engine owns parsed command evaluation, logical path resolution, first-touch order,
one immutable invocation baseline per touched existing file, target resolution, ordered
baseline-coordinate splice registration, atomic edit-conflict validation, new-file
initialization state, and original-to-final net file actions for `REQ-SCRIPT-001`,
`REQ-FILE-001`, `REQ-SELECT-001`, and `REQ-EDIT-001`. Apply and translation callers consume the same completed result; neither path reimplements command semantics. HPATCH/2 reduces
this existing owner rather than adding an editor, AST layer, or parallel patch engine.

The workspace owner retains each touched file's invocation-original identity and content,
current logical path, pending lifecycle action, and ordered pending splices. Returning to
a file reuses those values. A successful script orders the splices once and renders one
original-to-final content value. No command materializes an intermediate baseline, and
all targets continue to resolve against the invocation-original content.

One shared pure verified-row owner computes and renders `LINE:HASH` identity for routed
reads under `REQ-READ-001`, target validation, repair context, and final-state previews.
Target resolution looks up the specified one-based line, compares its hash, and never
scans for another hash match. It converts line, inclusive range, and anchored literal
targets into one or more immutable-baseline spans. Resolution performs no mutation and
retains no active target state.

The evaluator permits targetless `type` only as the next nonblank command after `new` and
closes that opportunity when any other command begins. The edit planner maps target-bearing
`type` and destination-bearing `add` directly over resolved spans; an empty `type` value is a
deletion, while `add` inserts before a line or text destination or appends at `EOF`. It registers every splice generated by one command atomically, rejects overlapping destructive
interiors and insertions strictly inside them, permits boundary insertions, and orders
same-boundary insertions by script command order. Later targets continue to resolve
against the unchanged invocation baseline. Content introduced by a pending edit is not
target input; a dependent edit crosses the external success boundary and uses an exact
current report row when present or a focused later hread when absent.

The engine obtains original files only through the workspace boundary, never writes files
or process output, and retains original identity across moves. It checks cancellation
before command evaluation and final rendering. Failure or cancellation returns no
completed changes or final state. A completed result contains ordered net changes,
structured command and target metrics, final active logical path, last effective mutation
metadata, effective per-command editor splices with authored command provenance, rendered
final content already owned by each editor, and language-formatting offset maps needed for
bounded state reporting.

## CTR-CORRECT-001 — Router-only rejected-script recovery

The router owns the dedicated target-only recovery grammar, command handles,
rejected-script ancestry, worktree isolation, correlation, replay, diagnostics, chain metrics,
and complete-script reevaluation. Each recovery line replaces only the target of one handled
command in the latest evaluated script. The router uses the root `EditText` primitive to rebuild
that complete script and evaluates it normally. The core evaluator, root public APIs, root
grammar, and ordinary `functions.hpatch` have no recovery mode. Non-target and mixed failures
require a complete ordinary script. Malformed, stale, conflicting, or incomplete recovery
changes neither the workspace nor the retained evaluated baseline.
Before rebuilding, the router compares the parsed current and replacement target identities and
rejects an unchanged identity without root reevaluation. Equivalent quoted escapes and implicit
versus explicit default occurrence counts share one identity; a single-row range shares its row's
identity. That proxy rejection retains the same baseline and handles for a later different target.

## CTR-STATE-001 — Bounded final-state projection

One projector owned beside the engine consumes only the completed result. It derives the
active final path, last effective mutation operation and surviving path, affected target
count, at most three immutable-baseline Unicode ranges, remaining-range count, aggregate
net file actions, and final-reference blocks for `REQ-OUTPUT-001`. For each surviving
effective content command, it uses the editor's existing splices and rendered content to
form one aggregate rendered extent, maps its endpoints through the existing language-format
offset map, and selects the endpoint rows plus their immediate surviving neighbors. It
retains authored command order, orders each block by final line, and deduplicates rows
within that block. It does not consult the committed filesystem or reconstruct editor state.

One pure formatter renders the projection through the shared verified-row owner. It emits
at most four rows per effective command and retains the existing three-row active-file
fallback whenever that active file has no reference block. It truncates displayed row text
to 64 Unicode code points while hashing complete final line content. It preserves active
and moved paths, last-edit summaries, net file counts, empty-file rows, and control escaping
before any external effect. Apply and translation paths share this projection.

The projection borrows completed editor content during rendering and retains no additional
original or final content copy. Its rows describe only successful completed state. The router
consumes the rendered report and does not compute coordinates, hashes, command extents, or
formatting adjustments.

## CTR-BOUNDARY-001 — Filesystem and output boundary

The root library boundary owns workspace authorization, evaluation diagnostics, completed
results, atomic commit coordination, translation, and structured host metrics for
`REQ-GUIDE-001` and `REQ-OUTPUT-001`. All persistent Codex edit, shell, read, search, and
inspection and CTP representation guidance shares `contrib/codex/file-editing-instructions.md` as its source.
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

Metrics on the response path are auxiliary: tokenization or durable-write failures cannot
replace a successful tool result, rejection diagnostic, read or search result, or overhead-only
response, while request cancellation still propagates. Definition accounting consumes the
exact serialized collection installed from the validated built-in and plugin registry and a
stable per-plugin and per-tool breakdown derived from that same collection.

The router exposes only hpatch, hpatch_recover, and shell beside the displaced Code Mode `exec` carrier.
Hpatch remains the native engine contribution. Hpatch_recover is a router-owned recovery contribution. Shell, hread, hgrep, hsymbol, and inspect_file are
JavaScript- and TypeScript-authored built-in plugin contributions compiled by Bun into one
embedded JavaScript module with the reserved `builtin.shell` identity. Shell is model-visible;
hread, hgrep, hsymbol, and inspect_file retain snapshot-backed implementations but their
specifications are private and they have no executable frontends. Configured user tools remain
model-visible.

The plugin runtime snapshots and validates the immutable built-in module before user
declarations, then applies one normalized registry and metrics path to all executable
contributions. The registry projects only model-visible tool definitions. Configured
executor-backed plugins retain wrapper and frontend dispatch. Built-in shell uses the fixed
executor-side locator and a direct per-thread runtime path; its private commands execute from
that worker.
Request rewriting installs the projected hpatch, hpatch_recover, and shell definitions, removes the native
exec-command contract, and rewrites received Responses instructions from the central guidance
source. Native model protocol stops after that ordinary rewrite; CTP/1 may append dictionary data
to the resulting instruction carrier under `REQ-CTP-001`.
Private contribution descriptions are execution contracts, not a prompt source. Passthrough
mode loads no registry.

The shell carrier emits `shell <interpreter> <program>` in Codex's exec context. The fixed
`cmd/shell` locator reads the path `$HPATCH_RUNTIME_DIR/hpatch-$CODEX_THREAD_ID/.runtime` and
replaces itself with the authenticated snapshot worker stored there by the router. For Bash and
sh selectors, a router-owned `mvdan/sh` runner
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
metadata. Unsupported extensions stop after
file metadata. A concise result shape schema supplies the embedded private guidance.
The renderer owns the 64 KiB complete-document budget and truncates only at outline-entry
boundaries; parser recovery remains an independent result flag.

The generated built-in JavaScript and runtime host are materialized inside the authenticated
process snapshot. The directly launched shell child verifies that snapshot before loading an
implementation; configured plugin children retain symlink-based verification. The router never
pre-reads files and never fabricates an `apply_patch` result for
read, search, symbol lookup, or inspection. Model history retains shell calls, private commands stay outside
response routing and edit recovery ancestry, and generalized per-tool metrics record execution.
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

## CTR-PLUGIN-001 — Tool registry and Code Mode carrier boundary

For `REQ-PLUGIN-001`, the router owns discovery from the public configuration surface in
`doc/brief.md` § Public surface, complete-registry validation, stable registration order,
global tool-name ownership, immutable process-lifetime registry state, and the fail-before-serve
sequence required by `doc/brief.md` § Constraints. One JavaScript runtime adapter loads compiled
declaration modules and invokes their input parsers and translators; it does not own Responses
rewriting, Code Mode capability discovery, wrappers, history, metrics, workspace authority, or
executor effects. Loading a declaration is trusted local extension code, but the adapter
receives no engine workspace capability or Codex credential interface.

The registry normalizes each accepted declaration into one router-owned contribution containing
its plugin and tool identity, exact serialized OpenAI specification, bounded input parser and
argv projection, translator handle, executor implementation handle, and metrics identity. This
normalized interface is the only input from plugin code to request rewriting. The router
validates every translator result as a typed Code Mode tool-call carrier against the carrier
catalog retained from that request. Plugins never construct output IDs, call IDs, status,
JSON/SSE envelopes, or replay items.

One carrier renderer owns each supported carrier shape. The generic path preserves a validated
normal Code Mode tool name and payload. The exec helper is a renderer over that path. It alone
owns the outer exec program, nested invocation, serialization, independent argv quoting, optional
single-placeholder command-template expansion, optional JSON parameters, and result forwarding.
The parameter object cannot contain `cmd`; the renderer supplies `cmd` from the independently
quoted worker command. A present `login` value must be exactly `false`, and the renderer
supplies `login: false` when it is absent. Plugin code can select the typed template and
parameter variants but cannot construct the outer carrier or quote the nested worker command.

For the built-in `shell` contribution, the owning Code Mode executable definition also owns
asynchronous exec-session creation, yield timing, continuation handles, continuation input,
cancellation, and terminal state. The exec renderer forwards the complete native nested-tool
result through the outer carrier instead of projecting only command output. A yielded result and
its continuation handle remain nonterminal; the renderer does not poll, resume, cancel, retry,
replace, or persist the session, and plugin code receives no session-lifecycle capability. The
native continuation operation resumes the same host-owned session. JSON and SSE framing, history,
and replay preserve this distinction without defining another result envelope or continuation
protocol. Other contributed tools retain their declared output projections.

An exec translator may provide a validated nonempty stock command for output metrics. The same
renderer applies the selected template and parameters to produce its canonical stock carrier.
This evidence never replaces the worker carrier used for the response, history, replay, or
execution. An implementation needing another executable Code Mode carrier uses the generic path
rather than encoding an exec surrogate. Hpatch's native workspace translation, recovery
ancestry, patch renderer, and semantic failure baseline remain adapter extensions beside this
generic interface rather than capabilities granted to ordinary plugins.

For each eligible request, the router recognizes exactly one authoritative Code Mode owner: the
custom `exec` tool either directly inside an `additional_tools` input item's tool list for
app-server traffic or nested under that item's `functions` namespace for CLI traffic. The
`apply_patch` extractor rewrites the owning description. The router also removes the `exec_command`
Markdown section and introductory `tools.exec_command` example from that description. It derives
the request-specific app argument-object or CLI parameter-list shape, removes `cmd`, and appends
only that sanitized shape under `#!params` in the built-in `shell` description. Sibling direct tools,
sibling namespaces, and nested tools remain unchanged. Direct `functions.exec` entries and
top-level `exec` or `functions.exec` tools fail closed.

Codex owns base prompt delivery. The router owns request-local hpatch guidance injection: it
refreshes a marked section, replaces the pinned stock editing section, or appends only when the
top-level Codex config declares `model_instructions_file`. An unconfigured unknown section fails
closed as upstream drift. This policy runs in memory and never changes Codex configuration or
instruction files.

For every configured executor-backed contribution, the router wrapper owner creates a symlink
inside the authenticated snapshot directory. The snapshot symlink has the tool-name basename and targets
the running router executable. After complete-registry validation, the owner creates or verifies
a stable same-basename frontend beside the router executable. The frontend targets the snapshot
wrapper. Configured child dispatch resolves the frontend once, validates the snapshot wrapper and
registry identity, and gives the implementation the remaining argv without inventing a cwd or
environment. The worker passes frontend standard input to the JavaScript host on a dedicated
inherited descriptor while the host retains descriptor zero for its bounded JSON control request.
The child executes only because Codex ran the returned basename carrier, so Codex continues to
own sandbox and permission enforcement.

Built-in shell uses one private `shell` wrapper inside the authenticated snapshot but no stable
frontend. Its carrier independently quotes the stable `shell` basename, normalized interpreter
fields, and exact body. The locator
never contains tool-specific state; the router updates the direct thread runtime path to the
current snapshot worker instead. The worker verifies the manifest and registry identity. For Bash and sh
basenames, `mvdan/sh` owns parsing, built-ins, functions, expansion, redirections, pipelines,
working-directory changes, exported environment, and fallback external commands. Its exec
middleware recognizes only hread, hgrep, hsymbol, and inspect_file, invokes the matching
snapshot implementation once with expanded argv and the current handler context, writes results
through the handler streams, records the same-execution stock evidence, and returns its status to
the shell. Non-terminal fallback commands use cancellable process groups so descendants cannot
keep the worker alive by retaining inherited streams after cancellation or output overflow.
Every command in a PTY-backed shell remains in the worker's foreground group because `mvdan/sh`
does not coordinate job-control handoff across pipelines; cancellation uses a bounded
inherited-pipe wait.
Other interpreter basenames retain the JavaScript executor's anonymous script descriptor path.

`internal/router/toolplugin/plugin.d.ts` owns the executable result schema. The runtime adapter
validates the current result independently from its optional stock metric evidence. The worker
writes only the current result to Codex-facing streams and sends structured current and
validated stock evidence, with the pinned plugin and tool identity, to the root metrics owner.
The executor is the only content producer for both shapes; no metrics owner invokes it again.
Configured frontend and wrapper creation is all-or-nothing for startup. When those frontends
exist, the router holds one exclusive frontend lock for its process lifetime, and another router
using that frontend directory fails startup. A built-in-only registry creates no frontend or
frontend lock. Each eligible thread instead gets one direct `.runtime` link under its
`hpatch-$CODEX_THREAD_ID` directory. During upgrade the registry briefly takes the frontend lock
only when an authenticated
pre-revamp shell/private frontend exists, removes those retired links, and leaves unrelated paths
unchanged. A later process can replace an authenticated prior configured frontend after a crash
releases the lock. Shutdown removes thread runtime directories and owned configured frontends before
removing the snapshot and releasing the frontend lock. An isolated executor deployment must use
the same absolute `HPATCH_RUNTIME_DIR` and make the thread runtime link, router executable, plugin runtime,
and implementation resources visible independently of workspace selection; the fixed helper and
configured frontends additionally require their shared directory on the executor `PATH`.

Startup materializes the validated implementation modules and dispatch metadata into an
immutable process-scoped worker snapshot. Locator-launched shell and symlink-launched configured
children read that snapshot and verify its registry identity before loading an implementation. A child never rediscovers or
executes the live configuration directory. Changing a configured module therefore cannot alter
served tool behavior before restart. Missing, corrupted, or mismatched snapshot state fails the
child honestly. Shutdown cleanup owns thread runtime directories, configured stable frontends,
snapshot wrappers, and the shared snapshot.

The response transformer uses registry membership instead of hardcoded tool-name predicates for
JSON, SSE, and replay. Retained history stores the original contribution identity and input plus
the exact validated carrier kind, name, and payload. Replay verifies the carrier byte-for-byte
before restoring the model-visible call. Generic history cannot enter recovery ancestry;
hpatch alone attaches its existing recovery state. A plugin input rejection may become a
bounded diagnostic carrier, while a runtime-adapter failure, malformed translator result, or
unavailable carrier fails routing and cannot be represented as successful translation.

For hpatch, the immediate Code Mode carrier contains the root engine's translated patch and
already-rendered final-state report. Response restoration retains the original model-visible
hpatch call and normal executor result for later model-visible history; it does not expose
the translated patch as later model input or derive another report representation. A later
model inference can therefore reuse an exact current row present in the retained successful
report. Router history does not retain hread rows on behalf of the engine, predict later
targets, or move final-reference projection across the root boundary.

## CTR-METRICS-001 — Metrics classification and persistence

One metrics classifier consumes structured parser, evaluator, registry, carrier, stock-carrier,
and completed executor-result events for `REQ-METRICS-001`. It does not re-parse tool inputs,
diagnostics, or rendered responses. The translation path owns per-plugin and per-tool definition,
call, emitted-shape, translated-shape, and failed-translation counters. The execution path owns
current and stock result-shape counters from the private worker's validated evidence. Both paths
derive persisted metric token counts and stable identity inside the metrics owner; plugin code
independently counts formatted verified rows only to enforce its output-admission contract and
never supplies those counts as metric evidence or outer carrier serialization. Hpatch's adapter additionally
owns its effective, ineffective, fixed failed semantic baseline, report, command, target, and stable
terminal-reason classifications. The report formatter's exact emitted string is the only source
for report-input token counting. Reduction ratios and signed net input are presentation-time
calculations from
persisted counters.

The metrics store owns tokenizer use, stable tool identity keys, exact installed-definition
totals, executor-result totals, overflow checks, interprocess locking, alternating checksummed
bounded slots, persistence-generation selection, current-version decoding, obsolete-version reset,
and page-cache writeback policy. Translation classification occurs after the router validates a
carrier. Executor-result classification occurs in the private worker after one completed execution
and does not rewrite translation classification. Missing or invalid stock evidence uses the current
shape under `REQ-METRICS-001`. Metrics failure remains auxiliary and cannot change the requested
edit, translated carrier, executor result, state report, or exit status. Router end-to-end Responses
usage remains authoritative for provider-consumed model input.

## CTR-TRANSLATE-001 — Patch rendering

One translation renderer owns all OpenAI `apply_patch` syntax. It receives the engine's ordered net change set and emits one envelope containing the required `Add File`, `Update File`, `Move to`, and `Delete File` actions. It finishes the complete string before returning so evaluation or rendering failures cannot expose a partial patch.

For root-scoped engine translation, every emitted path is relative to the workspace root, independent of cwd. The router's normal host adapter uses `TranslateForHostAt`; it evaluates against an optional canonical metadata directory without confinement, rejects relative operands when no directory is selected, never falls back to router cwd, and preserves cleaned host path identities for Codex's carrier. The renderer owns the minimal nonempty verification hunk required by OpenAI `apply_patch` when a move has no content change, and the renderer-only LF normalization required by the line-oriented output format. For changed content it expands context until every bare hunk's old-side sequence is unique, failing instead of emitting an ambiguous patch. The engine's evaluated contents remain unchanged.

## CTR-COMPARE-001 — Independent comparison cases

The comparison artifact may call the engine as test support, but every equivalent
`apply_patch` input is independently authored scenario data. A clearly test-only patch
applier verifies both representations reach the same final path-to-content map before
token counts are reported. Neither the applier nor the comparison is part of an installed runtime surface.

## CTR-BENCH-001 — Benchmark trust and execution boundary

The command boundary in `cmd/hpatch-bench` owns invocation,
progress, exit status, and result destinations for `REQ-BENCH-001`. The benchmark owner in
`internal/bench` owns the executable manifest schema, source-revision
resolution, history-free snapshot lifecycle, arm scheduling, Codex process execution,
pre-grader change capture, hidden-file injection, graders, and structured results. Source
repositories and hidden inputs are read-only authorities; disposable snapshots own every
agent and grader mutation and are removed after their artifacts are captured.

The router remains the sole owner of upstream authentication, provider forwarding, cache
keys, response delivery, and provider usage. Its selected mode determines whether the
existing hpatch transformer participates. Pass-through does not duplicate forwarding or
introduce another provider client. The metrics endpoint is the executable mode-label
boundary used to prevent arm misconfiguration.
The terminal request log is the request-level cache-attribution boundary: it combines the one
terminal provider usage observation with the request and session already owned by that lifecycle.
Aggregate metrics remain unchanged.

Hidden destinations cross into an agent-mutated workspace only through a pinned `*os.Root`
after change capture. A pre-existing destination, lexical escape, or symlink escape fails
instead of overwriting agent or external content. Grader commands consume the resulting
disposable tree; they cannot turn an agent, scope, infrastructure, timeout, or cancellation
failure into benchmark success.
