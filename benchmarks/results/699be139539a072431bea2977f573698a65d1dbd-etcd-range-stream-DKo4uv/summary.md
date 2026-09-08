# Hpatch benchmark: etcd-range-stream

- Mode: `hpatch-diagnostic`
- Model: `gpt-6-astra`
- Reasoning effort: `low`
- Metrics owner: in-process `capturer` on each router listener
- Evidence validation: passed

## Outcome and provider usage

| Arm | Task passes | Agent wall time (s) | Logical requests | Input tokens | Output tokens | Reasoning tokens |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 1/1 | 300.811 | 18 | 520435 | 3682 | 683 |

## Cache attribution

| Arm | Cached input | Uncached input | Provider cache rate | Cold/new uncached | Eligible prefix | Eligible cached | Eligible misses | Eligible prefix cache rate |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 454784 | 65651 | 87.39% | 29214 | 485461 | 449024 | 36437 | 92.49% |

## Cache-prefix diagnostics

Comparisons use decoded request items, not the provider hidden token prefix. Appended/identical means the observed earlier content is stable; it does not guarantee a cache hit. A changed post-replay prefix with a stable client prefix points to projection/replay; a changed provider prefix with a stable native prefix points to CTP. Missing, truncated, restarted, or first prefix observations are unavailable. Routing compares private fingerprints of the actual outgoing session key. Turn-state forwarding compares the current client/provider sticky-routing header: absent, preserved, dropped, changed, or unavailable. Stable session keys alone do not prove sticky routing; no key or content hash is shown.

| Arm | Request ordinal | Input | Cached | Client prefix | Post-replay prefix | Provider prefix | Route key | Request cache key | Turn-state forwarding |
|---|---:|---:|---:|---|---|---|---|---|---|
| Hpatch + CTP/2 | 1 | 15622 | 5760 | unavailable | unavailable | unavailable | unavailable | unavailable | absent |
| Hpatch + CTP/2 | 2 | 16822 | 15488 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 3 | 25118 | 16640 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 4 | 25764 | 24960 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 5 | 26226 | 25600 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 6 | 28887 | 26112 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 7 | 29026 | 28672 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 8 | 30046 | 28800 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 9 | 30721 | 0 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 10 | 30948 | 30592 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 11 | 31045 | 30720 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 12 | 31292 | 29824 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 13 | 31469 | 31104 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 14 | 31773 | 31360 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 15 | 33291 | 31616 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 16 | 33643 | 33152 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 17 | 33768 | 30848 | appended | appended | appended | stable | stable | preserved |
| Hpatch + CTP/2 | 18 | 34974 | 33536 | appended | appended | appended | stable | stable | preserved |

## Provider response evidence

Cached-token telemetry distinguishes an explicit count (including zero) from missing, null, invalid, or unavailable evidence. Legacy aggregate counters may default missing telemetry to zero; those zeros are not proven cache misses. Response/header models are provider-reported identifiers, not verification of backend identity. Provider request IDs are retained privately in capture details, not this summary.

| Arm | Request ordinal | Attempt | Response model | Header model | Cached-token telemetry | Explicit cached tokens |
|---|---:|---:|---|---|---|---:|
| Hpatch + CTP/2 | 1 | 1 | gpt-6-astra | unavailable | present | 5760 |
| Hpatch + CTP/2 | 2 | 1 | gpt-6-astra | unavailable | present | 15488 |
| Hpatch + CTP/2 | 3 | 1 | gpt-6-astra | unavailable | present | 16640 |
| Hpatch + CTP/2 | 4 | 1 | gpt-6-astra | unavailable | present | 24960 |
| Hpatch + CTP/2 | 5 | 1 | gpt-6-astra | unavailable | present | 25600 |
| Hpatch + CTP/2 | 6 | 1 | gpt-6-astra | unavailable | present | 26112 |
| Hpatch + CTP/2 | 7 | 1 | gpt-6-astra | unavailable | present | 28672 |
| Hpatch + CTP/2 | 8 | 1 | gpt-6-astra | unavailable | present | 28800 |
| Hpatch + CTP/2 | 9 | 1 | gpt-6-astra | unavailable | present | 0 |
| Hpatch + CTP/2 | 10 | 1 | gpt-6-astra | unavailable | present | 30592 |
| Hpatch + CTP/2 | 11 | 1 | gpt-6-astra | unavailable | present | 30720 |
| Hpatch + CTP/2 | 12 | 1 | gpt-6-astra | unavailable | present | 29824 |
| Hpatch + CTP/2 | 13 | 1 | gpt-6-astra | unavailable | present | 31104 |
| Hpatch + CTP/2 | 14 | 1 | gpt-6-astra | unavailable | present | 31360 |
| Hpatch + CTP/2 | 15 | 1 | gpt-6-astra | unavailable | present | 31616 |
| Hpatch + CTP/2 | 16 | 1 | gpt-6-astra | unavailable | present | 33152 |
| Hpatch + CTP/2 | 17 | 1 | gpt-6-astra | unavailable | present | 30848 |
| Hpatch + CTP/2 | 18 | 1 | gpt-6-astra | unavailable | present | 33536 |

## Protocol transformation

Token estimates count decoded JSON keys and scalar values, excluding outer JSON framing and escaping; literal escapes inside content still count. Byte counts retain exact observed bytes. Input savings compare the actual native request AFTER replay and Hpatch projection with its final CTP provider request, not incoming Codex history. Output representation differences compare complete model-origin output arrays, reconstructed from finalized stream items when needed and excluding router-generated commentary, echoed tools, and other response metadata as well as repeated SSE events. Output differences include tool-carrier translation and are not CTP savings or stock-model savings. Positive output differences mean the delivered representation is larger than provider output. Only paired provider usage measures actual model-use differences. Retries remain separate provider attempts.

| Arm | CTP input bytes saved | CTP input tokens saved | Delivery byte expansion | Delivery token expansion | Provider attempts |
|---|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 127597 | 28445 | 10877 | 3780 | 18 |

### Observed content-token estimates

| Arm | Client requests | Provider requests | Provider response streams | Client response streams | Provider outputs | Client outputs |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 672597 | 673638 | 563690 | 330164 | 22218 | 25998 |

### CTP/2 acceptance: Hpatch + CTP/2

Input compression uses the post-replay native request versus its CTP encoding. Output compression uses only assistant output_text, excluding tool-carrier translation.

| Direction | Required | Tokens saved | Result |
|---|---|---:|---|
| Input | false | 28445 | not required |
| Output | false | 0 | not required |

## Actual model use

| Arm | Provider model | Provider attempts | Usage-bearing attempts | Input tokens | Cached input | Output tokens | Reasoning tokens |
|---|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | `gpt-6-astra` | 18 | 18 | 520435 | 454784 | 3682 | 683 |

## Tool transport

| Arm | Tool | Provider calls | Provider input tokens | Delivered calls | Delivered input tokens |
|---|---|---:|---:|---:|---:|
| Hpatch + CTP/2 | `exec` | 6 | 261 | 17 | 6566 |
| Hpatch + CTP/2 | `hpatch` | 3 | 1945 | 0 | 0 |
| Hpatch + CTP/2 | `shell` | 8 | 577 | 0 | 0 |

## Hpatch delivery

| Measure | Result |
|---|---:|
| Calls | 3 |
| Corrections | 0 |
| Successful deliveries | 3 |
| Rejected deliveries | 0 |
| Unmatched calls | 0 |
| Provider Hpatch input tokens | 1945 |
| Delivered carrier input tokens | 4851 |
| Carrier token expansion | 2906 |

## Capture completeness

| Arm | Records | Provider attempts | Capture errors | Incomplete | Provider/sequence errors | Write/skipped errors | Dropped detail |
|---|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 36 | 18 | 0 | 0 | 0 | 0 | 0 |

The capturer snapshot is authoritative for calculations. `results.jsonl` is reconciled against per-thread provider usage, and the sanitized schema-6 JSONL in `captures/` is reconciled against snapshot health and exchange totals. The summary contains no request, session, thread, call, or capture identifiers.
