# Astra-low cache follow-up

Exactly one Astra-low Hpatch + CTP/2 attempt ran. Runner exit status 0: task grading,
capture validation, and no-edit-loop acceptance all passed. Four Hpatch deliveries
succeeded, with no rejections or corrections.

Compared with `92ef3b68c8faa902d243f5138c20c95bdb4e7c97-etcd-range-stream-inhN8d`, the
task fingerprint, Codex release, treatment protocol, grading settings, and instruction
bytes match. Both use Astra low. The router revision and execution time differ.

| Measure | Previous Astra low | This attempt |
|---|---:|---:|
| Requests | 19 | 19 |
| Agent seconds | 265.438 | 280.613 |
| Input tokens | 625,466 | 614,247 |
| Cached input | 547,584 | 573,184 |
| Uncached input | 77,882 | 41,063 |
| Provider cache rate | 87.55% | 93.31% |
| Output tokens | 3,447 | 3,816 |

Uncached input decreased by 36,819 tokens (47.28%). The prior full-cache-drop pattern
did not recur: all 18 continuation requests had positive cached input. All 19 requests
had explicitly present numeric cached-token telemetry; the initial request explicitly
reported zero. Request 6 reported 32,128 cached tokens out of 32,469 input, not zero.

All comparable client/native/provider prefixes are append-stable. Turn state was
preserved at the provider boundary on every continuation and remained identical across
continuations. Session and body cache keys remained stable. Every response body reported
`gpt-6-astra`. Header-model and provider-request-ID evidence are unavailable on all 19
attempts; no support correlation identifier was retained.

This verifies the new telemetry-presence capture in live traffic and records a run
without a full continuation miss. It does not prove that the previous zero was explicit
rather than missing telemetry, nor that intermittent provider cache drops are eliminated.
The instrumentation added observation, not cache warmup or a new routing change. No
second attempt or extra control was run.
