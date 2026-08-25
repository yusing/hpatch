# Historical benchmark contract

## REQ-BENCH-001 — Containerized paired correctness evaluation

`benchmarks/bench.sh` owns the executable benchmark workflow. Dockerfile and
Docker Compose own the agent and router topology; the shell directly invokes
Docker, Codex, Git, the Go test grader, and metrics endpoints.
No Go or Python benchmark runner or process-execution abstraction is part of the
benchmark.

The default paired mode runs all configured repetitions concurrently; the default is four.
Within each repetition, one control and one hpatch attempt run sequentially, with odd and even
repetitions alternating which arm runs first. Hpatch-only mode accepts exactly one repetition,
runs only the treatment model attempt, and imports one matching passing control record and
control metrics from a complete published baseline. The imported record is labeled with its
source summary and is never counted as a new model attempt. Both modes use the same pinned Codex
image, model, prompt, historical etcd base, full-access Codex sandbox setting, approval policy,
timeout, authentication mount, and disposable-container setup.

CTP-only mode runs configured repetitions of two fresh Hpatch attempts: `native` and `ctp`. Each
repetition MUST use independent copies of the same task base and MUST run its attempts
sequentially, rotating their order across repetitions. Repetitions MAY run concurrently. The native
arm uses Hpatch guidance with the complete CTP section removed and a native router. The ctp arm uses
the complete CTP guidance and an Hpatch router with `--model-protocol ctp1`. Issue
reporting MUST be disabled in this mode. Every task used by this mode MUST declare one exact decoded
final response, and both arms MUST be graded against it in addition to the hidden executable
grader. No historical result is imported.

Before either arm starts, a setup-only container downloads Go dependencies from a
history-free base snapshot into a temporary module cache. The workflow rejects the cache if
it contains downloadable copies of benchmark-owned etcd modules, then mounts it read-only
into both agent environments with
`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, and `GOVCS=*:off`. A missing dependency
therefore fails locally instead of reopening network access or exposing another etcd revision.
A Git-backed task whose hidden grader exercises test-only dependencies may set
`runtime.preload_go_qualification_grader` to compile its declared `go test` grader in
setup-only base and oracle snapshots before hidden tests are injected. The runner rejects that
option for other source or grader kinds. This task-owned opt-in populates the exact qualification
dependency graph without exposing hidden test source or adding a second package list. Only the
base source snapshot is mounted into agent environments, and the cache rejection for
benchmark-owned etcd modules applies after both preloads.

Control and hpatch agents run on distinct internal Docker networks. Each network contains only
its agent and assigned router; the routers separately attach to an egress network for model API
access. The agent has no host networking, cannot resolve the other arm's router, and cannot
reach external HTTP endpoints. Before model execution, the workflow proves from each exact
agent service that its assigned router is reachable, the other router and an external endpoint
are unreachable, no conventional MCP server is configured, and `go mod download all`
succeeds using only the read-only cache. Every measured Codex invocation also disables the
default-on `apps` feature, preventing the first-party app/connector MCP transport from starting.

In the default paired mode, the control uses the pinned CLI's stock bundled base instructions
and the passthrough router. At the stock file-editing section, the treatment installs the
edit, read, search, and shell guidance from
`contrib/codex/file-editing-instructions.md` and uses the hpatch router. The
treatment also removes exactly one pinned stock `rg` preference line and exactly
one pinned stock `exec_command` guidance line; the control retains both. Every
other stock base-instruction line remains byte-for-byte. Both generated base
instructions append the same offline-isolation rule, and the task prompt repeats
it: agents must use only the workspace, visible prompt, local toolchain, and
visible tests, must not inspect the module cache for an implementation, and must
not seek oracle revisions, hidden tests, other-arm artifacts, upstream material,
or other external resources. The complete control and treatment instructions,
their hashes, and their unified diff are retained in the run directory and
referenced by each result.

For CTP-only mode both arms MUST receive the same pre-router instructions and retain the same
instruction digest. The native-protocol Hpatch router MUST inject the central guidance without its
`## CTP/1 transport` section; the separate CTP/1 Hpatch router MUST inject the complete source.
Both agent environments MUST share the router-owned shell runtime with their assigned router and
remain isolated from the other router and external networks.

The active etcd task exports base revision
`84e612f39b82d1c8ee3f884a59e3f973209d8fbc` and oracle revision
`fd2cc937c9d4413a410d36eb340d83981535b00f`. Before model execution, the shell
must prove that the hidden RangeStream tests fail on the base with the expected
missing-method discriminator and pass on the oracle. The grader asserts observable
behavior rather than requiring the oracle's private helper names or internal
streaming decomposition.

Every attempt receives a fresh history-free base repository without hidden
tests, oracle objects, or a remote. After Codex exits, the shell records changed
and untracked paths and writes the literal binary-capable diff. It then injects
the hidden tests and grades the attempt. Only the eight production paths declared
by the task manifest may change.

An attempt passes only when Codex exits successfully without timeout, all
changed paths are allowed, Codex JSONL can be recorded, and the hidden grader
passes. When the task declares `expected_final_response`, the last completed decoded assistant
message MUST equal that string byte-for-byte and contributes a separate required grader result.
A failed task attempt is recorded as benchmark data and does not cancel
the other arm or other repetitions; the run completes all scheduled attempts and
exits nonzero after collecting their evidence. User cancellation still terminates
active workers through the evidence-preserving cleanup path. Failures retain the
available diff, Codex diagnostics, grader diagnostics, and router evidence.

Each measured attempt writes one private JSON object containing arm and repetition order, selected
base instruction artifact and digest, elapsed time, token usage, Codex item counts and terminal
error, model-turn and unified-exec process-creation failure counts, changed and unauthorized paths,
literal diff and diff path, grader outcome, and final
pass decision. Newly measured records also identify the router mode and model protocol. The
paired parent deterministically merges all `2 * repetitions` objects.
Hpatch-only mode merges its one measured treatment object with the labeled imported control
object. CTP-only mode merges all `2 * repetitions` fresh objects. External cancellation merges all
retained objects.

After measured attempts, the shell retains treatment router metrics, including structured gain.
Paired mode also records fresh control router metrics; Hpatch-only mode
copies the matching baseline control metrics. A gain of zero is not an editing-performance
result when no treatment request reached hpatch.
In CTP-only mode both metric files describe fresh Hpatch routers. Control metrics contain the
native sessions from the native-protocol router; treatment metrics contain active CTP/1
sessions. The report MUST join each record to its exact router session instead of using the
multi-session router aggregate as one arm.
Treatment runs expose `report_issue` by default and MUST accept an explicit boolean environment
toggle that removes the tool. When enabled, the benchmark MUST install one diagnose hook in the
treatment router container, retain each completed report in a unique run-local atomic destination,
and consolidate exact report titles and Markdown without adding agent or transport identity.
Treatment instructions MUST require one report after each rejected-call recovery chain so a
recovering agent cannot silently omit the evidence the diagnostic run exists to collect.
Concurrent attempts and benchmark invocations MUST NOT share report destinations. The summary
MUST report the toggle state and collected count but MUST leave report bodies in the retained
machine-readable artifact.
Hpatch-only and hpatch-diagnostic runs MUST accept an explicit, default-disabled exact-evidence
toggle. When enabled, the treatment router MUST retain only completed `hpatch` and
`hpatch_recover` attempts, with the exact decoded model payload, exact successful report, and
exact final rendered diagnostic returned to the model. Records MUST include session, chain, call, attempt,
tool, correction, model, and outcome identity plus byte lengths and SHA-256 digests for each
exact string. They MUST exclude request headers, credentials, shell and plugin traffic,
router-rebuilt scripts, and translated patches. Normal routing MUST allocate or
write no exact-evidence record when the toggle is disabled.

Each completed attempt MUST publish one mode-0600 JSON object through a unique same-directory
temporary file and atomic rename inside a run-local mode-0700 directory. Concurrent benchmark
invocations and attempts MUST NOT share that directory. After routing stops, the runner MUST
validate the schema, exact UTF-8 byte lengths and digests, and unique session/call identities,
then deterministically consolidate the records into `hpatch-exact-evidence.jsonl`. Recorder
failures remain auxiliary to tool behavior, while missing or invalid enabled benchmark evidence
fails evidence collection. Cancellation may retain fewer recorded attempts than routed calls;
the summary MUST label both retained fractions instead of fabricating completeness. Payloads,
reports, diagnostics, and their private identities MUST remain outside `summary.md`.
When exact evidence is enabled, `report.sh` MUST join the retained records to Hpatch attempt
telemetry by session and call identity, order them by telemetry sequence, and emit deterministic
aggregate reliability and byte-cost rows. Those rows include initial, rejected, and correction
payload bytes, rendered diagnostic and report bytes, chain recovery on the first correction, and
conservative counts of correction target/value fragments that exactly occur in the immediately
preceding diagnostic. Fragment counts MUST NOT retain or expose the fragments themselves and
MUST NOT be described as evidence of model cognition. A record whose identity does not exactly
match telemetry MUST fail analysis rather than being silently attributed.
The generated summary MUST report model requests, correctness and token deltas, parsed command
invocations, edit-round structure, routed Hpatch success/rejection/correction totals, grouped
rejection causes, and semantic edit-payload reduction. Command categories MUST distinguish file
reads, search, discovery, content `git diff`, `git diff --check`, diff metadata, `git status`,
tests/builds, formatters, and upstream fetches. Counts after an edit MUST be separate. For file
reads, searches, and content `git diff` commands, the
summary MUST parse concrete path operands separately from patterns and option values. Supported
positive basename `--glob` filters on hgrep and ripgrep MUST narrow directory coverage equally;
unsupported glob forms MUST remain conservative. The summary MUST otherwise interpret a directory
operand as covering its descendant paths and report a same-path structural loop when a concrete
file or directory operand covers a path in completed file-change events both before and after the
command. It MUST keep prior-changed paths with no
later change separate so terminal validation does not count as a loop. Unresolved operands MUST
remain ambiguous instead of being inferred. A bare worktree `git diff` has workspace-wide scope;
it MUST count as a structural loop when any path has completed file changes both before and after
the command, and MUST be reported separately from explicit path operands. Compound shell items MUST contribute each parsed
invocation rather than one event-row count. The summary MUST automate findings for same-path
edit-read/search/content-diff-edit structural loops, recovery, repeated rejection signatures,
repeated rejected attempts on the same command, operation, target kind, and path, task success,
and request/input/output deltas. It MUST NOT emit
session, thread, tool-call, or correlation identifiers. Detailed transport metrics, raw attempts,
paths, and gain output remain available in retained machine-readable artifacts instead of being
duplicated in the primary summary. When bounded attempt retention is incomplete, the summary MUST
label the retained fraction rather than infer full-run rates.
The summary MUST preserve provider input and cached-input totals. When terminal router logs contain
request-level token attribution, it MUST also split provider uncached input into cold or newly
appended input and misses within the immediately preceding request's eligible prefix. For a warm
request, that eligible prefix is the smaller of its input-token count and the preceding request's
input-token count; cached tokens up to that size are hits. The derived buckets MUST reconcile
exactly to provider uncached input, and the report MUST render their aggregate eligible-prefix
cache rate. An imported historical control whose retained log predates request-level token
attribution MUST show the derived values as unavailable rather than fabricate them. Missing or
partial attribution for a newly measured session MUST fail report generation.

For CTP-only results, the summary MUST verify exactly one fresh native and active record per
repetition, unique order values, the native protocol for native, CTP/1 for active, and identical
pre-router instruction digests. Every arm MUST contain a
hidden required grader and the exact decoded-response grader. Imported-baseline provenance is not a
valid substitute for any arm.

The report MUST join each attempt to its router session and reject missing sessions, missing provider
usage, dropped request or CTP observations, or per-request usage totals that do not exactly reconcile
with their session aggregate. It MUST separate three evidence layers:

1. Model performance reports hidden correctness, exact decoded-response correctness, overall task
   acceptance, median wall time, model requests, agent turns, reasoning tokens, and completed shell
   and file-change items for both arms. It separately attributes model-turn, Codex executor process-creation,
   router failure and timeout, Hpatch rejection, and CTP decode-failure counters without removing any
   of them from the operational outcome.
2. CTP performance reports every active admission decision and each active request's native and
   compact tokens and UTF-8 bytes, request dictionary definitions and bytes, and encode time. It also
   reports each logical assistant text's native and compact tokens and bytes and response dictionary
   size, plus aggregate encode/decode operations, time, and decode failures. Input covers complete
   post-Hpatch requests; output excludes tools and uses terminal `response.output_text.done` events for
   streaming responses.
3. Operational provider usage reports total, cached, and uncached input, output and reasoning output,
   requests, and wall time for both arms. It MUST show active-minus-native deltas, individual attempt
   rows, and individual provider request rows. Provider
   usage observed before response transformation is authoritative. The report MAY label Pareto
   dominance only after correctness, and MUST label tradeoffs as mixed. Token components are a cost
   basis; the report MUST NOT invent dollar pricing without an authoritative configured price source.

Missing or inconsistent CTP counters MUST fail the report. The report MUST account for every
considered CTP request as admitted, missing an instruction carrier, having no positive definition, or
having a complete representation that was not smaller. When no request admits CTP, ratios are
unavailable rather than zero or baseline-derived. The summary MUST use local request ordinals and MUST
NOT emit session, thread, tool-call, correlation, or global router request identifiers. It MUST not
present stock-control or Hpatch-editing findings for this CTP comparison.
A task manifest MAY set the boolean fields `ctp.require_input_compression` and
`ctp.require_output_compression`. The runner MUST record those requirements in
`benchmark-config.json`. An enabled input requirement passes only when at least one request is
admitted and its compact token estimate is smaller than its native estimate. An enabled output
requirement passes only when at least one assistant text is observed and the provider-emitted compact
text is smaller than the restored native text. The report MUST retain and label a failed requirement,
and the benchmark MUST exit unsuccessfully after preserving its evidence.
Unless `BENCHMARK_ENFORCE_NO_EDIT_LOOPS=false`, a measured run with any same-path file-read,
search, or content-diff loop MUST retain its artifacts and exit unsuccessfully after report
generation. CTP-only mode applies this acceptance check to both measured Hpatch arms.
Hpatch-diagnostic mode MUST generate the same Hpatch reliability and command analysis without
inventing control values; its summary MUST label that no control arm ran.

Acceptance:

1. Base qualification fails with `missing method RangeStream`; oracle
   qualification passes the identical hidden grader.
2. Every arm in a comparison receives an independent byte-equivalent base snapshot and exactly the
   same non-editing setup.
3. The treatment base instruction is derived from the stock prompt using the
   repository-owned replacement, and the retained diff contains no unrelated
   base-instruction changes.
4. Hidden tests are absent during agent execution and injected only after changed
   paths and the literal agent diff are captured.
5. A timeout, Codex failure, invalid JSONL, unauthorized path, or hidden-grader
   failure makes the attempt fail without deleting its evidence.
6. Results retain elapsed/token metrics and the actual agent diff; treatment
   artifacts retain structured router gain metrics.
7. The summary attributes routed hpatch outcomes to their originating repetitions,
   distinguishes client file-change items from routed calls, and separates semantic
   edit-payload estimates from end-to-end output usage. It reports structured evaluator
   rejection evidence and payload-mode attempt telemetry when present, and identifies
   artifacts that predate those bounded records.
8. Agents cannot retrieve upstream implementation or oracle material at runtime: per-arm
   internal networks expose only the assigned router, while the setup-only dependency loader
   is the sole benchmark component allowed to populate the sanitized read-only module cache.
9. Isolation qualification fails the run before model execution unless each agent environment can reach
   only their assigned router, compile-time module resolution succeeds with network-backed Go
   resolution disabled, and every measured Codex invocation disables `apps`.
10. CTP-only mode runs fresh native and active Hpatch attempts; native omits only CTP guidance,
    active enables CTP/1, and both arms satisfy the same decoded final response contract.
11. A task that opts into CTP input or assistant-output compression fails unless every required
    direction has observed text and a strictly smaller compact representation.
12. Each CTP attempt joins to complete per-request provider and CTP observations, and the report
    separates model behavior, protocol mechanics, and end-to-end operational usage.
