# Historical benchmark contract

## REQ-BENCH-001 — Containerized paired correctness evaluation

`benchmarks/bench.sh` owns the executable benchmark workflow. Dockerfile and
Docker Compose own the agent and router topology; the shell directly invokes
Docker, Codex, Git, the Go test grader, metrics endpoints, and `hpatch gain`.
No Go or Python benchmark runner or process-execution abstraction is part of the
benchmark.

All configured repetitions run concurrently; the default is four. Within each
repetition, one control and one hpatch attempt run sequentially, with odd and
even repetitions alternating which arm runs first. Both attempts use
the same pinned Codex image, model, prompt, historical etcd base, full-access
Codex sandbox setting, approval policy, timeout, authentication mount,
Codex-owned workspace metadata, and disposable-container setup.

Before either arm starts, a setup-only container downloads Go dependencies from a
history-free base snapshot into a temporary module cache. The workflow rejects the cache if
it contains downloadable copies of benchmark-owned etcd modules, then mounts it read-only
into both agent environments with
`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, and `GOVCS=*:off`. A missing dependency
therefore fails locally instead of reopening network access or exposing another etcd revision.

Control and hpatch agents run on distinct internal Docker networks. Each network contains only
its agent and assigned router; the routers separately attach to an egress network for model API
access. The agent has no host networking, cannot resolve the other arm's router, and cannot
reach external HTTP endpoints. Before model execution, the workflow proves from each exact
agent service that its assigned router is reachable, the other router and an external endpoint
are unreachable, no conventional MCP server is configured, and `go mod download all`
succeeds using only the read-only cache. Every measured Codex invocation also disables the
default-on `apps` feature, preventing the first-party app/connector MCP transport from starting.

The control uses the pinned CLI's stock bundled base instructions and the
passthrough router. The treatment replaces only the stock file-editing guidance
with `contrib/codex/file-editing-instructions.md` and uses the hpatch router.
Both generated base instructions append the same offline-isolation rule, and the
task prompt repeats it: agents must use only the workspace, visible prompt, local
toolchain, and visible tests, must not inspect the module cache for an implementation,
and must not seek oracle revisions, hidden tests, other-arm artifacts, upstream material,
or other external resources.
The complete control and treatment instructions, their hashes, and their unified
diff are retained in the run directory and referenced by each result.

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
passes. A failed task attempt is recorded as benchmark data and does not cancel
the other arm or other repetitions; the run completes all scheduled attempts and
exits nonzero after collecting their evidence. User cancellation still terminates
active workers through the evidence-preserving cleanup path. Failures retain the
available diff, Codex diagnostics, grader diagnostics, and router evidence.

Each concurrent attempt writes one private JSON object containing arm and pair
order, selected base instruction artifact and digest, elapsed time, token usage,
Codex item counts and terminal error, changed and unauthorized paths, literal
diff and diff path, grader outcome, and the final pass decision. The parent
deterministically merges all `2 * repetitions` objects after a complete run, or
all retained objects after external cancellation, into `results.jsonl`.

After the paired attempts, the shell retains control and treatment router
metrics and an isolated treatment `hpatch gain` report. A gain of zero is not an
editing-performance result when no treatment request reached hpatch.
The generated summary joins each attempt's thread ID to router session metrics and reports
model requests, command executions, hread calls, client-visible file-change items, routed
hpatch translations and rejections, and rejected-call diagnostic tokens. It separately shows
semantic edit-payload reduction, end-to-end agent output change, and estimated non-edit
output so payload savings cannot be mistaken for whole-agent savings. A client stderr
translation envelope is labeled separately from an hpatch command rejection. When the router
artifact supports it, the summary lists bounded attempt sequences including correction scope,
value-row operation count, base-body rows, and base-command tokens. It lists evaluator
rejection evidence by repetition, command, physical source line, operation, target kind,
multiline value row, stable reason, path, and generated Go line and column when applicable.
An older artifact without either bounded collection is labeled unavailable rather than
reported as zero activity. When lifetime routed-call counters exceed retained attempts,
retention-dependent correction-adoption and recovery measures are labeled unavailable rather
than reported as full-run rates.

Acceptance:

1. Base qualification fails with `missing method RangeStream`; oracle
   qualification passes the identical hidden grader.
2. Both arms receive independent byte-equivalent base snapshots and exactly the
   same non-editing setup.
3. The treatment base instruction is derived from the stock prompt using the
   repository-owned replacement, and the retained diff contains no unrelated
   base-instruction changes.
4. Hidden tests are absent during agent execution and injected only after changed
   paths and the literal agent diff are captured.
5. A timeout, Codex failure, invalid JSONL, unauthorized path, or hidden-grader
   failure makes the attempt fail without deleting its evidence.
6. Results retain elapsed/token metrics and the actual agent diff; treatment
   artifacts retain structured router gain metrics and textual `hpatch gain`.
7. The summary attributes routed hpatch outcomes to their originating repetitions,
   distinguishes client file-change items from routed calls, and separates semantic
   edit-payload estimates from end-to-end output usage. It reports structured evaluator
   rejection evidence and correction-scope attempt telemetry when present, and identifies
   artifacts that predate those bounded records.
8. Agents cannot retrieve upstream implementation or oracle material at runtime: per-arm
   internal networks expose only the assigned router, while the setup-only dependency loader
   is the sole benchmark component allowed to populate the sanitized read-only module cache.
9. Isolation qualification fails the run before either arm starts unless both agents can reach
   only their assigned router and compile-time module resolution succeeds with network-backed
   Go resolution disabled.
