# Hpatch benchmark: etcd-range-stream

- Mode: `hpatch-diagnostic`
- Model: `gpt-6-astra`
- Reasoning effort: `low`
- Metrics owner: in-process `capturer` on each router listener
- Evidence validation: passed

## Outcome and provider usage

| Arm | Task passes | Agent wall time (s) | Logical requests | Input tokens | Output tokens | Reasoning tokens |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 1/1 | 241.510 | 15 | 494516 | 3687 | 630 |

## Cache attribution

| Arm | Cached input | Uncached input | Provider cache rate | Cold/new uncached | Eligible prefix | Eligible cached | Eligible misses | Eligible prefix cache rate |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 435200 | 59316 | 88.01% | 39960 | 454556 | 435200 | 19356 | 95.74% |

## Cache-prefix diagnostics

Comparisons use decoded request items, not the provider hidden token prefix. Appended/identical means the observed earlier content is stable; it does not guarantee a cache hit. A changed post-replay prefix with a stable client prefix points to projection/replay; a changed provider prefix with a stable native prefix points to CTP. Missing, truncated, restarted, or first prefix observations are unavailable. Routing compares private fingerprints of the actual outgoing session key. Turn-state forwarding compares the current client/provider sticky-routing header: absent, preserved, dropped, changed, or unavailable. Stable session keys alone do not prove sticky routing; no key or content hash is shown.

| Arm | Request ordinal | Input | Cached | Client prefix | Post-replay prefix | Provider prefix | Route key | Request cache key | Turn-state forwarding |
|---|---:|---:|---:|---|---|---|---|---|---|
| Hpatch + CTP/2 | 1 | 15425 | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | absent |
| Hpatch + CTP/2 | 2 | 16808 | 15232 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 3 | 28139 | 0 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 4 | 30568 | 27904 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 5 | 31935 | 30336 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 6 | 35134 | 31744 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 7 | 35269 | 34944 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 8 | 36063 | 35072 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 9 | 36501 | 35840 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 10 | 37285 | 36352 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 11 | 37381 | 37120 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 12 | 37589 | 37248 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 13 | 37849 | 37376 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 14 | 38610 | 37632 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 15 | 39960 | 38400 | appended | appended | appended | stable | stable | preserved |

## Provider response evidence

Cached-token telemetry distinguishes an explicit count (including zero) from missing, null, invalid, or unavailable evidence. Legacy aggregate counters may default missing telemetry to zero; those zeros are not proven cache misses. Response/header models are provider-reported identifiers, not verification of backend identity. Provider request IDs are retained privately in capture details, not this summary.

| Arm | Request ordinal | Attempt | Response model | Header model | Cached-token telemetry | Explicit cached tokens |
|---|---:|---:|---|---|---|---:|
| Hpatch + CTP/2 | 1 | 1 | gpt-6-astra | unavailable | present | 0 |
| Hpatch + CTP/2 | 2 | 1 | gpt-6-astra | unavailable | present | 15232 |
| Hpatch + CTP/2 | 3 | 1 | gpt-6-astra | unavailable | present | 0 |
| Hpatch + CTP/2 | 4 | 1 | gpt-6-astra | unavailable | present | 27904 |
| Hpatch + CTP/2 | 5 | 1 | gpt-6-astra | unavailable | present | 30336 |
| Hpatch + CTP/2 | 6 | 1 | gpt-6-astra | unavailable | present | 31744 |
| Hpatch + CTP/2 | 7 | 1 | gpt-6-astra | unavailable | present | 34944 |
| Hpatch + CTP/2 | 8 | 1 | gpt-6-astra | unavailable | present | 35072 |
| Hpatch + CTP/2 | 9 | 1 | gpt-6-astra | unavailable | present | 35840 |
| Hpatch + CTP/2 | 10 | 1 | gpt-6-astra | unavailable | present | 36352 |
| Hpatch + CTP/2 | 11 | 1 | gpt-6-astra | unavailable | present | 37120 |
| Hpatch + CTP/2 | 12 | 1 | gpt-6-astra | unavailable | present | 37248 |
| Hpatch + CTP/2 | 13 | 1 | gpt-6-astra | unavailable | present | 37376 |
| Hpatch + CTP/2 | 14 | 1 | gpt-6-astra | unavailable | present | 37632 |
| Hpatch + CTP/2 | 15 | 1 | gpt-6-astra | unavailable | present | 38400 |

## Protocol transformation

Token estimates count decoded JSON keys and scalar values, excluding outer JSON framing and escaping; literal escapes inside content still count. Byte counts retain exact observed bytes. Input savings compare the actual native request AFTER replay and Hpatch projection with its final CTP provider request, not incoming Codex history. Output representation differences compare complete model-origin output arrays, reconstructed from finalized stream items when needed and excluding router-generated commentary, echoed tools, and other response metadata as well as repeated SSE events. Output differences include tool-carrier translation and are not CTP savings or stock-model savings. Positive output differences mean the delivered representation is larger than provider output. Only paired provider usage measures actual model-use differences. Retries remain separate provider attempts.

| Arm | CTP input bytes saved | CTP input tokens saved | Delivery byte expansion | Delivery token expansion | Provider attempts |
|---|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 86577 | 19169 | 14190 | 4746 | 15 |

### Observed content-token estimates

| Arm | Client requests | Provider requests | Provider response streams | Client response streams | Provider outputs | Client outputs |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 591241 | 589856 | 499881 | 272597 | 17682 | 22428 |

### CTP/2 acceptance: Hpatch + CTP/2

Input compression uses the post-replay native request versus its CTP encoding. Output compression uses only assistant output_text, excluding tool-carrier translation.

| Direction | Required | Tokens saved | Result |
|---|---|---:|---|
| Input | false | 19169 | not required |
| Output | false | 0 | not required |

## Actual model use

| Arm | Provider model | Provider attempts | Usage-bearing attempts | Input tokens | Cached input | Output tokens | Reasoning tokens |
|---|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | `gpt-6-astra` | 15 | 15 | 494516 | 435200 | 3687 | 630 |

## Tool transport

| Arm | Tool | Provider calls | Provider input tokens | Delivered calls | Delivered input tokens |
|---|---|---:|---:|---:|---:|
| Hpatch + CTP/2 | `exec` | 3 | 167 | 14 | 7628 |
| Hpatch + CTP/2 | `hpatch` | 2 | 2054 | 0 | 0 |
| Hpatch + CTP/2 | `shell` | 9 | 659 | 0 | 0 |

## Hpatch delivery

| Measure | Result |
|---|---:|
| Calls | 2 |
| Corrections | 0 |
| Successful deliveries | 2 |
| Rejected deliveries | 0 |
| Unmatched calls | 0 |
| Provider Hpatch input tokens | 2054 |
| Delivered carrier input tokens | 5805 |
| Carrier token expansion | 3751 |

## Capture completeness

| Arm | Records | Provider attempts | Capture errors | Incomplete | Provider/sequence errors | Write/skipped errors | Dropped detail |
|---|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 30 | 15 | 0 | 0 | 0 | 0 | 0 |

The capturer snapshot is authoritative for calculations. `results.jsonl` is reconciled against per-thread provider usage, and the sanitized schema-6 JSONL in `captures/` is reconciled against snapshot health and exchange totals. The summary contains no request, session, thread, call, or capture identifiers.
