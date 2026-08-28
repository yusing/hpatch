# Spawned-subagent Mentor Handoff

## REQ-MENTOR-001 — Spawned-subagent Mentor Handoff

Mentor Handoff is an opt-in Hpatch-mode model schedule. Passthrough mode rejects the option. Its
first incremental form recognizes only an AgentControl thread spawn carrying exactly one
`x-openai-subagent: collab_spawn` header and valid Codex turn metadata whose `subagent_kind` is
`thread_spawn`. The router forwards the subagent header unchanged. It does not infer a subagent from
thread lineage, thread-source classification, a new routing session, a fork, compaction, or
instructions. Requests outside this exact boundary remain unchanged.

For a recognized child request whose configured model is exactly `gpt-5.6-luna` or
`gpt-5.6-terra`, the router replaces only the top-level request model with `gpt-5.6-sol` and the
reasoning effort with `high`, preserving other reasoning members, input history, tools, metadata,
and request fields. This happens before Hpatch projection, CTP preparation, provider serialization,
and actual-model metrics attribution. Codex continues to construct later requests from its session
settings; the router never rewrites response model metadata.

The router retains one bounded schedule per child thread. Completed provider responses contribute
one count for each `custom_tool_call` or `function_call` output item and each assistant `message`
output item. Streaming counts `response.output_item.done`; a terminal response output is only a
fallback when no done item was observed. Provider-reported `input_tokens` contribute on completed
and failed delivery paths whenever usage was observed. Tool calls and messages contribute only for
a completed terminal response.

After the mentor reaches three tool calls, it remains active for one more completed response so it
consumes the third call's result. A failed response in that position retains the mentor unless the
input budget is reached. The handoff completes after that result-consuming response, after two
assistant messages, or when the latest request reports at least 50,000 input tokens. A request's
input count already includes its inherited conversation history, so counts from separate requests
are never summed. The completed request may overshoot the token limit.
The next request from that child uses the model and reasoning supplied by Codex without a compatibility
rewrite. Child schedules are retained for the router lifetime so a completed schedule is never
silently forgotten and restarted. State and progress logs retain counts and identifiers only, not
prompt or response content. Metrics charge every request to the model actually sent upstream.
