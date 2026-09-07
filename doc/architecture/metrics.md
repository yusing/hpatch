# Capture-owned metrics

## CTR-METRICS-001 — Capture-owned metrics

The root `capturer` subpackage is the sole owner of request correlation, payload measurement,
provider-usage metrics, cache attribution, representation differences, transported-tool accounting, Hpatch
delivery accounting, capture health, durable capture records, and the structured metrics snapshot.
The router's terminal-payload seam parses provider usage once and passes the resulting counts to
the capturer, Mentor Handoff, and user-only usage commentary.

The capturer is in-process. `hpatch-router` wraps its existing `POST /v1/responses` handler and
its existing provider `http.RoundTripper` for Responses and Chat Completions; it does not start a second HTTP server, open another
listener, or require another process. `GET /api/metrics` serves the capturer snapshot from the same
router listener as Responses and models traffic. The embedded `GET /` dashboard is a presentation
view of that snapshot on the same listener and owns no metric state or calculation.

The client and provider wrappers share a request-scoped, process-private correlation value through
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
and retains only sizes and keyed fingerprints. Native-only forwarding supplies the same request as its own baseline.
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
