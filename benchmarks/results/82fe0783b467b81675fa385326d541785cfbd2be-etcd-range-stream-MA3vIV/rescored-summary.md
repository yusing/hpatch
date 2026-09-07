# Retained Astra pair: corrected accounting assessment

Partial rescore of the retained run, not a new model execution or new grader run. Historical grading and provider usage were rechecked against per-arm results and sanitized provider capture records. Original artifacts remain unchanged.

| Measure | Stock control | Hpatch + CTP/2 | Hpatch change |
|---|---:|---:|---:|
| Required grading | Pass | Pass | Both passed |
| Agent time (s) | 357.899 | 386.555 | +8.01% |
| Requests | 17 | 21 | +23.53% |
| Provider input tokens | 557,589 | 712,360 | +27.76% |
| Cached input tokens | 502,912 | 668,032 | +32.83% |
| Uncached input tokens | 54,677 | 44,328 | -18.93% |
| Provider output tokens | 6,322 | 4,972 | -21.35% |
| Reasoning tokens (included in output) | 1,603 | 1,537 | -4.12% |

## Unavailable under corrected accounting

Framing-independent request/output content counts, post-replay native-to-CTP input savings, and assistant-text-only compression cannot be reconstructed from the retained schema-5/metrics-v3 counters. The necessary raw/intermediate observations were not retained. Mark these values **unavailable**, not zero. The previous protocol-savings figures are withdrawn as compression/model-savings evidence; old wire byte measurements remain transport facts.

No schema upgrade, fabricated replay, or model rerun was used. This report is not a metrics-v4-qualified benchmark result. Fresh captures are required for those metrics.

## Conclusion

Both implementations passed. In this single pair Hpatch took about 8% longer, used 27.76% more total provider input, 18.93% less uncached input, and 21.35% less provider output. Those observed usage differences remain valid; they do not isolate the cause or prove CTP compression effectiveness.
