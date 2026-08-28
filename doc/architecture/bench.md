# Benchmark trust and execution boundary

## CTR-BENCH-001 — Benchmark trust and execution boundary

The benchmark runner owns historical workspace creation, instruction preparation, arm scheduling,
network isolation, Codex invocation, pre-grader artifact capture, hidden grading, result retention,
and report validation. Agents cannot reach the historical oracle or hidden grader before their
changes are captured.

Each fresh arm has one `hpatch-router` process and one router listener. Codex connects directly to
that listener. The router connects directly to the provider. The root `capturer` package observes
both boundaries in-process and writes the arm's sanitized JSONL. The benchmark never inserts a
capturer proxy or service and never needs three servers for one router.

Separate internal agent networks prevent either agent from reaching the other arm's router. Each
router alone also joins the egress network. Capture files are mounted directly into the router and
`GET /api/metrics` is collected from that same listener after attempts finish.

The capturer snapshot is the authoritative calculation surface. Reporting validates raw records,
snapshot exchange totals, capture health, and result-reported per-thread provider usage before it
formats provider usage, cache attribution, payload savings, actual provider and delivered tool
shapes, Hpatch delivery, model attribution, and completeness. It never reconstructs a hypothetical
stock command, result, or patch.

Fresh two-arm modes require both arms' current capture and snapshot. The validator binds each root
thread to its configured model, binds Mentor child threads to retained child proofs, rejects model
traffic outside the allowed schedule, and rejects any unproved thread. Configured CTP/2 compression
requirements use the capturer's signed end-to-end protocol totals and fail after preserving the
summary when required input or output savings are not positive.

Arm labels are evidence-backed: the validator requires passthrough/native for control,
Hpatch/native for Hpatch, native-protocol, and Mentor arms, and Hpatch/CTP2 for the CTP arm. Raw
records must agree with the snapshot. Model reporting groups every provider attempt and separately
counts attempts carrying provider usage.

Hidden tests and the allowed-path boundary decide correctness. Timing and token comparisons are
interpreted only after correctness. CTP/2 and Mentor Handoff use the same capture and reporting
path as paired and diagnostic runs; their distinct model/protocol behavior is visible in exchange
models, protocol savings, and provider usage rather than production metric callbacks.
