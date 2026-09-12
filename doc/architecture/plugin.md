# Tool registry and executor carrier boundary

## CTR-PLUGIN-001 — Tool registry and executor carrier boundary

For `REQ-PLUGIN-001`, the router owns discovery from the public configuration surface in
`doc/brief.md` § Public surface, complete-registry validation, stable registration order,
global tool-name ownership, immutable process-lifetime registry state, and the fail-before-serve
sequence required by `REQ-PLUGIN-001`. One JavaScript runtime adapter loads compiled
declaration modules and invokes their input parsers and translators; it does not own Responses
rewriting, Code Mode capability discovery, wrappers, history, observation, workspace authority, or
executor effects. Loading a declaration is trusted local extension code, but the adapter
receives no engine workspace capability or Codex credential interface.

The registry owns one warm translation host for the router-owned built-in declaration. It starts
after snapshot authentication and before serving, waits for declaration/core import readiness,
and closes before snapshot removal. The toolplugin adapter owns serialized, bounded request/response
framing over private pipes, cancellation, process-group cleanup, and replacement after a failed
call. It never retries the failed call. This keeps Node, bundle, and WASM cold loading outside
ordinary built-in response translation without raising its five-second budget. Configured plugins
retain fresh hosts so module-local state cannot leak between their translation calls. Executors
remain one-shot and are never invoked by the warm host.

The plugin host maps the exact virtual import `mekugi:core/v1` to a router-owned ECMAScript adapter beside
one Go-built WASI reactor in the immutable snapshot. The adapter and reactor are included in the registry
identity, and the host rejects every other `mekugi:` import. Built-in and configured modules therefore use
the same Go-owned portable semantics without vendoring an npm package or binary. The public ECMAScript
surface is versioned independently from `mekugi-tool-plugin/v1`; the raw WASM exports are a private,
lockstep adapter boundary.

`internal/verifiedrow` owns hash and logical UTF-8 row mechanics; `internal/hpatchsyntax` owns compact
quoted framing; `internal/sourcekind` owns portable source capabilities; `internal/golex` owns Go lexical
questions; and `internal/shellsyntax` owns shell headers, batch boundaries, params inheritance, and interpreter identity. Native Go callers import
those packages directly. The reactor receives no preopened directory, inherited environment, or process
capability. It reports UTF-8 byte coordinates only. Parser-specific UTF-16 coordinates, workspace
canonicalization, retained-script reads, process execution, carrier policy, and stale-row resolution remain
with their existing owners.

The registry normalizes each accepted declaration into one router-owned contribution containing
its plugin and tool identity, exact serialized OpenAI specification, bounded input parser and
argv projection, translator handle, and executor implementation handle. This
normalized interface is the only input from plugin code to request rewriting. The router
validates every translator result as a typed executor tool-call carrier against the carrier
catalog retained from that request. Plugins never construct output IDs, call IDs, status,
JSON/SSE envelopes, or replay items.

One carrier renderer owns each supported carrier shape. The generic path preserves a validated
normal tool name and payload. The exec helper is a renderer over that path. It alone owns the
Code Mode outer program or native function arguments, nested invocation, serialization,
independent argv quoting, optional single-placeholder command-template expansion, optional JSON
parameters, and result forwarding.
The parameter object cannot contain `cmd`; the renderer supplies `cmd` from the selected direct or
independently quoted worker command. A present `login` value must be exactly `false`, and the
renderer supplies `login: false` when it is absent. Plugin code can select the typed template and
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

The router's model-input projection may annotate an observed execution yield with the next
host call. It derives tool availability from the current catalog and provenance from validated
replay and visible calls/results. Original output stays intact; no execution-lifecycle record,
polling loop, permission decision, or new continuation tool belongs to this projection.

For multi-program built-in shell input, the response transformer resolves retained input and
uses the portable splitter before invoking the existing translator for each program. It validates
the complete batch before rendering one Code Mode carrier with separate native exec arguments
and sequential continuation waits. Each program keeps its own native result fields and output in
the ordered results array; nonzero exits continue and host errors propagate with partial results.
Only Codex owns execution parameters and session lifecycle. Native-only requests reject batches
rather than projecting differing params onto a shared execution. Retention and replay remain
one original call, without child call IDs or a new persistent session owner.

The bounded syntax exception is the simple cat-write sequence in `REQ-SHELL-001`.
The response transformer owns detection and lowering after built-in shell parsing and before
the ordinary exec renderer. It uses the shared shell AST to reject stateful/compound scripts,
the root renderer for literal patches, and the existing call history for exact provider replay.
Its Code Mode carrier awaits each nested command and its native continuation before advancing,
then aggregates ordinary output and the final status; it owns no separate persistent session.
The native carrier invokes the same executor-provided apply_patch command as mekugi. Plugin
declarations and the model-visible tool catalog remain unchanged.

An implementation needing another executable carrier uses
the generic path rather than encoding an exec surrogate. Mekugi's native workspace translation, recovery
ancestry, patch renderer, and semantic failure baseline remain adapter extensions beside this
generic interface rather than capabilities granted to ordinary plugins.

For each eligible Code Mode request, the router recognizes exactly one authoritative owner: the
custom `exec` tool at the top level, directly inside an `additional_tools` input item's tool list, or nested
under that item's `functions` namespace. The transport surface does not determine which shape
Codex sends. The
`apply_patch` extractor rewrites the owning description. The router also removes the `exec_command`
Markdown section and introductory `tools.exec_command` example from that description. It derives
the request-specific app argument-object or CLI parameter-list shape, removes `cmd`, and appends
only that sanitized shape under `#!params` in the built-in `shell` description. Sibling direct tools,
sibling namespaces, and nested tools remain unchanged. Direct `functions.exec` entries and duplicate Code Mode owners fail closed. A top-level
custom `exec` is accepted only when its description contains the authoritative apply-patch contract.

For an eligible native request, the authoritative tool set instead contains exactly one top-level
custom `apply_patch` and one top-level function `exec_command`. The router removes `apply_patch`,
retains `exec_command` and unrelated siblings, and installs the same model-visible registry tools.
The response transformer uses `exec_command` as a function carrier. Mekugi sends one shell command
that feeds the translated patch to the executor-provided `apply_patch` command, suppresses its
ordinary success text, and returns the root engine's complete final-state report. Failure preserves
the command's nonzero status and output. Generic exec-backed contributions render direct native
function arguments. Both request shapes share the same listener, registry, histories, replay,
recovery, JSON framing, and SSE framing.

Codex owns base prompt delivery. The router owns request-local mekugi guidance injection: it
refreshes a marked section, replaces the pinned stock editing section or GPT-6 Astra search line
and displaced exec-command guidance, or appends only when the top-level Codex config declares
`model_instructions_file`. Pinned conflicting progress and tool-scheduling fragments are also
rewritten outside the marked section on every path; transport-independent safety rules remain.
An unconfigured unknown section fails
closed as upstream drift. This policy runs in memory and never changes Codex configuration or
instruction files.

For every configured executor-backed contribution, the router wrapper owner creates a symlink
inside the authenticated snapshot directory. The snapshot symlink has the tool-name basename and targets
a pinned router executable inside that same snapshot. The executable owner opens the
running image (`/proc/self/exe` on Linux), then retains a verified hard link or a private
copy when linking is unavailable or the installation pathname has changed. Worker
authentication compares file identity, not the installation pathname. The snapshot's
existing lifecycle owns the pinned executable's cleanup. After complete-registry validation, the owner creates or verifies
a session-private same-basename frontend in the snapshot's `bin` directory. The frontend targets the snapshot
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
middleware recognizes only hcat, hgrep, hsymbol, and inspect_file, invokes the matching
snapshot implementation once with expanded argv and the current handler context, writes results
through the handler streams, and returns its status to
the shell. Non-terminal fallback commands use cancellable process groups so descendants cannot
keep the worker alive by retaining inherited streams after cancellation or output overflow.
Every command in a PTY-backed shell remains in the worker's foreground group because `mvdan/sh`
does not coordinate job-control handoff across pipelines; cancellation uses a bounded
inherited-pipe wait.
The same shell middleware owns `hrun` argument validation and bounded prefix/tail display.
Hrun reuses the existing external-command execution owner with substituted capture writers;
it owns no alternate process or session lifecycle. Line-only selection stays in the Go capture
owner without tokenization. Exact token selection uses a private formatting operation on the existing shell executor, sharing the readers' bundled GPT-5
tokenizer. Large tokenization pieces use heap-ordered byte-pair merges with the pinned
model's vocabulary and splitting rules; cancellation can retire the formatting invocation.
Hrun stays outside the plugin contribution and AX reader catalogs. The direct carrier excludes hrun, and declaration validation reserves
its name. Hcat owns whole-row tail selection inside its existing reader implementation.

Other interpreter basenames retain the JavaScript executor's anonymous script descriptor path.

Shell transformation never adds router-owned flags. The shell runtime owner supplies commentary
connection details through a private per-thread descriptor beside the locator, keyed by inherited
`CODEX_THREAD_ID`. The worker accepts discovery only for its own current runtime target. Descriptor
reads are bounded and reject symlinks and non-regular files; missing or invalid discovery leaves
script execution unchanged. A shared thread publisher is independent of individual worker
completion. The runtime owner removes only its own descriptor, preserving replacement entries.

`internal/router/toolplugin/plugin.d.ts` owns the executable result schema. The runtime adapter
validates the current result, and the worker writes it to Codex-facing streams. Optional
`terminationReason: "output_limit"` is validated nonzero-result cleanup metadata, not public
execution output. The JavaScript interpreter executor owns bounded capture and inherited-pipe
drain; it keeps the interpreter in the existing invocation group. After receiving that bounded
failure, the Go invocation owner terminates the remaining Unix process group and strips the
metadata before returning stdout, stderr, and status. Resolver lifecycle code distinguishes child
exit from stream closure, bounds final drain and shutdown, and requests `resolver_cleanup` for
its invocation-owned descendants without changing a completed semantic result. This cleanup
reason permits successful results. Successful shell programs without cleanup metadata continue
to preserve background processes. Cancellation continues to use the same existing process-group owner. No observation owner
invokes the executor again.
Configured frontend and wrapper creation is all-or-nothing for startup. Each registry uses its own `bin` directory, and the wrapper prepends it only
to its Codex child's PATH. No shared frontend lock exists. Built-in shell keeps its
fixed locator and direct per-thread runtime path. Shutdown removes thread runtime
resources and owned configured frontends before the snapshot. Isolated executors
must see the same absolute runtime directory and executable resources; the fixed
shell locator remains on the executor PATH.

The shell runtime owner validates thread and artifact IDs before treating them as single
filesystem components. It pins the thread's active `mekugi-scripts-<thread-id>` directory with `os.Root` for
retention, rerun resolution, and private mekugi application. Exclusive artifact creation
cannot follow a preexisting symlink or overwrite an existing artifact. Expiry uses the pinned
script root; shutdown cancels timers and cleans the owned contents through pinned roots.
It removes only empty directory entries whose identities still match those roots, never
recursively deleting a replacement pathname. Directory opening verifies the opened identity
against the checked entry, and nonblocking file opening rejects FIFO replacements before
reading. The flat runtime locator is outside the script capability.
The router resolves `#!script` references before calling the shell plugin parser, which
rejects unresolved references and performs no retained-file reads. Nested references remain
within the same script root and cycles reject. Replay retains the original call, while a
rerun's retained artifact contains the resolved body. The private hcat middleware opens a
retained regular file through the same confined Go boundary and passes its descriptor to
the JavaScript reader; the reader never reconstructs a retained host path. Ordinary hcat
paths and configured plugin translation remain unchanged.

Startup materializes the validated implementation modules, shared-core adapter and reactor, and dispatch metadata into an
immutable process-scoped worker snapshot. Locator-launched shell and symlink-launched configured
children read that snapshot and verify its registry identity before loading an implementation. A child never rediscovers or
executes the live configuration directory. Changing a configured module therefore cannot alter
served tool behavior before restart. Missing, corrupted, or mismatched snapshot state fails the
child honestly. Router shell storage owns one pinned runtime parent, flat per-thread launcher
symlinks, private commentary descriptors, and exclusively-created active script directories. Artifact and operation leases keep
script capabilities alive; only those live capabilities authorize recursive cleanup. Idle sessions
never reopen directory identity snapshots. Locator cleanup matches the router worker target and
only unlinks the locator; this is not authentication or isolation from arbitrary same-user
filesystem tampering. Shutdown cleanup owns these locators, commentary descriptors, and active script capabilities,
configured session frontends, snapshot wrappers, and the shared snapshot.

The response transformer uses registry membership instead of hardcoded tool-name predicates for
JSON, SSE, and replay. One status-aware terminal projection restores both JSON and every SSE
terminal variant. An SSE terminal event supplies the authoritative status even when the embedded
response omits it, without adding or changing wire fields; JSON uses its body status. Stream
handling owns pending-call cancellation versus successful completion checks. Interrupted terminal projection only evaluates completed items or restores already-delivered
calls, never unfinished input. Retained history stores the original contribution identity and input plus
the exact validated carrier kind, name, and payload. Replay verifies the carrier byte-for-byte
before restoring the model-visible call. Generic history cannot enter recovery ancestry;
mekugi alone attaches its existing recovery state. A plugin input rejection may become a
bounded diagnostic carrier, while a runtime-adapter failure, malformed translator result, or
unavailable carrier fails routing and cannot be represented as successful translation.

When authored Code Mode owns the `text` identifier, diagnostics are retained with the carrier
instead of being evaluated inside that program. The replay owner appends one separate warning
text part to the model-visible tool result, preserving the executor's original text and other
content parts. The durable record carries this projection across resume and forks; repeated
projection does not duplicate the warning. Commentary configuration does not control delivery.

For mekugi, the immediate executor carrier contains the root engine's translated patch and
already-rendered final-state report. Response restoration retains the original model-visible
hpatch call and normal executor result for later model-visible history; it does not expose
the translated patch as later model input or derive another report representation. A later
model inference can therefore reuse an exact current row present in the retained successful
report. Router history does not retain hcat rows on behalf of the engine, predict later
targets, or move final-reference projection across the root boundary.
