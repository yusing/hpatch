# Hpatch benchmark: etcd-range-stream

- Mode: `paired`
- Model: `gpt-5.6-sol`
- Reasoning effort: `medium`
- Metrics owner: in-process `capturer` on each router listener
- Evidence validation: passed

## Outcome and provider usage

| Arm | Task passes | Agent wall time (s) | Logical requests | Input tokens | Output tokens | Reasoning tokens |
|---|---:|---:|---:|---:|---:|---:|
| Stock | 1/1 | 902.957 | 34 | 1365579 | 15240 | 6413 |
| Hpatch + CTP/2 | 1/1 | 705.397 | 28 | 1367060 | 11948 | 5630 |

Actual provider-token change from Stock to Hpatch + CTP/2: input **+1481** (0.11%), output **-3292** (-21.60%).

## Cache attribution

| Arm | Cached input | Uncached input | Provider cache rate | Cold/new uncached | Eligible prefix | Eligible cached | Eligible misses | Eligible prefix cache rate |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 1287552 | 79508 | 94.18% | 60665 | 1306395 | 1287552 | 18843 | 98.56% |
| Stock | 1306112 | 59467 | 95.65% | 53409 | 1312170 | 1306112 | 6058 | 99.54% |

## Cache-prefix diagnostics

Comparisons use decoded request items, not the provider hidden token prefix. Appended/identical means the observed earlier content is stable; it does not guarantee a cache hit. A changed post-replay prefix with a stable client prefix points to projection/replay; a changed provider prefix with a stable native prefix points to CTP. Missing, truncated, restarted, or first observations are unavailable. Routing compares private fingerprints of the actual outgoing session key; no key or content hash is shown.

| Arm | Request ordinal | Input | Cached | Client prefix | Post-replay prefix | Provider prefix | Route key | Request cache key |
|---|---:|---:|---:|---|---|---|---|---|
| Hpatch + CTP/2 | 1 | 14421 | 0 | unavailable | unavailable | unavailable | unavailable | unavailable |
| Hpatch + CTP/2 | 2 | 15796 | 0 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 3 | 28873 | 15616 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 4 | 35847 | 28672 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 5 | 39297 | 35712 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 6 | 40548 | 39168 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 7 | 43128 | 40320 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 8 | 48183 | 43008 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 9 | 48399 | 48000 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 10 | 50389 | 48256 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 11 | 51114 | 50176 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 12 | 52550 | 50944 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 13 | 52805 | 52352 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 14 | 53081 | 52608 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 15 | 53236 | 52864 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 16 | 53345 | 53120 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 17 | 53774 | 53120 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 18 | 54244 | 53632 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 19 | 54517 | 54016 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 20 | 54880 | 54400 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 21 | 57042 | 54656 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 22 | 57473 | 56832 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 23 | 57667 | 57344 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 24 | 58110 | 57472 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 25 | 58539 | 57984 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 26 | 59253 | 58368 | appended | appended | appended | stable | stable |
| Hpatch + CTP/2 | 27 | 59884 | 59136 | unavailable | appended | appended | stable | stable |
| Hpatch + CTP/2 | 28 | 60665 | 59776 | unavailable | appended | appended | stable | stable |
| Stock | 1 | 9937 | 0 | unavailable | unavailable | unavailable | unavailable | unavailable |
| Stock | 2 | 13721 | 9728 | appended | appended | appended | stable | stable |
| Stock | 3 | 23026 | 13568 | appended | appended | appended | stable | stable |
| Stock | 4 | 25573 | 22784 | appended | appended | appended | stable | stable |
| Stock | 5 | 27653 | 25344 | appended | appended | appended | stable | stable |
| Stock | 6 | 30811 | 27520 | appended | appended | appended | stable | stable |
| Stock | 7 | 32002 | 30592 | appended | appended | appended | stable | stable |
| Stock | 8 | 34081 | 31872 | appended | appended | appended | stable | stable |
| Stock | 9 | 37600 | 33920 | appended | appended | appended | stable | stable |
| Stock | 10 | 39296 | 37376 | appended | appended | appended | stable | stable |
| Stock | 11 | 40240 | 39168 | appended | appended | appended | stable | stable |
| Stock | 12 | 40717 | 40064 | appended | appended | appended | stable | stable |
| Stock | 13 | 40842 | 40576 | appended | appended | appended | stable | stable |
| Stock | 14 | 40918 | 40704 | appended | appended | appended | stable | stable |
| Stock | 15 | 40986 | 40704 | appended | appended | appended | stable | stable |
| Stock | 16 | 41054 | 40832 | appended | appended | appended | stable | stable |
| Stock | 17 | 41131 | 40832 | appended | appended | appended | stable | stable |
| Stock | 18 | 41199 | 40960 | appended | appended | appended | stable | stable |
| Stock | 19 | 41319 | 40960 | appended | appended | appended | stable | stable |
| Stock | 20 | 41461 | 41088 | appended | appended | appended | stable | stable |
| Stock | 21 | 41700 | 41344 | appended | appended | appended | stable | stable |
| Stock | 22 | 42027 | 41472 | appended | appended | appended | stable | stable |
| Stock | 23 | 46040 | 41856 | appended | appended | appended | stable | stable |
| Stock | 24 | 47129 | 45824 | appended | appended | appended | stable | stable |
| Stock | 25 | 47527 | 46976 | appended | appended | appended | stable | stable |
| Stock | 26 | 48185 | 47360 | appended | appended | appended | stable | stable |
| Stock | 27 | 48885 | 48000 | appended | appended | appended | stable | stable |
| Stock | 28 | 49175 | 48768 | appended | appended | appended | stable | stable |
| Stock | 29 | 49729 | 49024 | appended | appended | appended | stable | stable |
| Stock | 30 | 50109 | 49536 | appended | appended | appended | stable | stable |
| Stock | 31 | 52165 | 49920 | appended | appended | appended | stable | stable |
| Stock | 32 | 52844 | 51968 | appended | appended | appended | stable | stable |
| Stock | 33 | 53088 | 52608 | appended | appended | appended | stable | stable |
| Stock | 34 | 53409 | 52864 | appended | appended | appended | stable | stable |

## Protocol transformation

Token estimates count decoded JSON keys and scalar values, excluding outer JSON framing and escaping; literal escapes inside content still count. Byte counts retain exact observed bytes. Input savings compare the actual native request AFTER replay and Hpatch projection with its final CTP provider request, not incoming Codex history. Output representation differences compare complete model-origin output arrays, reconstructed from finalized stream items when needed and excluding router-generated commentary, echoed tools, and other response metadata as well as repeated SSE events. Output differences include tool-carrier translation and are not CTP savings or stock-model savings. Positive output differences mean the delivered representation is larger than provider output. Only paired provider usage measures actual model-use differences. Retries remain separate provider attempts.

| Arm | CTP input bytes saved | CTP input tokens saved | Delivery byte expansion | Delivery token expansion | Provider attempts |
|---|---:|---:|---:|---:|---:|
| Stock | 0 | 0 | 0 | 0 | 34 |
| Hpatch + CTP/2 | 299559 | 67128 | 11064 | 4294 | 28 |

### Observed content-token estimates

| Arm | Client requests | Provider requests | Provider response streams | Client response streams | Provider outputs | Client outputs |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 2169005 | 2162497 | 1043383 | 560849 | 68696 | 72990 |
| Stock | 2211070 | 2211070 | 1121645 | 1121645 | 76187 | 76187 |

### CTP/2 acceptance: Hpatch + CTP/2

Input compression uses the post-replay native request versus its CTP encoding. Output compression uses only assistant output_text, excluding tool-carrier translation.

| Direction | Required | Tokens saved | Result |
|---|---|---:|---|
| Input | false | 67128 | not required |
| Output | false | 0 | not required |

## Actual model use

| Arm | Provider model | Provider attempts | Usage-bearing attempts | Input tokens | Cached input | Output tokens | Reasoning tokens |
|---|---|---:|---:|---:|---:|---:|---:|
| Stock | `gpt-5.6-sol` | 34 | 34 | 1365579 | 1306112 | 15240 | 6413 |
| Hpatch + CTP/2 | `gpt-5.6-sol` | 28 | 28 | 1367060 | 1287552 | 11948 | 5630 |

## Tool transport

| Arm | Tool | Provider calls | Provider input tokens | Delivered calls | Delivered input tokens |
|---|---|---:|---:|---:|---:|
| Stock | `exec` | 33 | 8207 | 33 | 8207 |
| Hpatch + CTP/2 | `exec` | 4 | 140 | 27 | 10271 |
| Hpatch + CTP/2 | `hpatch` | 8 | 4396 | 0 | 0 |
| Hpatch + CTP/2 | `hpatch_recover` | 1 | 12 | 0 | 0 |
| Hpatch + CTP/2 | `shell` | 14 | 1418 | 0 | 0 |

## Hpatch delivery

| Measure | Result |
|---|---:|
| Calls | 9 |
| Corrections | 1 |
| Successful deliveries | 7 |
| Rejected deliveries | 2 |
| Unmatched calls | 0 |
| Provider Hpatch input tokens | 4408 |
| Delivered carrier input tokens | 7139 |
| Carrier token expansion | 2731 |
| Diagnostic `language-syntax` | 1 |
| Diagnostic `row-stale` | 1 |

## Capture completeness

| Arm | Records | Provider attempts | Capture errors | Incomplete | Provider/sequence errors | Write/skipped errors | Dropped detail |
|---|---:|---:|---:|---:|---:|---:|---:|
| Stock | 68 | 34 | 0 | 0 | 0 | 0 | 0 |
| Hpatch + CTP/2 | 56 | 28 | 0 | 0 | 0 | 0 | 0 |

The capturer snapshot is authoritative for calculations. `results.jsonl` is reconciled against per-thread provider usage, and the sanitized schema-6 JSONL in `captures/` is reconciled against snapshot health and exchange totals. The summary contains no request, session, thread, call, or capture identifiers.
