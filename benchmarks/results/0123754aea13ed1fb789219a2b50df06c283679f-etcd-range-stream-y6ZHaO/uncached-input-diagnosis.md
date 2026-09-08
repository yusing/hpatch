# Uncached-input diagnosis

The latest Astra-medium pair used 75,528 uncached input tokens with Hpatch + CTP/2,
versus 38,876 for stock: +36,652 (+94.28%). Both arms passed and had no retries.

## Where the difference occurs

| Measure | Stock | Hpatch + CTP/2 | Difference |
|---|---:|---:|---:|
| Cold/new attribution | 35,932 | 38,880 | +2,948 |
| Repeat-prefix shortfall estimate | 2,944 | 36,648 | +33,704 |
| First-request cached input | 8,704 | 0 | -8,704 |
| Final-request input | 44,636 | 38,880 | -5,756 |

The shortfall estimate is min(previous input, current input) minus reused cached
input, clipped at zero. It does not measure the actual common prefix and cannot
by itself prove a router-induced cache invalidation. The cold/new attribution
includes the initial cache advantage; it is not a direct measurement of unique content.

Hpatch request 2 reused zero tokens from a preceding 15,724-token request.
Request 6, immediately after the first Hpatch edit, reused 16,896 versus the preceding
31,082 input tokens (14,186 shortfall). Request 7 reused 30,848 versus 34,226
(3,378 shortfall). These three events total 33,288 of Hpatch's 36,648 estimated
repeat-prefix shortfall. Ordinary subsequent requests return to small shortfalls.

All request input lengths grew monotonically, allowing this exact arithmetic
breakdown of the uncached difference: 33,704 additional repeat-prefix shortfall
+ 8,704 stock initial-cache advantage - 5,756 smaller final Hpatch context = 36,652.
This is an accounting decomposition, not proof that all shorter hits were caused
by identical-prefix misses.

## Mechanism and limits

Source inspection confirms Hpatch restores delivered carriers to original model calls
before CTP, and CTP encodes input sequentially using only preceding sources. Existing
prefix tests cover append stability. The router preserves a valid prompt cache key
across forwarding/retries. Alias confirmation after successful edits affects later
edit target resolution, not a general rewrite of all earlier read results.

The timing makes the first-edit replay boundary worth checking, but does not prove
replay or CTP caused the cache hit collapse. A prefix change, routing to a backend
with a shorter cached prefix, or provider cache availability could produce the
observed counts. Stock also began with an existing cache advantage. OpenAI documents
that cache keys influence routing but do not guarantee a hit:
https://developers.openai.com/api/docs/guides/prompt-caching
This is API guidance, not direct telemetry from the benchmark's Codex backend.

The retained sanitized captures have neither per-stage prefix fingerprints nor
provider cache-routing decisions, so the exact mechanism is not recoverable from
this run. A decisive follow-up would record privacy-safe per-item prefix fingerprints
before and after replay/CTP, plus cache-key stability, then correlate the first changed
prefix position with provider cache hits. No implementation or live run was performed
for this diagnosis.
