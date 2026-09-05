# Tool registry and executor carrier boundary

## CTR-PLUGIN-001 — Tool registry and executor carrier boundary

For `REQ-PLUGIN-001`, the router owns discovery from the public configuration surface in
`doc/brief.md` § Public surface, complete-registry validation, stable registration order,
global tool-name ownership, immutable process-lifetime registry state, and the fail-before-serve
sequence required by `doc/brief.md` § Constraints. One JavaScript runtime adapter loads compiled
declaration modules and invokes their input parsers and translators; it does not own Responses
rewriting, Code Mode capability discovery, wrappers, history, observation, workspace authority, or
executor effects. Loading a declaration is trusted local extension code, but the adapter
receives no engine workspace capability or Codex credential interface.

The plugin host maps the exact virtual import `hpatch:core/v1` to a router-owned ECMAScript adapter beside
one Go-built WASI reactor in the immutable snapshot. The adapter and reactor are included in the registry
identity, and the host rejects every other `hpatch:` import. Built-in and configured modules therefore use
the same Go-owned portable semantics without vendoring an npm package or binary. The public ECMAScript
surface is versioned independently from `hpatch-tool-plugin/v1`; the raw WASM exports are a private,
lockstep adapter boundary.

`internal/verifiedrow` owns hash and logical UTF-8 row mechanics; `internal/hpatchsyntax` owns compact
quoted framing; `internal/sourcekind` owns portable source capabilities; `internal/golex` owns Go lexical
questions; and `internal/shellsyntax` owns shell header and interpreter identity. Native Go callers import
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

An implementation needing another executable carrier uses
the generic path rather than encoding an exec surrogate. Hpatch's native workspace translation, recovery
ancestry, patch renderer, and semantic failure baseline remain adapter extensions beside this
generic interface rather than capabilities granted to ordinary plugins.

For each eligible Code Mode request, the router recognizes exactly one authoritative owner: the
custom `exec` tool either directly inside an `additional_tools` input item's tool list or nested
under that item's `functions` namespace. The transport surface does not determine which shape
Codex sends. The
`apply_patch` extractor rewrites the owning description. The router also removes the `exec_command`
Markdown section and introductory `tools.exec_command` example from that description. It derives
the request-specific app argument-object or CLI parameter-list shape, removes `cmd`, and appends
only that sanitized shape under `#!params` in the built-in `shell` description. Sibling direct tools,
sibling namespaces, and nested tools remain unchanged. Direct `functions.exec` entries and
top-level `exec` or `functions.exec` tools fail closed.

For an eligible native request, the authoritative tool set instead contains exactly one top-level
custom `apply_patch` and one top-level function `exec_command`. The router removes `apply_patch`,
retains `exec_command` and unrelated siblings, and installs the same model-visible registry tools.
The response transformer uses `exec_command` as a function carrier. Hpatch sends one shell command
that feeds the translated patch to the executor-provided `apply_patch` command, suppresses its
ordinary success text, and returns the root engine's complete final-state report. Failure preserves
the command's nonzero status and output. Generic exec-backed contributions render direct native
function arguments. Both request shapes share the same listener, registry, histories, replay,
recovery, JSON framing, and SSE framing.

Codex owns base prompt delivery. The router owns request-local hpatch guidance injection: it
refreshes a marked section, replaces the pinned stock editing section or GPT-6 Astra search line
and displaced exec-command guidance, or appends only when the top-level Codex config declares
`model_instructions_file`. An unconfigured unknown section fails
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
through the handler streams, and returns its status to
the shell. Non-terminal fallback commands use cancellable process groups so descendants cannot
keep the worker alive by retaining inherited streams after cancellation or output overflow.
Every command in a PTY-backed shell remains in the worker's foreground group because `mvdan/sh`
does not coordinate job-control handoff across pipelines; cancellation uses a bounded
inherited-pipe wait.
Other interpreter basenames retain the JavaScript executor's anonymous script descriptor path.

`internal/router/toolplugin/plugin.d.ts` owns the executable result schema. The runtime adapter
validates the current result, and the worker writes it to Codex-facing streams. No observation owner
invokes the executor again.
Configured frontend and wrapper creation is all-or-nothing for startup. When those frontends
exist, the router holds one exclusive frontend lock for its process lifetime, and another router
using that frontend directory fails startup. A built-in-only registry creates no frontend or
frontend lock. Each eligible thread instead gets one direct `.runtime` link under its
`hpatch-$CODEX_THREAD_ID` directory. A later process can replace an authenticated prior configured
frontend after a crash releases the lock. Shutdown removes thread runtime directories and owned
configured frontends before removing the snapshot and releasing the frontend lock. An isolated
executor deployment must use the same absolute `HPATCH_RUNTIME_DIR` and make the thread runtime
link, router executable, plugin runtime, and implementation resources visible independently of
workspace selection; the fixed helper and configured frontends additionally require their shared
directory on the executor `PATH`.

Startup materializes the validated implementation modules, shared-core adapter and reactor, and dispatch metadata into an
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

For hpatch, the immediate executor carrier contains the root engine's translated patch and
already-rendered final-state report. Response restoration retains the original model-visible
hpatch call and normal executor result for later model-visible history; it does not expose
the translated patch as later model input or derive another report representation. A later
model inference can therefore reuse an exact current row present in the retained successful
report. Router history does not retain hread rows on behalf of the engine, predict later
targets, or move final-reference projection across the root boundary.
