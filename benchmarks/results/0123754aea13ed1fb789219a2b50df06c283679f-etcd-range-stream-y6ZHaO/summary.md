# Hpatch benchmark: etcd-range-stream

- Mode: `paired`
- Model: `gpt-6-astra`
- Reasoning effort: `medium`
- Metrics owner: in-process `capturer` on each router listener
- Evidence validation: passed

## Outcome and provider usage

| Arm | Task passes | Agent wall time (s) | Logical requests | Input tokens | Output tokens | Reasoning tokens |
|---|---:|---:|---:|---:|---:|---:|
| Stock | 1/1 | 356.045 | 17 | 586844 | 5832 | 1554 |
| Hpatch + CTP/2 | 1/1 | 263.627 | 15 | 486024 | 3652 | 754 |

Actual provider-token change from Stock to Hpatch + CTP/2: input **-100820** (-17.18%), output **-2180** (-37.38%).

## Cache attribution

| Arm | Cached input | Uncached input | Provider cache rate | Cold/new uncached | Eligible prefix | Eligible cached | Eligible misses | Eligible prefix cache rate |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 410496 | 75528 | 84.46% | 38880 | 447144 | 410496 | 36648 | 91.80% |
| Stock | 547968 | 38876 | 93.38% | 35932 | 542208 | 539264 | 2944 | 99.46% |

## Protocol transformation

Token estimates count decoded JSON keys and scalar values, excluding outer JSON framing and escaping; literal escapes inside content still count. Byte counts retain exact observed bytes. Input savings compare the actual native request AFTER replay and Hpatch projection with its final CTP provider request, not incoming Codex history. Output representation differences compare complete model-origin output arrays, reconstructed from finalized stream items when needed and excluding router-generated commentary, echoed tools, and other response metadata as well as repeated SSE events. Output differences include tool-carrier translation and are not CTP savings or stock-model savings. Positive output differences mean the delivered representation is larger than provider output. Only paired provider usage measures actual model-use differences. Retries remain separate provider attempts.

| Arm | CTP input bytes saved | CTP input tokens saved | Delivery byte expansion | Delivery token expansion | Provider attempts |
|---|---:|---:|---:|---:|---:|
| Stock | 0 | 0 | 0 | 0 | 17 |
| Hpatch + CTP/2 | 101708 | 22570 | 13026 | 4388 | 15 |

### Observed content-token estimates

| Arm | Client requests | Provider requests | Provider response streams | Client response streams | Provider outputs | Client outputs |
|---|---:|---:|---:|---:|---:|---:|
| Hpatch + CTP/2 | 579444 | 579924 | 492948 | 269998 | 18172 | 22560 |
| Stock | 728570 | 728570 | 577855 | 577855 | 25714 | 25714 |

### CTP/2 acceptance: Hpatch + CTP/2

Input compression uses the post-replay native request versus its CTP encoding. Output compression uses only assistant output_text, excluding tool-carrier translation.

| Direction | Required | Tokens saved | Result |
|---|---|---:|---|
| Input | false | 22570 | not required |
| Output | false | 0 | not required |

## Actual model use

| Arm | Provider model | Provider attempts | Usage-bearing attempts | Input tokens | Cached input | Output tokens | Reasoning tokens |
|---|---|---:|---:|---:|---:|---:|---:|
| Stock | `gpt-6-astra` | 17 | 17 | 586844 | 547968 | 5832 | 1554 |
| Hpatch + CTP/2 | `gpt-6-astra` | 15 | 15 | 486024 | 410496 | 3652 | 754 |

## Tool transport

| Arm | Tool | Provider calls | Provider input tokens | Delivered calls | Delivered input tokens |
|---|---|---:|---:|---:|---:|
| Stock | `exec` | 16 | 3931 | 16 | 3931 |
| Hpatch + CTP/2 | `exec` | 4 | 145 | 14 | 7111 |
| Hpatch + CTP/2 | `hpatch` | 2 | 1898 | 0 | 0 |
| Hpatch + CTP/2 | `shell` | 8 | 678 | 0 | 0 |

## Hpatch delivery

| Measure | Result |
|---|---:|
| Calls | 2 |
| Corrections | 0 |
| Successful deliveries | 2 |
| Rejected deliveries | 0 |
| Unmatched calls | 0 |
| Provider Hpatch input tokens | 1898 |
| Delivered carrier input tokens | 5390 |
| Carrier token expansion | 3492 |

## Capture completeness

| Arm | Records | Provider attempts | Capture errors | Incomplete | Provider/sequence errors | Write/skipped errors | Dropped detail |
|---|---:|---:|---:|---:|---:|---:|---:|
| Stock | 34 | 17 | 0 | 0 | 0 | 0 | 0 |
| Hpatch + CTP/2 | 30 | 15 | 0 | 0 | 0 | 0 | 0 |

The capturer snapshot is authoritative for calculations. `results.jsonl` is reconciled against per-thread provider usage, and the sanitized schema-6 JSONL in `captures/` is reconciled against snapshot health and exchange totals. The summary contains no request, session, thread, call, or capture identifiers.
