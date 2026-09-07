# Captured Responses metrics

## REQ-METRICS-001 — Captured Responses metrics

`hpatch-router` MUST create one in-process capturer and MUST keep one HTTP listener. The same listener
MUST serve `POST /v1/responses`, `GET /v1/models`, and `GET /api/metrics`. Enabling
`--capture-output PATH` MUST append sanitized schema-6 JSONL records at `PATH`; it MUST NOT start or
require a capturer service, listener, proxy, or network hop.

The same listener MUST serve a human-readable dashboard at `GET /`. The dashboard MUST consume the
capturer snapshot and MUST NOT own counters, histories, classifications, or alternate calculations.
It MUST present every aggregate group plus the retained exchange, provider-attempt, provider-tool,
and delivered-tool detail rather than substituting a reduced dashboard-specific metric set.

The capturer MUST observe both the Codex-facing Responses handler and every provider-facing
Responses or Chat Completions attempt made by that request. Correlation MUST remain process-private and MUST NOT add a
header to either observed request. A provider retry MUST retain the logical request identity and use
attempt numbers `1..N` without gaps.

Observation MUST preserve the routed behavior. It MUST preserve streaming flushes, cancellation,
request and response bytes, response headers and status, provider retry behavior, and response-body
ownership. Raw bodies MUST be discarded after measurement. Response observation MUST retain at most
8 MiB per boundary while continuing to forward and count every byte. Crossing that bound MUST mark
capture health incomplete rather than buffering the remaining content or publishing partial token
and structured-response measurements as complete.

A durable record MUST contain only:

- schema version, boundary, private capture identity, logical sequence, and provider attempt;
- mode, model protocol, provider request model, and benchmark correlation fields already supplied
  by Codex;
- complete transport byte counts and framing-independent GPT-5 content estimates plus the terminal Responses `output` array measured once;
- HTTP and Responses status, completeness, duration, and a bounded capture-error category;
- provider usage counters;
- the measured `native_request` after history replay/tool projection and before CTP, on provider records;
- decoded assistant `final_text` sizes, separate from complete output arrays;
- bounded private request/routing fingerprints as specified below;
- request tool names; and
- tool name, call identity, byte/token sizes, sanitized delivered kind, and an allowlisted stable
  diagnostic reason parsed from the complete router-owned diagnostic envelope.

It MUST NOT contain authorization material, prompts, instructions, message content, tool arguments,
command output, response text, script text, patches, reports, or diagnostics beyond the stable code.

`GET /api/metrics` MUST return `hpatch.capture.metrics.v4`. Its calculations MUST be made by the
capturer, not by the router, engine, plugin, benchmark report, or dashboard. The snapshot MUST expose:

1. logical request and provider-attempt counts, including completed and failed logical requests;
2. provider input, cached input, uncached input, output, reasoning, and usage-bearing attempt counts;
3. the overall provider cache rate from authoritative cached and total provider input, plus cache attribution that separates cold/new uncached input from misses within the immediately
   preceding logical request's final provider attempt for the same nonempty thread; retries within
   one request MUST NOT become cache predecessors, requests without a thread are cold, concurrent
   completions MUST retain request-arrival order, and a final attempt without usage MUST break the
   predecessor chain rather than reuse older evidence;
4. client-request, provider-attempt-request, complete provider-response-stream, and complete
   client-response-stream payload totals, plus terminal provider and client `output` arrays measured once;
   router-generated commentary MUST be excluded from model-origin output accounting by its reserved
   message identity, never by matching text or the commentary phase. This exclusion applies only
   to router-generated client output, not provider output or passthrough responses. All bytes
   remain in transport totals. Chat Completions streams reconstruct their terminal assistant-message
   array and preserve actual function names and argument measurements. Streamed responses whose
   terminal output is empty, omitted, null, or contains only generated commentary MUST reconstruct
   model-origin output in `output_index` order from finalized `response.output_item.done` items,
   excluding generated commentary there as well. A missing terminal event MUST NOT be treated as a
   completed output;
5. signed CTP input byte and token savings between the actual post-replay, post-Hpatch native
   request and its final provider request, never between raw client history and provider input,
   plus signed delivery expansion between their complete model-origin `output` arrays, excluding generated commentary, echoed tools, and all
   other response metadata, so repeated SSE framing and response metadata remain transport evidence
   rather than model-output savings. Tool translation is delivery expansion, not CTP compression
   or a hypothetical stock-model saving. Separate `output_text_tokens_saved` MUST compare only
   decoded assistant `output_text` strings, excluding tool calls and reasoning;
6. provider-emitted and client-delivered tool aggregates;
7. Hpatch call, correction, success, rejection, unmatched, diagnostic, provider-input,
   delivered-carrier-input, and signed delivered-carrier input expansion, not stock-model savings;
8. a bounded recent window of per-logical-request exchanges containing every provider attempt and
   its usage, while cumulative totals remain process-lifetime totals; and
9. capture health for record failures, incomplete records, missing provider records,
   provider-attempt gaps, durable-write errors, skipped requests, and dropped exchange detail.

Provider usage is authoritative for model consumption. Local token estimates MUST count decoded
JSON object keys and scalar values independently, excluding JSON punctuation, field ordering,
whitespace, and string-escape spelling. Equivalent numeric spellings MUST normalize without losing
precision. Arrays retain every element. Strings containing code or JSON remain literal content:
backslashes and escapes inside that content still count. SSE estimates count each decoded event's
content, not event/data framing; repeated events remain stream evidence, not final model output.
Non-JSON text is counted as literal text. Transport byte counts MUST remain exact observed bytes.
These reproducible GPT-5 content estimates include envelope and opaque reasoning values when
present; they MUST NOT be labeled as exact provider input or billed generated tokens.
The Hpatch comparison MUST pair the
actual provider-emitted Hpatch call with the actual delivered native carrier by tool-call identity;
it MUST NOT synthesize an `apply_patch`, `exec_command`, shell command, or stock result.

A benchmark report MUST read these calculations from the snapshot. It MAY independently reconcile
the snapshot against sanitized records and measured result usage, but MUST NOT replace the
capturer's calculations with report-local formulas. A fresh measured arm with any nonzero capture
health error, including dropped detail, MUST fail validation instead of reporting partial evidence
as zero.

Acceptance:

1. A test with one wrapped router listener and a retrying provider observes one logical request,
   consecutive provider attempts, one client record, provider usage, and correlated provider and
   delivered tool calls without retaining private payload text.
2. A streaming test receives the first flushed event before the handler completes.
3. JSON, multiline SSE, and gzip Responses payloads produce the same sanitized observations;
   finalized SSE output items MUST produce the same ordered array when the terminal envelope omits
   them, and any number of nonterminal SSE events contributes exactly one terminal output array to
   protocol output savings. JSON and SSE exclude router-generated usage, operation, runtime, and
   subagent commentary while retaining genuine model commentary, even with identical text.
   The real router/capturer integration MUST prove a telemetry-only terminal array does not hide
   finalized model messages or tool calls, and synthetic commentary changes neither output savings
   nor provider usage.
4. Snapshot totals reconcile their exchanges and provider attempts, and benchmark validation rejects
   changed aggregate usage or nonzero capture-health errors.
5. Passthrough, Hpatch-native, CTP/2, and Mentor Handoff use the same capture owner and endpoint;
   none requires another listener.
6. Cumulative metrics remain complete after the detailed exchange window fills, while health marks
   the discarded detail and benchmark validation rejects it.
7. Arbitrary or malformed `text(...)` carrier content never becomes a durable diagnostic, and a
   response larger than the observation bound preserves delivery while failing capture health.

Schema-6 records and metrics v4 identify this content-token and output-accounting contract. Older records cannot be
reinterpreted as corrected measurements because they do not retain the raw output items; benchmark
validation MUST reject them as current comparison evidence.

JSON whitespace, key order, and equivalent string escaping MUST leave all content estimates unchanged while observed bytes may differ. Literal model-visible escape sequences MUST retain their token cost.

The router supplies the actual native request at the projection seam as observation data. The
capturer owns its measurement, discards the bytes immediately, and correlates the sizes with each
provider attempt. Missing CTP baseline observation MUST mark capture incomplete, never fall back to
client history. Native-only forwarding uses the forwarded request as the identical baseline.
Only paired authoritative provider usage measures actual model-consumption changes. Input CTP
savings and assistant-text CTP savings measure representation changes, not billing predictions.

### Privacy-safe cache diagnostics

Schema-6 records and metrics v4 MAY additionally contain `cache_fingerprint` for observed
request representations, `native_fingerprint` for the actual post-replay/pre-CTP request,
and `client_fingerprint` in exchanges. New captures MUST produce these for valid requests.
The capturer MUST HMAC decoded JSON components and ordered input items with a fresh random
256-bit recorder-lifetime key, retaining only 128-bit digests. It MUST NOT persist that key,
raw content, or raw outgoing routing keys. Fingerprints MUST include a recorder scope;
comparisons across different scopes MUST be unavailable. Equal keys may be correlated only
within that recorder. Field categories MUST come from a fixed allowlist, never arbitrary
user-supplied property names. JSON framing differences MUST NOT change fingerprints.

At most the first 128 input items are retained, with total item count and explicit completeness.
Truncated, malformed, missing, or cross-scope fingerprints MUST NOT claim a stable prefix.
`cache_diagnostics` MUST compare client, native, and final-provider representations against
the immediate same-thread arrival predecessor, and only when that request remains retained and completed.
Requests MUST retain that predecessor sequence even when completions arrive out of order. Arrival
head metadata is bounded to the existing 4096-entry detail limit; evicted head metadata yields
an unavailable comparison, never a guess at an older predecessor.
An unfinished intervening request, failed predecessor, absent thread, or missing observation
MUST break comparison. Retries MUST retain their individual fingerprints and actual outgoing
`Session_id` fingerprints but MUST NOT become logical-request predecessors.

Each stage reports identical, appended, changed, or unavailable, the common leading item count,
and changed fixed field categories. It MUST compare body cache-key and actual outgoing route-key
stability separately. These are observable representation differences, not the provider's hidden
model-token prefix, cache residency, or a guarantee of cache reuse. Reports and the dashboard MUST
show unavailable evidence explicitly and correlate stage changes with authoritative input/cached
usage without labeling inferred shortfalls as proven router-induced cache misses.
Older captures without these additive fields remain valid for their existing metrics but cannot
supply cache-prefix diagnoses. Benchmark validation MUST reconcile retained fingerprints and
independently verify published diagnostic comparisons.
