# Benchmark trust and execution boundary

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
The benchmark owns a separate capturer in `benchmarks/capturer`. One instance places a
front proxy before each router and a back proxy before the provider. It forwards request and response
body bytes unchanged,
streams responses as they arrive, and emits only the content-free correlation, local sequence,
provider-attempt ordinal, duration, model, usage, status, and tool-call fields needed for benchmark proof. A private correlation header exists only between
the two capturer boundaries and is removed before the provider. Per-arm Compose networks make both proxy crossings
mandatory. The router's provider-base configuration selects the back proxy but does not move
authentication, retry, cache-key, or response-transformation ownership out of the router.
The provider-facing capture is the benchmark's provider-attempt usage and cache-attribution boundary.
Router metrics retain operator-facing lifecycle and aggregate usage, Hpatch evidence, and CTP
representation measurements; they do not retain a second benchmark-only provider-request ledger.

Hidden destinations cross into an agent-mutated workspace only through a pinned `*os.Root`
after change capture. A pre-existing destination, lexical escape, or symlink escape fails
instead of overwriting agent or external content. Grader commands consume the resulting
disposable tree; they cannot turn an agent, scope, infrastructure, timeout, or cancellation
failure into benchmark success.
