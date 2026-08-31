# Router-owned subagent commentary projection

## CTR-COMMENTARY-001 — Router-owned subagent commentary projection

The Responses router owns detection of the `spawn_agent` function from the request's configured
namespaced tool catalog and rendering deterministic assistant commentary from non-message call
arguments. It does not inspect the encrypted spawn message or render commentary for `followup_task`.
It does not read Codex configuration files. The Codex collaboration runtime continues to own
validation, agent creation, delivery, effective execution, and the original tool results.

The same request boundary recognizes plaintext inter-agent messages addressed from a child path to
`/root`. It projects their payload for the user without replacing the original item. Deterministic
router IDs let request preparation remove only router-authored commentary before provider dispatch
and suppress a projection already present in Codex history. JSON and SSE response transformers own
equivalent ordering; the streaming path buffers only matched function-call framing until arguments
are complete.

The terminal response transformer also owns one user-only commentary projection of the provider's
input, cached-input, output, and reasoning usage whenever a root or subagent turn stops. The
provider usage object remains authoritative; the projection does not participate in capture
calculations or durable metrics.
