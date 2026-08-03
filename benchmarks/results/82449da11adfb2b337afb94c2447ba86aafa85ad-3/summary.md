# Hpatch-only benchmark summary

This run contains one `gpt-5.6-luna` repetition at `xhigh` reasoning effort. It intentionally has no control arm.

## Outcome

| Metric | Hpatch |
|---|---:|
| Task pass | yes |
| Unauthorized paths | 0 |
| Grader | passed in all 6 packages |
| Agent duration | 782.098 s |
| Requests | 49 |
| Agent commands | 35 |
| Input tokens | 5,756,359 |
| Cached input tokens | 5,522,432 |
| Uncached input tokens | 233,927 |
| Output tokens | 34,395 |
| Reasoning output tokens | 14,194 |
| Successful Hpatch calls | 2 |
| Rejected Hpatch calls | 6 |
| Hpatch command invocations | 117 |
| Hpatch command errors | 6 (5.1%) |

The agent changed exactly the eight allowed files, exited normally, and passed the hidden grader. Router lifecycle was clean: 49 started, completed, and usage-accounted requests; zero active, failed, canceled, timed-out, or background-pending requests.

## Rejections

| Call command | Script line | Operation | Target | Reason | Path | Generated line | Generated column |
|---:|---:|---|---|---|---|---:|---:|
| 2 | 2 | `type` | range | row-stale | `client/v3/mock/mockserver/mockserver.go` | — | — |
| 11 | 88 | `type-` | line | row-stale | `server/etcdserver/api/v3rpc/key.go` | — | — |
| 17 | 124 | `type` | line | row-stale | `server/etcdserver/txn/range.go` | — | — |
| 7 | 23 | `type` | range | language-syntax | `server/etcdserver/api/v3rpc/header.go` | 58 | 1 |
| 20 | 130 | `type-` | line | language-syntax | `server/etcdserver/v3_server.go` | 62 | 68 |
| 3 | 8 | `type` | range | language-syntax | `server/etcdserver/v3_server.go` | 289 | 1 |

All three Go syntax failures carry the localized originating command, target kind, and generated parser position. The three pre-format stale-row failures correctly omit generated positions.

## Payload efficiency

| Scope | Hpatch tokens | Estimated `apply_patch` tokens | Reduction |
|---|---:|---:|---:|
| Successful calls | 2,729 | 4,355 | 37.3% |
| Failed calls | 12,857 | 132 | n/a |
| All calls | 15,586 | 4,487 | -247.4% |

Added input-token overhead was 3,051 tokens: 198 state-report tokens, 926 rejection-diagnostic tokens, 1,985 installed-definition tokens, less 58 removed `apply_patch`-definition tokens.

The failed payload is the dominant regression: six rejected complete scripts cost 4.7 times the successful Hpatch payload. Per the explicit constraint, no new prompt hint was installed to force compact indexed corrections.

## Comparison with the preceding Hpatch-only repetition

| Metric | Previous | This run | Change |
|---|---:|---:|---:|
| Agent duration | ~795.0 s | 782.1 s | -1.6% |
| Requests | 72 | 49 | -31.9% |
| Agent commands | 89 | 35 | -60.7% |
| Uncached input tokens | 183,461 | 233,927 | +27.5% |
| Output tokens | 31,782 | 34,395 | +8.2% |
| Reasoning output tokens | 15,234 | 14,194 | -6.8% |
| Successful Hpatch calls | 5 | 2 | -60.0% |
| Rejected Hpatch calls | 4 | 6 | +50.0% |
| Successful payload reduction | 29.7% | 37.3% | +7.6 pp |
| Overall payload reduction | -70.9% | -247.4% | -176.5 pp |

Tool-level activity improved substantially, but error rate and output-token behavior regressed. The syntax-localization architecture improves diagnosis, not prevention; the run still produced three syntax failures, and row-stale failures increased from one to three.

## Comparison with the original two Hpatch repetitions

| Metric | Original mean | This run | Change |
|---|---:|---:|---:|
| Agent duration | 616.6 s | 782.1 s | +26.8% |
| Requests | 52.0 | 49 | -5.8% |
| Agent commands | 48.5 | 35 | -27.8% |
| Uncached input tokens | 132,716 | 233,927 | +76.3% |
| Output tokens | 24,116 | 34,395 | +42.6% |
| Reasoning output tokens | 13,292 | 14,194 | +6.8% |
| Successful Hpatch calls | 2.5 | 2 | -20.0% |
| Rejected Hpatch calls | 4.0 | 6 | +50.0% |
| Successful payload reduction | 91.1% | 37.3% | -53.8 pp |
| Overall payload reduction | -37.2% | -247.4% | -210.2 pp |

This repetition does not satisfy the primary output-token goal. It uses fewer requests and shell commands than the preceding run, but substantially more uncached input and output, with worse edit reliability and failed-payload cost.

## Artifacts

- `results.jsonl`: the single Hpatch result record.
- `artifacts/etcd-range-stream/etcd-range-stream-hpatch-r001/result.json`: complete run record.
- `artifacts/etcd-range-stream/etcd-range-stream-hpatch-r001/changes.patch`: agent diff captured before hidden-test injection.
- `artifacts/etcd-range-stream/etcd-range-stream-hpatch-r001/grader-etcd-range-stream.stdout`: grader output.
- `hpatch-metrics.json`: router and Hpatch interaction metrics.
- `gain.txt`: payload and command-efficiency report.
