# Hpatch benchmark: etcd-range-stream

- Mode: `hpatch-diagnostic`
- Model: `gpt-6-astra`
- Reasoning effort: `low`
- Metrics owner: in-process `capturer` on each router listener
- Evidence validation: passed

## Outcome and provider usage

| Arm | Task passes | Agent wall time (s) | Logical requests | Input tokens | Output tokens | Reasoning tokens |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 1/1 | 280.613 | 19 | 614247 | 3816 | 738 |

## Cache attribution

| Arm | Cached input | Uncached input | Provider cache rate | Cold/new uncached | Eligible prefix | Eligible cached | Eligible misses | Eligible prefix cache rate |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 573184 | 41063 | 93.31% | 37889 | 576358 | 573184 | 3174 | 99.45% |

## Cache-prefix diagnostics

Comparisons use decoded request items, not the provider hidden token prefix. Appended/identical means the observed earlier content is stable; it does not guarantee a cache hit. A changed post-replay prefix with a stable client prefix points to projection/replay; a changed provider prefix with a stable native prefix points to CTP. Missing, truncated, restarted, or first prefix observations are unavailable. Routing compares private fingerprints of the actual outgoing session key. Turn-state forwarding compares the current client/provider sticky-routing header: absent, preserved, dropped, changed, or unavailable. Stable session keys alone do not prove sticky routing; no key or content hash is shown.

| Arm | Request ordinal | Input | Cached | Client prefix | Post-replay prefix | Provider prefix | Route key | Request cache key | Turn-state forwarding |
|---|---:|---:|---:|---|---|---|---|---|---|
| Hpatch + CTP/2 | 1 | 15726 | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | absent |
| Hpatch + CTP/2 | 2 | 17075 | 15616 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 3 | 28737 | 16896 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 4 | 29536 | 28544 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 5 | 32338 | 29312 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 6 | 32469 | 32128 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 7 | 32620 | 32256 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 8 | 32878 | 32512 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 9 | 33486 | 32768 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 10 | 33580 | 33280 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 11 | 33832 | 33408 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 12 | 34895 | 33664 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 13 | 35046 | 34688 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 14 | 36348 | 34816 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 15 | 36442 | 36224 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 16 | 36747 | 36224 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 17 | 37035 | 36608 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 18 | 37568 | 36864 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 19 | 37889 | 37376 | appended | appended | appended | stable | stable | preserved |

## Provider response evidence

Cached-token telemetry distinguishes an explicit count (including zero) from missing, null, invalid, or unavailable evidence. Legacy aggregate counters may default missing telemetry to zero; those zeros are not proven cache misses. Response/header models are provider-reported identifiers, not verification of backend identity. Provider request IDs are retained privately in capture details, not this summary.

| Arm | Request ordinal | Attempt | Response model | Header model | Cached-token telemetry | Explicit cached tokens |
|---|---:|---:|---|---|---|---:|
| Hpatch + CTP/2 | 1 | 1 | gpt-6-astra | unavailable | present | 0 |
| Hpatch + CTP/2 | 2 | 1 | gpt-6-astra | unavailable | present | 15616 |
| Hpatch + CTP/2 | 3 | 1 | gpt-6-astra | unavailable | present | 16896 |
| Hpatch + CTP/2 | 4 | 1 | gpt-6-astra | unavailable | present | 28544 |
| Hpatch + CTP/2 | 5 | 1 | gpt-6-astra | unavailable | present | 29312 |
| Hpatch + CTP/2 | 6 | 1 | gpt-6-astra | unavailable | present | 32128 |
| Hpatch + CTP/2 | 7 | 1 | gpt-6-astra | unavailable | present | 32256 |
| Hpatch + CTP/2 | 8 | 1 | gpt-6-astra | unavailable | present | 32512 |
| Hpatch + CTP/2 | 9 | 1 | gpt-6-astra | unavailable | present | 32768 |
| Hpatch + CTP/2 | 10 | 1 | gpt-6-astra | unavailable | present | 33280 |
| Hpatch + CTP/2 | 11 | 1 | gpt-6-astra | unavailable | present | 33408 |
| Hpatch + CTP/2 | 12 | 1 | gpt-6-astra | unavailable | present | 33664 |
| Hpatch + CTP/2 | 13 | 1 | gpt-6-astra | unavailable | present | 34688 |
| Hpatch + CTP/2 | 14 | 1 | gpt-6-astra | unavailable | present | 34816 |
| Hpatch + CTP/2 | 15 | 1 | gpt-6-astra | unavailable | present | 36224 |
| Hpatch + CTP/2 | 16 | 1 | gpt-6-astra | unavailable | present | 36224 |
| Hpatch + CTP/2 | 17 | 1 | gpt-6-astra | unavailable | present | 36608 |
| Hpatch + CTP/2 | 18 | 1 | gpt-6-astra | unavailable | present | 36864 |
| Hpatch + CTP/2 | 19 | 1 | gpt-6-astra | unavailable | present | 37376 |

## Protocol transformation

Token estimates count decoded JSON keys and scalar values, excluding outer JSON framing and escaping; literal escapes inside content still count. Byte counts retain exact observed bytes. Input savings compare the actual native request AFTER replay and Hpatch projection with its final CTP provider request, not incoming Codex history. Output representation differences compare complete model-origin output arrays, reconstructed from finalized stream items when needed and excluding router-generated commentary, echoed tools, and other response metadata as well as repeated SSE events. Output differences include tool-carrier translation and are not CTP savings or stock-model savings. Positive output differences mean the delivered representation is larger than provider output. Only paired provider usage measures actual model-use differences. Retries remain separate provider attempts.

| Arm | CTP input bytes saved | CTP input tokens saved | Delivery byte expansion | Delivery token expansion | Provider attempts |
|---|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 121963 | 27957 | 13895 | 4738 | 19 |

### Observed content-token estimates

| Arm | Client requests | Provider requests | Provider response streams | Client response streams | Provider outputs | Client outputs |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 788427 | 783140 | 591673 | 340838 | 22179 | 26917 |

### CTP/2 acceptance: Hpatch + CTP/2

Input compression uses the post-replay native request versus its CTP encoding. Output compression uses only assistant output_text, excluding tool-carrier translation.

| Direction | Required | Tokens saved | Result |
|---|---|---:|---|
| Input | false | 27957 | not required |
| Output | false | 0 | not required |

## Actual model use

| Arm | Provider model | Provider attempts | Usage-bearing attempts | Input tokens | Cached input | Output tokens | Reasoning tokens |
|---|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | `gpt-6-astra` | 19 | 19 | 614247 | 573184 | 3816 | 738 |

## Tool transport

| Arm | Tool | Provider calls | Provider input tokens | Delivered calls | Delivered input tokens |
|---|---|---:|---:|---:|---:|
| Hpatch + CTP/2 | `exec` | 5 | 145 | 18 | 7593 |
| Hpatch + CTP/2 | `hpatch` | 4 | 2081 | 0 | 0 |
| Hpatch + CTP/2 | `shell` | 9 | 625 | 0 | 0 |

## Hpatch delivery

| Measure | Result |
|---|---:|
| Calls | 4 |
| Corrections | 0 |
| Successful deliveries | 4 |
| Rejected deliveries | 0 |
| Unmatched calls | 0 |
| Provider Hpatch input tokens | 2081 |
| Delivered carrier input tokens | 5933 |
| Carrier token expansion | 3852 |

## Capture completeness

| Arm | Records | Provider attempts | Capture errors | Incomplete | Provider/sequence errors | Write/skipped errors | Dropped detail |
|---|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 38 | 19 | 0 | 0 | 0 | 0 | 0 |

The capturer snapshot is authoritative for calculations. `results.jsonl` is reconciled against per-thread provider usage, and the sanitized schema-6 JSONL in `captures/` is reconciled against snapshot health and exchange totals. The summary contains no request, session, thread, call, or capture identifiers.
