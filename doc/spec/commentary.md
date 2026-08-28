# Subagent activity commentary

## REQ-COMMENTARY-001 — User-only subagent activity details

In Hpatch router mode, a namespaced Codex `spawn_agent` function call produces one assistant
commentary message immediately before the unchanged call. It shows the exact spawn message and the
model and reasoning effort from explicit call arguments, falling back to the parent request when an
argument is absent. It shows `agent_type` as the role when present, using Codex's trimmed role-name
semantics. The router does not read Codex configuration files to produce commentary.

A namespaced `followup_task` call produces one commentary message immediately before the unchanged
call. It identifies the target and preserves the exact follow-up message. Other collaboration calls
receive no router-authored commentary.

When the root agent receives a plaintext Codex inter-agent `MESSAGE` or `FINAL_ANSWER`, the next
response begins with commentary identifying the sender and preserving the exact payload. Encrypted
or malformed inter-agent items remain unchanged and produce no projection.

Router-authored subagent commentary uses deterministic router-owned message IDs. The router removes
those messages from later provider-bound input while preserving the original collaboration calls,
tool outputs, and inter-agent messages. A response already accompanied by its deterministic
commentary is not projected again.

Acceptance:

1. Spawn commentary shows `agent_type` when present, the selected model and reasoning effort,
   and the exact spawn message before the unchanged call.
2. Follow-up commentary shows the target and exact message before the unchanged call; other
   collaboration tools remain unchanged and receive no commentary.
3. Plaintext `MESSAGE` and `FINAL_ANSWER` items from a subagent to `/root` produce one commentary
   containing the exact payload without removing or changing the model-visible item.
4. JSON and streaming responses expose the same messages and preserve the collaboration calls.
   Streaming buffers only a matched call until its complete arguments are available.
5. Router-authored messages are removed from every later provider request and are not repeated when
   the matching message is already present in Codex history.
