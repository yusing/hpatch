# Containerized correctness and capture benchmark

## REQ-BENCH-001 — Containerized paired correctness evaluation

The benchmark MUST create independent historical workspaces for its measured arms, hide oracle and
grader material until after agent changes are captured, enforce the allowed changed-path boundary,
and run the hidden executable grader. Correctness MUST take precedence over performance reporting.

Supported modes are:

- `paired`: passthrough control versus Hpatch with alternating order;
- `hpatch-only`: one fresh Hpatch attempt against an explicitly matching published control result;
- `hpatch-diagnostic`: one fresh Hpatch attempt without a control arm;
- `ctp-only`: Hpatch native protocol versus Hpatch CTP/2 with alternating order; and
- `mentor-handoff`: Hpatch versus Hpatch with the bounded mentor model schedule.

Each fresh arm MUST run exactly one router process with exactly one listener. The agent MUST connect
directly to that listener, and the listener MUST expose Responses plus `/api/metrics`. The router
MUST connect directly to the provider. No standalone capturer process, proxy, listener, or Compose
service is permitted.

The runner MUST pass `--capture-output` to each router and mount a private arm-specific capture
path. Its isolated networks MUST prevent either agent from reaching the other arm's router while
allowing only its router provider egress. The collected `control-metrics.json` and
`hpatch-metrics.json` (or mode-specific equivalents) MUST come from the same listeners used by the
agents.

For every fresh arm, report generation MUST:

1. require `hpatch.capture.metrics.v2` and schema-4 sanitized records;
2. reject empty capture, capture errors, incomplete records, boundary mismatches, duplicate client
   records, missing provider records, attempt gaps, write failures, skipped requests, and dropped
   exchange detail;
3. reconcile raw record count with capture health, provider attempts with exchanges, aggregate
   provider usage with exchange attempts, and each measured root thread's usage with
   `results.jsonl`;
4. use capturer-owned provider usage and cache attribution for all model-consumption totals;
5. report signed client-versus-final-provider request savings and terminal-output-array savings,
   excluding echoed response metadata while reporting complete response-stream transport separately;
6. report actual provider-emitted and client-delivered tool shapes, including correlated Hpatch
   success, rejection, correction, unmatched, diagnostic, and carrier-token totals;
7. report actual provider models from exchanges, including parent and child traffic; and
8. omit request, session, thread, tool-call, and capture identities from `summary.md`.

Paired, CTP/2, and Mentor Handoff reports MUST require current baseline and treatment capture plus
snapshots; a missing, empty, or wrong-schema baseline MUST fail. Hpatch-only and diagnostic modes
are the only modes that MAY omit a fresh baseline. Each root thread's provider attempts MUST use its
configured parent model. Mentor child traffic MUST match a retained child proof, use only its
configured child model in the baseline, use only the child or mentor model in the treatment, and
include at least one mentor-model request in the treatment. Any unproved thread MUST fail validation.

When a CTP task requires input or output compression, report generation MUST evaluate the matching
signed client-versus-provider token savings from the CTP capturer snapshot. A required direction
MUST be positive. Failure MUST retain the measured value in `summary.md` and make the benchmark exit
nonzero.

The report validator MAY recompute exchange sums only to prove that the snapshot is internally
consistent. The summary MUST format the snapshot's values and MUST NOT own alternate metric,
cache, gain, or synthetic-stock calculations. Negative savings MUST remain negative, provider
retries MUST not be counted as new logical requests, retry usage MUST not be discarded, and model
reporting MUST include provider attempts without usage while distinguishing usage-bearing attempts.

The validator MUST bind each arm to its router configuration: `control` is passthrough/native;
`hpatch`, `native`, and both Mentor arms are Hpatch/native; and `ctp` is Hpatch/CTP2. Every raw record
MUST agree with its snapshot mode and protocol. Self-consistent evidence from the wrong configuration
MUST fail before it receives a treatment label.

The benchmark MAY retain command and file-change events for structural loop analysis and agent issue
reports for diagnostics. Those artifacts are behavioral evidence, not metric inputs. It MUST NOT ask
production engine, router, CTP, registry, or plugin code to emit benchmark-only evidence.

An imported historical control without the current capture schema MAY supply correctness context,
but its old metrics MUST NOT be combined with a fresh capture or presented as current authoritative
metrics.

Acceptance:

1. Compose defines one control router and one Hpatch router, with no capturer services; each router
   has one `--listen` and one `--capture-output`.
2. Report fixtures prove provider usage, signed arm deltas, cache values, protocol savings, Hpatch
   delivery, and zero capture-health errors, and reject altered aggregate usage, incomplete
   evidence, absent baseline evidence, wrong router mode or protocol, wrong provider models, and
   failed required CTP compression.
3. Paired, CTP/2, Mentor Handoff, Hpatch-only, and diagnostic scheduling reuse the same capture
   owner and report schema.
4. A failed attempt or infrastructure check retains available artifacts and returns nonzero.
