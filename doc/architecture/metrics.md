# Capture-owned metrics

## CTR-METRICS-001 — Capture-owned metrics

The opt-in debug artifact bundle is router-owned in `internal/router/debug.go`, not part
of sanitized capture. It reuses capturer exports for capture and metrics, records a selected
instruction snapshot after rewriting, and logs lifecycle/request outcomes without raw errors.
`cmd/hpatch/wrap.go` prints its paths only after the child and router exit.

The root `capturer` subpackage is the sole owner of request correlation, payload measurement,
provider-usage metrics, cache attribution, representation differences, transported-tool accounting, Hpatch
delivery accounting, capture health, durable capture records, and the structured metrics snapshot.
The router's terminal-payload seam parses provider usage once and passes the resulting counts to
the capturer, Mentor Handoff, and user-only usage commentary.

The capturer is in-process. `hpatch` wraps its `POST /v1/responses` handler and
provider `http.RoundTripper` for HTTP Responses and Chat Completions. For
`GET /v1/responses`, that wrapper supplies a context-private factory for
Codex-facing WebSocket exchanges, without counting the upgrade as an inference
request. It observes client and provider WebSocket JSON-message exchanges at
their transport boundaries; it does not start a second HTTP server, open another
listener, or require another process. `GET /api/metrics` serves the capturer snapshot from the same
router listener as Responses and models traffic. The embedded `GET /` dashboard is a presentation
view of that snapshot on the same listener and owns no metric state or calculation.

The client and provider transport observers share a request-scoped, process-private correlation value through
Go context. No correlation header crosses either HTTP boundary. Provider retries receive consecutive
attempt numbers under the same logical request. The wrappers preserve request bytes, response bytes,
stream flushing, cancellation, status, headers, and response-body ownership.

Raw request and response bodies exist only while one boundary is being measured. Durable schema-6
JSONL records contain complete transport lengths, GPT-5 token estimates, one separately measured
terminal Responses `output` array (or reconstructed Chat Completions assistant-message array), statuses, duration, request identity fields
needed for benchmark reconciliation, the passed provider usage, tool names, tool-call identities, and sanitized
Hpatch outcome kinds and allowlisted diagnostic reason codes parsed from the router-owned envelope.
They never retain credentials, prompts, instructions, tool arguments, command output, response text,
translated patches, or reports. Each response boundary retains at most 8 MiB for parsing while
forwarding and byte-counting the complete stream; overflow becomes explicit incomplete health.

The snapshot derives:

- logical requests, provider attempts, completion and failure counts;
- provider input, cached input, uncached input, output, and reasoning usage;
- overall provider cache rate, plus cold/new input and immediately preceding logical-request eligible-prefix cache attribution from
  the final provider attempt by nonempty thread, ordered when requests enter the handler and
  invalidated when that final attempt has no usage;
- complete client and provider transport bytes and GPT-5 token estimates, plus terminal `output`
  arrays measured once independently of SSE event count and echoed response metadata; streaming
  boundaries rebuild empty, missing, null, or generated-commentary-only terminal arrays from
  finalized output items in protocol index order. The capturer excludes only client message items
  in the router-owned namespaces supplied by `internal/commentaryid`; provider and passthrough
  items remain exact. This operates on observed bytes without metric callbacks from the router.
  All generated commentary remains part of complete transport measurement;
- signed post-replay-native-versus-final-provider CTP request savings, assistant-text CTP savings,
  and separately labeled complete-output delivery expansion;
- provider-emitted and client-delivered tool shapes;
- correlated Hpatch calls, corrections, successful and rejected deliveries, unmatched calls,
  diagnostic codes, and signed Hpatch-versus-delivered-carrier input expansion (not stock-model savings);
- a bounded recent window of per-exchange provider attempts and usage, with complete cumulative
  process totals and explicit dropped-detail health; and
- capture, completeness, boundary, sequence, write, and skipped-request health.

Router, edit-engine, CTP, registry, and plugin production code implement behavior only. They do not
maintain hypothetical stock baselines, synthetic stock commands or results, gain counters, metric callbacks,
persistence slots, session metric histories, dashboard-owned calculations, or metric-only
classifier events.
The router passes usage and the actual post-replay, post-Hpatch, pre-CTP request as request-scoped
observation data without receiving metric callbacks. The capturer measures the latter immediately
and retains only sizes and keyed fingerprints. Native-only forwarding supplies its inference request before WebSocket transport framing as the baseline.
Mentor and commentary remain operational consumers, not metrics sources.

Capture failure is auxiliary after startup: it cannot alter an edit, command, translated response,
or provider result. Failure to initialize an explicitly requested capture output prevents startup,
because silently omitting requested evidence would make a benchmark invalid.

Local token estimates count decoded JSON keys and scalar values, not outer JSON framing or escaping. Literal escapes inside content still count. Transport bytes remain exact, and provider usage remains authoritative. Metrics v4/schema-6 evidence is required for this counting contract.

Cache diagnosis also belongs to the capturer. It fingerprints the existing client/native/provider
seams, never adds router callbacks or retains a second raw history. A recorder-private ephemeral
HMAC key makes fingerprints useful for within-run comparison without exposing public prompt
hashes. Snapshot-local comparisons use each request’s recorded immediate arrival predecessor, break at
pending, failed, or evicted predecessors,
and retain the existing 4096-exchange window. Each stage retains at most 128 input-item fingerprints;
partial evidence is unavailable rather than a claim of stability. Benchmark and dashboard code
present these diagnoses; the benchmark independently reconciles them with sanitized observations.

The same capture wrappers privately fingerprint the incoming and outgoing `x-codex-turn-state`
headers. The snapshot compares their forwarding separately from session-key stability, without
owning turn state or changing routing. Missing old evidence is unavailable, not an absent header.

Provider-response evidence also belongs to the capturer. Its existing transport observes only
allowlisted request-ID/model response headers, and its response parser observes envelope model
and terminal cached-token field presence. The router usage callback and normalized counters are
unchanged. Evidence travels with each attempt, without another callback or retained raw response;
snapshot clones isolate its explicit count. Reports distinguish unknown telemetry from explicit
zero and keep provider request IDs out of public summaries.

The capturer also owns final snapshot serialization and offline benchmark session
aggregation. Both reuse the live snapshot and exchange calculations. The benchmark
CLI owns artifact paths and orchestration, not another metric implementation.

WebSocket transport observation lives in `capturer/websocket.go`, alongside the
HTTP observer, not in router metrics callbacks. The router supplies the actual
sent message and received message bytes at the transport seam, and closes that
attempt after terminal usage observation. The capturer owns bounded raw-payload
observation, JSON-message parsing, exact payload lengths, transport labeling,
cache-fingerprint normalization, and sanitized persistence. Router-generated
SSE and reconstructed nonstream JSON belong only to the HTTP Codex boundary.
The dedicated Codex WebSocket path measures restored JSON messages, not its
internal SSE adaptation. Each explicit create or automatic successor receives
its own correlation context; automatic successors have no request payload.
Application-level controls use immediate, sanitized boundary-and-direction
records and separate transport measures. The capturer owns their measurement
and offline aggregation; the router supplies actual wire payloads without
computing metrics or retaining a second control history for capture.

`internal/router/server_websocket.go` owns each downstream session and its
dedicated provider connection, incremental native history, steering lifecycle,
and adaptation through the shared request/response pipeline.
`internal/router/client_websocket.go` owns the HTTP-client connection pool's
leases, credential/routing partitioning, message framing, HTTP fallback
decisions, response-body ownership, and cleanup. That pool retains no
conversation or capture history.
A lease's response body owns its attempt until Close, even after the socket has
received a terminal event. Capturer state never decides whether to reuse a
connection or whether a provider request may be retried.

The connection receiver observes each successful message read before queueing
it for that lease. Queued messages and blocked delivery reservations remain
part of that attempt's evidence. Abort closes the connection and joins its
receiver before capture finalization; successful handoff follows the terminal
receive boundary. Neither delivery backpressure nor early downstream close
can discard bytes that the receiver has already observed.
