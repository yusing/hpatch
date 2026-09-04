# Router-owned commentary projection

## CTR-COMMENTARY-001 — Router-owned operation and subagent commentary projection

The Responses router owns optional commentary schema projection for extensible ordinary function
tools, default selection for eligible structured calls, removal of only its own argument, assistant
message rendering, and exact replay restoration. Provider-owned and strict schemas remain exact.
Collaboration calls remain outside generic operation commentary so the distinct subagent contract
below stays authoritative. The existing bounded Hpatch call history retains original call identity
and router message IDs; JSON and SSE transformers share that state without adding another replay
store. Streaming keeps subagent and generic structured pending calls separate because their
validation and completion policies differ.

The router also owns one bounded authenticated in-process publication broker. Code Mode lowering
uses the JavaScript syntax owner when available and routes the evaluated expression through the
existing shell worker carrier. The CGO-disabled detector only fails closed for the reserved awaited
form. The Bash/POSIX evaluator intercepts the reserved command after ordinary expansion and turns
it into a successful no-output command. Both runtime paths use opaque per-call capabilities and the
same broker; they do not own executor results, shell process status, or Codex session control.
Publications ready at stream completion are drained before the terminal event, while later and JSON
publications are drained by the next non-concurrent request for the retained session. Limits and
publication failures are auxiliary.

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
input, cached-input, output, and reasoning usage whenever a root or subagent turn stops. It consumes
the counts from the shared terminal-payload parse rather than decoding usage again. The
projection precedes provider-authored output so it cannot replace a collaboration result. The
streaming path does not emit a later standalone usage item for a subagent turn because the Codex
collaboration runtime selects the last completed assistant item as the child result. The provider
usage object remains authoritative; the projection remains in the terminal response object and
does not participate in capture calculations or durable metrics.
