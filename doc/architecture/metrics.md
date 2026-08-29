# Capture-owned metrics

## CTR-METRICS-001 — Capture-owned metrics

The root `capturer` subpackage is the sole owner of request correlation, payload measurement,
provider usage, cache attribution, protocol savings, transported-tool accounting, Hpatch delivery
accounting, capture health, durable capture records, and the structured metrics snapshot.

The capturer is in-process. `hpatch-router` wraps its existing `POST /v1/responses` handler and
its existing provider `http.RoundTripper`; it does not start a second HTTP server, open another
listener, or require another process. `GET /api/metrics` serves the capturer snapshot from the same
router listener as Responses and models traffic. The embedded `GET /` dashboard is a presentation
view of that snapshot on the same listener and owns no metric state or calculation.

The client and provider wrappers share a request-scoped, process-private correlation value through
Go context. No correlation header crosses either HTTP boundary. Provider retries receive consecutive
attempt numbers under the same logical request. The wrappers preserve request bytes, response bytes,
stream flushing, cancellation, status, headers, and response-body ownership.

Raw request and response bodies exist only while one boundary is being measured. Durable schema-4
JSONL records contain complete transport lengths, GPT-5 token estimates, one separately measured
terminal Responses `output` array, statuses, duration, request identity fields
needed for benchmark reconciliation, provider usage, tool names, tool-call identities, and sanitized
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
  boundaries rebuild an empty terminal array from finalized output items in protocol index order;
- signed client-versus-final-provider request savings and terminal-output savings;
- provider-emitted and client-delivered tool shapes;
- correlated Hpatch calls, corrections, successful and rejected deliveries, unmatched calls,
  diagnostic codes, and signed Hpatch-versus-delivered-carrier input savings;
- a bounded recent window of per-exchange provider attempts and usage, with complete cumulative
  process totals and explicit dropped-detail health; and
- capture, completeness, boundary, sequence, write, and skipped-request health.

Router, edit-engine, CTP, registry, and plugin production code implement behavior only. They do not
maintain benchmark baselines, synthetic stock commands or results, gain counters, metric callbacks,
persistence slots, session metric histories, dashboard-owned calculations, or metric-only
classifier events.
Provider usage parsing may remain where an operational behavior, such as Mentor Handoff, needs it;
that behavior state is not a metrics source.

Capture failure is auxiliary after startup: it cannot alter an edit, command, translated response,
or provider result. Failure to initialize an explicitly requested capture output prevents startup,
because silently omitting requested evidence would make a benchmark invalid.
