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

All runs below used benchmark commit
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
