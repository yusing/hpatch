# Hpatch benchmark: etcd-range-stream

- Mode: `paired`
- Model: `gpt-6-astra`
- Reasoning effort: `medium`
- Metrics owner: in-process `capturer` on each router listener
- Evidence validation: passed

## Outcome and provider usage

| Arm | Task passes | Agent wall time (s) | Logical requests | Input tokens | Output tokens | Reasoning tokens |
|---|---:|---:|---:|---:|---:|---:|
| Stock | 1/1 | 357.899 | 17 | 557589 | 6322 | 1603 |
| Hpatch + CTP/2 | 1/1 | 386.555 | 21 | 712360 | 4972 | 1537 |

Actual provider-token change from Stock to Hpatch + CTP/2: input **+154771** (27.76%), output **-1350** (-21.35%).

## Cache attribution

| Arm | Cached input | Uncached input | Provider cache rate | Cold/new uncached | Eligible prefix | Eligible cached | Eligible misses | Eligible prefix cache rate |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 668032 | 44328 | 93.78% | 41014 | 671346 | 668032 | 3314 | 99.51% |
| Stock | 502912 | 54677 | 90.19% | 33780 | 515105 | 494208 | 20897 | 95.94% |

## Protocol transformation

Input savings compare each complete client request with its final provider request. Output savings compare complete model-origin output arrays, reconstructed from finalized stream items when needed and excluding router-generated commentary, echoed tools, and other response metadata as well as repeated SSE events. Positive values mean the provider boundary is smaller than the Codex-facing boundary; negative values mean provider-boundary expansion. Retries remain separate provider attempts.

| Arm | Input bytes saved | Input tokens saved | Output bytes saved | Output tokens saved | Provider attempts |
|---|---:|---:|---:|---:|---:|
| Stock | -17480 | -9841 | 0 | 0 | 17 |
| Hpatch + CTP/2 | -59314 | -12612 | 10890 | 3856 | 21 |

### Observed payloads

| Arm | Client requests | Provider requests | Provider response streams | Client response streams | Provider outputs | Client outputs |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 1041914 | 1054526 | 824024 | 517181 | 32951 | 36807 |
| Stock | 805658 | 815499 | 773048 | 773048 | 30287 | 30287 |

### CTP/2 acceptance: Hpatch + CTP/2

Configured input compression uses complete request payloads; output compression uses one capturer-owned terminal output array per boundary.

| Direction | Required | Tokens saved | Result |
|---|---|---:|---|
| Input | false | -12612 | not required |
| Output | false | 3856 | not required |

## Actual model use

| Arm | Provider model | Provider attempts | Usage-bearing attempts | Input tokens | Cached input | Output tokens | Reasoning tokens |
|---|---|---:|---:|---:|---:|---:|---:|
| Stock | `gpt-6-astra` | 17 | 17 | 557589 | 502912 | 6322 | 1603 |
| Hpatch + CTP/2 | `gpt-6-astra` | 21 | 21 | 712360 | 668032 | 4972 | 1537 |

## Tool transport

| Arm | Tool | Provider calls | Provider input tokens | Delivered calls | Delivered input tokens |
|---|---|---:|---:|---:|---:|
| Stock | `exec` | 16 | 4323 | 16 | 4323 |
| Hpatch + CTP/2 | `exec` | 8 | 502 | 20 | 7471 |
| Hpatch + CTP/2 | `hpatch` | 4 | 2032 | 0 | 0 |
| Hpatch + CTP/2 | `shell` | 8 | 645 | 0 | 0 |

## Hpatch delivery

| Measure | Result |
|---|---:|
| Calls | 4 |
| Corrections | 0 |
| Successful deliveries | 4 |
| Rejected deliveries | 0 |
| Unmatched calls | 0 |
| Provider Hpatch input tokens | 2032 |
| Delivered carrier input tokens | 5455 |
| Carrier input tokens saved | 3423 |

## Capture completeness

| Arm | Records | Provider attempts | Capture errors | Incomplete | Provider/sequence errors | Write/skipped errors | Dropped detail |
|---|---:|---:|---:|---:|---:|---:|---:|
| Stock | 34 | 17 | 0 | 0 | 0 | 0 | 0 |
| Hpatch + CTP/2 | 42 | 21 | 0 | 0 | 0 | 0 | 0 |

The capturer snapshot is authoritative for calculations. `results.jsonl` is reconciled against per-thread provider usage, and the sanitized schema-5 JSONL in `captures/` is reconciled against snapshot health and exchange totals. The summary contains no request, session, thread, call, or capture identifiers.
