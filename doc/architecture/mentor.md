# Router-owned subagent model schedule

## CTR-MENTOR-001 — Router-owned subagent model schedule

The Mentor Handoff owner in `internal/router` is a default-on Hpatch product schedule. It sits before Hpatch request projection and CTP
serialization. It authenticates the schedule boundary from Codex's exact thread-spawn header and
turn metadata, keeps process-lifetime child-thread counters, and changes only the provider-bound
model and reasoning effort. The ordinary request owner continues to supply input history, tools, and session
settings; Codex remains the owner of the configured model used after handoff.

A request-local observer runs before response transformations so it counts native provider output
items without depending on Hpatch carrier restoration or CTP decoding. The latest provider lifecycle
usage is the authoritative current-context input count; usage from separate requests is not summed.
The schedule commits completed output counts only after a
completed terminal response, but records observed input usage on later delivery or terminal failure
paths. This parsing exists only because the schedule behavior depends on the latest context size.
The transport capturer independently records the provider-bound model and usage. This owner is
separate from Hpatch recovery history and provider-client transport retries.
