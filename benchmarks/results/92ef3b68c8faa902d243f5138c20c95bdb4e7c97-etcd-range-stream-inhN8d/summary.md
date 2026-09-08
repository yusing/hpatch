# Hpatch benchmark: etcd-range-stream

- Mode: `hpatch-diagnostic`
- Model: `gpt-6-astra`
- Reasoning effort: `low`
- Metrics owner: in-process `capturer` on each router listener
- Evidence validation: passed

## Outcome and provider usage

| Arm | Task passes | Agent wall time (s) | Logical requests | Input tokens | Output tokens | Reasoning tokens |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 1/1 | 265.438 | 19 | 625466 | 3447 | 515 |

## Cache attribution

| Arm | Cached input | Uncached input | Provider cache rate | Cold/new uncached | Eligible prefix | Eligible cached | Eligible misses | Eligible prefix cache rate |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 547584 | 77882 | 87.55% | 38403 | 587063 | 547584 | 39479 | 93.28% |

## Cache-prefix diagnostics

Comparisons use decoded request items, not the provider hidden token prefix. Appended/identical means the observed earlier content is stable; it does not guarantee a cache hit. A changed post-replay prefix with a stable client prefix points to projection/replay; a changed provider prefix with a stable native prefix points to CTP. Missing, truncated, restarted, or first prefix observations are unavailable. Routing compares private fingerprints of the actual outgoing session key. Turn-state forwarding compares the current client/provider sticky-routing header: absent, preserved, dropped, changed, or unavailable. Stable session keys alone do not prove sticky routing; no key or content hash is shown.

| Arm | Request ordinal | Input | Cached | Client prefix | Post-replay prefix | Provider prefix | Route key | Request cache key | Turn-state forwarding |
|---|---:|---:|---:|---|---|---|---|---|---|
| Hpatch + CTP/2 | 1 | 15725 | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | absent |
| Hpatch + CTP/2 | 2 | 16915 | 15616 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 3 | 29306 | 16768 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 4 | 29928 | 29184 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 5 | 32909 | 29696 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 6 | 33084 | 0 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 7 | 34063 | 32896 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 8 | 34515 | 32768 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 9 | 34612 | 34304 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 10 | 35054 | 34432 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 11 | 35528 | 33920 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 12 | 35635 | 35328 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 13 | 36152 | 35456 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 14 | 36414 | 35968 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 15 | 36615 | 36224 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 16 | 36709 | 36480 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 17 | 36853 | 34944 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 18 | 37046 | 36736 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 19 | 38403 | 36864 | appended | appended | appended | stable | stable | preserved |

## Protocol transformation

Token estimates count decoded JSON keys and scalar values, excluding outer JSON framing and escaping; literal escapes inside content still count. Byte counts retain exact observed bytes. Input savings compare the actual native request AFTER replay and Hpatch projection with its final CTP provider request, not incoming Codex history. Output representation differences compare complete model-origin output arrays, reconstructed from finalized stream items when needed and excluding router-generated commentary, echoed tools, and other response metadata as well as repeated SSE events. Output differences include tool-carrier translation and are not CTP savings or stock-model savings. Positive output differences mean the delivered representation is larger than provider output. Only paired provider usage measures actual model-use differences. Retries remain separate provider attempts.

| Arm | CTP input bytes saved | CTP input tokens saved | Delivery byte expansion | Delivery token expansion | Provider attempts |
|---|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 117950 | 26461 | 15628 | 5158 | 19 |

### Observed content-token estimates

| Arm | Client requests | Provider requests | Provider response streams | Client response streams | Provider outputs | Client outputs |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 805670 | 793751 | 583147 | 341733 | 21232 | 26390 |

### CTP/2 acceptance: Hpatch + CTP/2

Input compression uses the post-replay native request versus its CTP encoding. Output compression uses only assistant output_text, excluding tool-carrier translation.

| Direction | Required | Tokens saved | Result |
|---|---|---:|---|
| Input | false | 26461 | not required |
| Output | false | 0 | not required |

## Actual model use

| Arm | Provider model | Provider attempts | Usage-bearing attempts | Input tokens | Cached input | Output tokens | Reasoning tokens |
|---|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | `gpt-6-astra` | 19 | 19 | 625466 | 547584 | 3447 | 515 |

## Tool transport

| Arm | Tool | Provider calls | Provider input tokens | Delivered calls | Delivered input tokens |
|---|---|---:|---:|---:|---:|
| Hpatch + CTP/2 | `exec` | 4 | 145 | 18 | 7867 |
| Hpatch + CTP/2 | `hpatch` | 4 | 1909 | 0 | 0 |
| Hpatch + CTP/2 | `shell` | 10 | 651 | 0 | 0 |

## Hpatch delivery

| Measure | Result |
|---|---:|
| Calls | 4 |
| Corrections | 0 |
| Successful deliveries | 4 |
| Rejected deliveries | 0 |
| Unmatched calls | 0 |
| Provider Hpatch input tokens | 1909 |
| Delivered carrier input tokens | 5997 |
| Carrier token expansion | 4088 |

## Capture completeness

| Arm | Records | Provider attempts | Capture errors | Incomplete | Provider/sequence errors | Write/skipped errors | Dropped detail |
|---|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 38 | 19 | 0 | 0 | 0 | 0 | 0 |

The capturer snapshot is authoritative for calculations. `results.jsonl` is reconciled against per-thread provider usage, and the sanitized schema-6 JSONL in `captures/` is reconciled against snapshot health and exchange totals. The summary contains no request, session, thread, call, or capture identifiers.
