# Current B baseline

Use **Astra B1, the first current-build Astra run**, as the provisional reference B
for subsequent `etcd-range-stream` observations. It supersedes B2 without changing
historical results or benchmark runner defaults. The known defects below prevent
treating it as authoritative end-to-end correctness or causal performance evidence.

- [Summary](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-VAHgrW/summary.md)
- [Configuration and build identity](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-VAHgrW/benchmark-config.json)
- [Capture metrics](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-VAHgrW/mekugi-metrics.json)
- [Implementation patch](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-VAHgrW/artifacts/etcd-range-stream/etcd-range-stream-mekugi-r001/changes.patch)

## Comparison settings

`gpt-6-astra`, medium effort, CTP/2, `mekugi-diagnostic`, one repetition,
issue reporting disabled, Codex 0.153.4. This is a treatment baseline, not a stock
control eligible for `CONTROL_BASELINE_DIR`.

Task-content SHA-256:
`a047c8afa7ab99dd16110be31aea8a715a1ec90bfe0ed45015bd958f0f0396c0`.

The measured build was `0aa58c4a8d680f3e1a94f85e88ca9353e827fcef`.
The retained build archive, rather than the run directory's commit prefix alone,
identifies the exact measured source:
`fed0d78f20a95efb63e8178e9e68b4f5a18f391179ea417b7b26393af779907d`.

## Recorded result

| Measure | Baseline Astra B1 |
| --- | ---: |
| Required grader | Passed |
| Capture validation | Passed |
| Agent wall time | 230.758 s |
| Input tokens | 410,517 |
| Cached input | 358,400 |
| Uncached input | 52,117 |
| Output tokens | 4,295 |
| Reasoning tokens | 1,279 |
| Provider requests | 13 |
| HPATCH calls | 2 |
| HPATCH rejections / recoveries | 0 / 0 |

Token totals use the capturer aggregate, including the initial setup request.
This single trial is a reference measurement, not a statistical performance guarantee.
The complete result directory is retained locally under the Git-ignored results directory;
it is not included when cloning the repository.

## Known benchmark defects

Known as of 2026-09-12:

- The required grader omits `./server/proxy/grpcproxy`, so it neither compiles nor
  behaviorally checks the production `kvProxy.RangeStream` path required by the prompt.
- Successful hidden tests invoke `EtcdServer.RangeStream` directly rather than through
  the v3rpc and gRPC serving path. The v3rpc tests cover rejection cases and header
  filling separately, so successful streamed header metadata is not verified end to end.
- Hidden coverage omits empty and single-key ranges, limits spanning multiple batches,
  `CountOnly` with `Limit`, compaction after partial output, non-serializable reads,
  context cancellation, successful authenticated streaming, and actual proxy behavior.
- The base workspace has no visible RangeStream tests despite the prompt requiring a
  focused test run. Agents may widen into unrelated package tests that fail under
  localhost isolation, adding request, token, and wall-time noise.
- Separately launched `mekugi-diagnostic` native and CTP/2 runs are not paired evidence.
  Causal CTP comparisons require alternating `ctp-only` arms with multiple repetitions
  and only correctness-passing pairs.
- Sol/high is 0/3 on required grading, so its recorded usage and timing are failure
  diagnostics rather than performance baselines.
- Exact final-response matching adds an instruction-compliance failure independent of
  implementation correctness.

Until these defects are resolved, use this task only as a provisional multi-file
editing stress test. Do not cite its recorded diagnostic runs as proof of complete
RangeStream correctness or of a CTP performance effect.

## Recorded comparison runs

The Astra-only and Sol-only runs below used benchmark commit
`0aa58c4a8d680f3e1a94f85e88ca9353e827fcef`, the same task contract, one
repetition, issue reporting disabled, and Codex 0.153.4. They are comparison
observations, not replacements for the Astra B1 baseline. The linked configuration
and build archive hash identify each exact measured source.

### Astra

- Astra B2: [summary](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-GKBDcf/summary.md),
  [configuration](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-GKBDcf/benchmark-config.json),
  and [implementation patch](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-GKBDcf/artifacts/etcd-range-stream/etcd-range-stream-mekugi-r001/changes.patch).
  Build archive SHA-256: `fed0d78f20a95efb63e8178e9e68b4f5a18f391179ea417b7b26393af779907d`.
- Astra B3, CTP off: [summary](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-SqN1BQ/summary.md),
  [configuration](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-SqN1BQ/benchmark-config.json),
  and [implementation patch](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-SqN1BQ/artifacts/etcd-range-stream/etcd-range-stream-mekugi-r001/changes.patch).
  Build archive SHA-256: `33531b1ac275f354d3b0831076037875c96a37c3191f4bed132ddcfb25f6f8fc`.

| Measure | Astra B2 | Astra B3, CTP off |
| --- | ---: | ---: |
| Model protocol | CTP/2 | Native |
| Required grader | Passed | Passed |
| Capture validation | Passed | Passed |
| Agent wall time | 366.806 s | 265.791 s |
| Input tokens | 731,804 | 432,001 |
| Cached input | 669,056 | 376,064 |
| Uncached input | 62,748 | 55,937 |
| Output tokens | 5,636 | 4,310 |
| Reasoning tokens | 2,031 | 1,229 |
| Provider requests | 20 | 13 |
| HPATCH calls | 3 | 1 |
| HPATCH rejections / corrections | 0 / 0 | 0 / 0 |

### Sol/high

All three Sol/high runs passed capture validation but failed the required grader
because the final response reported revision 22 instead of the pinned or explicitly
requested revision 21.

- Sol/high B1: [summary](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-PJ9HuN/summary.md),
  [configuration](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-PJ9HuN/benchmark-config.json),
  and [implementation patch](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-PJ9HuN/artifacts/etcd-range-stream/etcd-range-stream-mekugi-r001/changes.patch).
  Build archive SHA-256: `fed0d78f20a95efb63e8178e9e68b4f5a18f391179ea417b7b26393af779907d`.
- Sol/high B2: [summary](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-Cw14s8/summary.md),
  [configuration](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-Cw14s8/benchmark-config.json),
  and [implementation patch](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-Cw14s8/artifacts/etcd-range-stream/etcd-range-stream-mekugi-r001/changes.patch).
  Build archive SHA-256: `a4b40aa4fd73f49448c72929b9d605082569bf863b0f9513ee0315aa5c48bfb1`.
- Sol/high B3, CTP off: [summary](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-DwKpON/summary.md),
  [configuration](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-DwKpON/benchmark-config.json),
  and [implementation patch](results/0aa58c4a8d680f3e1a94f85e88ca9353e827fcef-etcd-range-stream-DwKpON/artifacts/etcd-range-stream/etcd-range-stream-mekugi-r001/changes.patch).
  Build archive SHA-256: `33531b1ac275f354d3b0831076037875c96a37c3191f4bed132ddcfb25f6f8fc`.

| Measure | Sol/high B1 | Sol/high B2 | Sol/high B3, CTP off |
| --- | ---: | ---: | ---: |
| Model protocol | CTP/2 | CTP/2 | Native |
| Required grader | Failed | Failed | Failed |
| Capture validation | Passed | Passed | Passed |
| Agent wall time | 355.535 s | 300.404 s | 352.710 s |
| Input tokens | 729,316 | 571,964 | 873,415 |
| Cached input | 634,112 | 514,944 | 811,008 |
| Uncached input | 95,204 | 57,020 | 62,407 |
| Output tokens | 6,897 | 4,840 | 4,948 |
| Reasoning tokens | 3,158 | 1,600 | 1,635 |
| Provider requests | 22 | 16 | 22 |
| HPATCH calls | 6 | 5 | 4 |
| HPATCH rejections / corrections | 2 / 2 | 3 / 2 | 1 / 1 |

### Main mentor handoff, CTP off

This historical run used Astra medium → main Sol high, native protocol, main
handoff enabled, subagent handoff disabled, one repetition, issue reporting
disabled, and Codex 0.153.4. It used the same task contract as the runs above.
It is another provisional observation, not a replacement baseline; the known
grader and benchmark defects above also apply.

- [Summary](results/04f7641037b336d6db345148b39eadb806c62e27-etcd-range-stream-5qUnOg/summary.md)
- [Configuration](results/04f7641037b336d6db345148b39eadb806c62e27-etcd-range-stream-5qUnOg/benchmark-config.json)
- [Metrics](results/04f7641037b336d6db345148b39eadb806c62e27-etcd-range-stream-5qUnOg/mekugi-metrics.json)
- [Implementation patch](results/04f7641037b336d6db345148b39eadb806c62e27-etcd-range-stream-5qUnOg/artifacts/etcd-range-stream/etcd-range-stream-mekugi-r001/changes.patch)

The measured archive included uncommitted main-toggle, Sol-mapping, and benchmark
support changes on top of `04f7641037b336d6db345148b39eadb806c62e27`.
Build archive SHA-256:
`a2c3bc34c2a60616b01cac710a331fdef397ffcab7c4fc2872e76bb39f5d9941`.

| Measure | Main mentor → Sol |
| --- | ---: |
| Required grader | Passed |
| Capture validation | Passed |
| Agent wall time | 271.310 s |
| Input tokens | 303,090 |
| Cached input | 228,608 |
| Uncached input | 74,482 |
| Output tokens | 3,431 |
| Reasoning tokens | 828 |
| Provider requests | 11 |
| HPATCH calls | 1 |
| HPATCH rejections / corrections | 0 / 0 |

Capture recorded five Astra requests, including setup, followed by six Sol requests.
The third Astra shell call completed about 32 seconds after launch. Astra stayed
for the result-consuming response and produced the implementation patch at about
89 seconds. Sol's first request started at about 94 seconds; it handled verification
and the final response. The handoff was triggered by tool count, not the input limit:
Astra's last request had 29,496 input tokens.

This run predates the review fix excluding main prewarm and compaction requests from
handoff. Its 7,887-token setup request was routed to Astra; current behavior leaves
that request on the configured model and does not consume the main schedule.
The figures are preserved historical evidence, not a rerun of the corrected code.
Sanitized capture records actual models, but not the transmitted reasoning-effort
field; Astra medium follows the tested mapping from configured Sol high.

## Estimated token costs

The following estimates use OpenRouter list prices retrieved on 2026-09-12, not
subscription charges or invoices. They follow the session-usage accounting approach:
price each provider request using its actual model, separate cached and uncached
input, and do not charge reasoning again because it is included in output.

| Model | Uncached input / million | Cached input / million | Output / million |
| --- | ---: | ---: | ---: |
| gpt-6-astra | $10.00 | $1.00 | $50.00 |
| gpt-5.6-sol | $2.00 | $0.20 | $10.00 |

Source: [OpenRouter model catalog](https://openrouter.ai/api/v1/models).
The live Sol prices differ from the session-usage fallback table. No request in
these runs reached the 272,000-token long-context pricing threshold. Separate
cache-write usage was not reported; no extra cache-write charge was inferred.
Aggregate token totals, including setup requests, are used consistently.

| Run | Protocol | Required grader | Agent wall time | Estimated token cost |
| --- | --- | --- | ---: | ---: |
| Astra B1 | CTP/2 | Passed | 230.758 s | $1.0943 |
| Astra B2 | CTP/2 | Passed | 366.806 s | $1.5783 |
| Astra B3 | Native | Passed | 265.791 s | $1.1509 |
| Sol/high B1 | CTP/2 | Failed | 355.535 s | $0.3862 |
| Sol/high B2 | CTP/2 | Failed | 300.404 s | $0.2654 |
| Sol/high B3 | Native | Failed | 352.710 s | $0.3365 |
| Main mentor → Sol | Native | Passed | 271.310 s | $0.6708 |

The mentor run's estimate splits into $0.5523 for Astra and $0.1185 for Sol.
Compared descriptively with:

- **Sol/high native:** 23.1% shorter elapsed time, 65.3% less total input, 19.3%
  more uncached input, and 99.4% greater estimated cost. Sol's grading failure
  prevents treating this as a comparison of equally successful implementations.
- **Astra native:** 2.1% longer elapsed time and 41.7% lower estimated cost.
- **Provisional Astra B1 with CTP/2:** 17.6% longer elapsed time and 38.7% lower
  estimated cost; both model scheduling and protocol differ.

These comparisons do not establish a causal performance effect, success rate, or
cost per correct task. They remain subject to the known benchmark defects above.
