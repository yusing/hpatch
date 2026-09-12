# Router-owned main and subagent model schedule

## CTR-MENTOR-001 — Router-owned main and subagent model schedule

The Mentor Handoff owner in `internal/router` is a Mekugi product schedule with independent toggles:
subagent handoff defaults on, and main handoff defaults off. It sits before Mekugi request projection and CTP
serialization. It identifies main turns from valid `request_kind: turn` metadata and the absence of subagent markers,
and spawned subagents from Codex's exact thread-spawn header and turn metadata. It keeps
process-lifetime per-thread counters, and changes only the provider-bound
model and reasoning effort. The ordinary request owner continues to supply input history, tools, and session
settings; Codex remains the owner of the configured model used after handoff.

A request-local observer runs before response transformations so it counts native provider output
items without depending on HPATCH carrier restoration or CTP decoding. The shared terminal-payload
parse supplies the latest provider lifecycle usage as the authoritative current-context input count;
usage from separate requests is not summed.
The schedule commits completed output counts only after a
completed terminal response, but records observed input usage on later delivery or terminal failure
paths. The same parsed usage is passed to the transport capturer; neither consumer reparses it.
This owner is separate from HPATCH recovery history and provider-client transport retries.
