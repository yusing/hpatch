# Sol-medium paired rerun

One attempt per arm, stock versus Hpatch + CTP/2. Both task graders passed and capture evidence validation passed. The runner exited 1 because Hpatch violated the enabled no-edit-loop acceptance check: one file-read loop and one search loop. This is not a clean benchmark pass.

| Measure | Stock | Hpatch + CTP/2 |
|---|---:|---:|
| Requests | 34 | 28 |
| Input | 1,365,579 | 1,367,060 |
| Cached input | 1,306,112 | 1,287,552 |
| Uncached input | 59,467 | 79,508 |
| Final request input | 53,409 | 60,665 |
| Reported eligible-prefix shortfall estimate | 6,058 | 18,843 |

The uncached-input excess persists: 20,041 tokens (+33.7%). For these monotonically growing, single-attempt request sequences, it decomposes into 7,256 more final-context tokens and 12,785 more estimated repeated-prefix shortfall. The shortfall uses preceding provider input counts, not a measured hidden model-token prefix; it is not proof of an avoidable cache miss.

Hpatch request 2 is the dominant outlier: 15,796 input and zero cached tokens after request 1 had 14,421 input. All three observed prefixes are append-stable and both the actual outgoing route key and request cache key are stable. Stock request 2 reuses 9,728 cached tokens after 9,937 input. Their second-request shortfall difference is 14,212 tokens, exceeding the aggregate shortfall difference because later requests partly offset it.

Every comparable post-replay and provider prefix is append-stable, and every comparable route/request key is stable. Client comparisons are unavailable on the final two Hpatch requests because of the bounded retained item evidence; post-replay and provider evidence remains complete there. No observed prefix mutation or key rotation explains this run's cache outlier. These fingerprints do not expose provider tokenization, hidden prompt construction, cache readiness, residency, or internal routing, so they cannot establish the provider-side cause.

Conclusion: diagnostics are working, but the targeted routing fallback fix has not eliminated the measured uncached-input excess. A single Sol pair cannot establish a causal improvement over the earlier Astra pair. No automatic retry was performed.

## Follow-up: confirmed sticky-routing transport defect

Source inspection and failing local forwarding/retry tests established that the router discarded
the incoming `x-codex-turn-state` header. It returned the provider's response header to Codex but
omitted the echoed token from the upstream request allowlist. This affected both arms. Stable
`Session_id` and `prompt_cache_key` fingerprints therefore did not establish complete routing
preservation.

The [official Codex client](https://github.com/openai/codex/blob/main/codex-rs/core/src/client.rs)
documents a separate, provider-issued sticky-routing token that the client must echo unchanged
on requests within the same turn and clear for a new turn. The
[SSE transport](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/sse/responses.rs)
captures it from response headers. Dropping that token violates this transport contract even
when request prefixes and session keys are stable.

The router now forwards it statelessly, including retries. Local JSON/SSE tests cover passthrough,
Hpatch-native, and Hpatch+CTP/2, provider-to-client return, continuation echo, and no carryover into
a new turn in the same session. Capture evidence now privately compares current client/provider
turn-state headers separately from session keys; historical evidence without this field stays
unavailable. No artificial cache warmup, sleeps, extra model calls, or metric changes were added.

This is a confirmed local mechanism that can lose provider affinity, not proof that it caused
all 20,041 excess uncached tokens in this historical run. These captures did not retain the
header, and the 7,256-token final-context difference is independent of cache reuse. A newly
authorized live pair is still needed to measure the fix's provider-side effect.
