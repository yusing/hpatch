# Captured Responses metrics

## REQ-METRICS-001 — Captured Responses metrics

`hpatch-router` MUST create one in-process capturer and MUST keep one HTTP listener. The same listener
MUST serve `POST /v1/responses`, `GET /v1/models`, and `GET /api/metrics`. Enabling
`--capture-output PATH` MUST append sanitized schema-4 JSONL records at `PATH`; it MUST NOT start or
require a capturer service, listener, proxy, or network hop.

The same listener MUST serve a human-readable dashboard at `GET /`. The dashboard MUST consume the
capturer snapshot and MUST NOT own counters, histories, classifications, or alternate calculations.
It MUST present every aggregate group plus the retained exchange, provider-attempt, provider-tool,
and delivered-tool detail rather than substituting a reduced dashboard-specific metric set.

The capturer MUST observe both the Codex-facing Responses handler and every provider-facing
Responses attempt made by that request. Correlation MUST remain process-private and MUST NOT add a
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
- complete transport payload byte and GPT-5 token counts plus the terminal Responses `output` array measured once;
- HTTP and Responses status, completeness, duration, and a bounded capture-error category;
- provider usage counters;
- request tool names; and
- tool name, call identity, byte/token sizes, sanitized delivered kind, and an allowlisted stable
  diagnostic reason parsed from the complete router-owned diagnostic envelope.

It MUST NOT contain authorization material, prompts, instructions, message content, tool arguments,
command output, response text, script text, patches, reports, or diagnostics beyond the stable code.

`GET /api/metrics` MUST return `hpatch.capture.metrics.v2`. Its calculations MUST be made by the
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
   streamed responses whose terminal envelope has an empty `output` array MUST reconstruct it in
   `output_index` order from finalized `response.output_item.done` items;
5. signed input byte and token savings between each client request and the final provider request,
   plus signed output savings between their terminal `output` arrays, excluding echoed tools and all
   other response metadata, so repeated SSE framing and response metadata remain transport evidence
   rather than model-output savings and negative provider-boundary expansion remains visible;
6. provider-emitted and client-delivered tool aggregates;
7. Hpatch call, correction, success, rejection, unmatched, diagnostic, provider-input,
   delivered-carrier-input, and signed saved-input totals;
8. a bounded recent window of per-logical-request exchanges containing every provider attempt and
   its usage, while cumulative totals remain process-lifetime totals; and
9. capture health for record failures, incomplete records, missing provider records,
   provider-attempt gaps, durable-write errors, skipped requests, and dropped exchange detail.

Provider usage is authoritative for model consumption. Payload token counts are reproducible GPT-5
estimates used only for exact observed transport or terminal-output comparisons. The Hpatch comparison MUST pair the
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
   protocol output savings.
4. Snapshot totals reconcile their exchanges and provider attempts, and benchmark validation rejects
   changed aggregate usage or nonzero capture-health errors.
5. Passthrough, Hpatch-native, CTP/2, and Mentor Handoff use the same capture owner and endpoint;
   none requires another listener.
6. Cumulative metrics remain complete after the detailed exchange window fills, while health marks
   the discarded detail and benchmark validation rejects it.
7. Arbitrary or malformed `text(...)` carrier content never becomes a durable diagnostic, and a
   response larger than the observation bound preserves delivery while failing capture health.
