# Metrics classification and persistence

## CTR-METRICS-001 — Metrics classification and persistence

One metrics classifier consumes structured parser, evaluator, registry, carrier, stock-carrier,
and completed executor-result events for `REQ-METRICS-001`. It does not re-parse tool inputs,
diagnostics, or rendered responses. The translation path owns per-plugin and per-tool definition,
call, emitted-shape, translated-shape, and failed-translation counters. The execution path owns
current and stock result-shape counters from the private worker's validated evidence. Both paths
derive persisted metric token counts and stable identity inside the metrics owner; plugin code
independently counts formatted verified rows only to enforce its output-admission contract and
never supplies those counts as metric evidence or outer carrier serialization. Hpatch's adapter additionally
owns its effective, ineffective, fixed failed semantic baseline, report, command, target, and stable
terminal-reason classifications. The report formatter's exact emitted string is the only source
for report-input token counting. Reduction ratios and signed net input are presentation-time
calculations from
persisted counters.

The metrics store owns tokenizer use, stable tool identity keys, exact installed-definition
totals, executor-result totals, overflow checks, interprocess locking, alternating checksummed
bounded slots, persistence-generation selection, current-version decoding, obsolete-version reset,
and page-cache writeback policy. Translation classification occurs after the router validates a
carrier. Executor-result classification occurs in the private worker after one completed execution
and does not rewrite translation classification. Missing or invalid stock evidence uses the current
shape under `REQ-METRICS-001`. Metrics failure remains auxiliary and cannot change the requested
edit, translated carrier, executor result, state report, or exit status. Router end-to-end Responses
usage remains authoritative for provider-consumed model input.
