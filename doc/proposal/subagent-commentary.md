# Hpatch-only subagent commentary

Status: implemented in Hpatch; stock-client synthetic playback verified on
2026-09-08. Real-agent smoke testing remains a separately requested operation.

## Audit and playback evidence

The router's existing attribution, publication broker, collaboration projection,
and diagnostic dispatcher support this design without changing Codex. Root-copy
state is separate from executable-call history; ancestry comes from observed
thread metadata, not agent-path text or a routing-session match.

Playback used installed stock `codex-cli 0.153.4` from its standalone release,
not the Codex checkout. Hpatch alone was built. A bundled model catalog and
loopback-only proxy destinations kept the experiment local. The stock TUI showed:

- `stream`: alpha reading, beta testing, then alpha's received reply, each with
  one author prefix and an unchanged substantive fixture result.
- `deferred`: the fixed shell no-op completed, then the next diagnostic
  continuation showed beta once under “Subagent activity since the last update.”
- `wait`: the native “Waiting for agents” row appeared before the synthetic
  alpha update. “Finished waiting” appeared later, followed by the local fixture
  continuation. The unchanged native row also said “No agents completed yet.”

The wait case demonstrates display during a native wait while a diagnostic stream
remains open. It does not authorize production stream holds. Production delivers
at available response-event boundaries and defers through idle or closed streams.
JSON/SSE and provider-replay invariants are checked at the router boundary; these
synthetic observations do not prove live child execution or lifecycle discovery.

## Outcome and scope

Make subagent work understandable in the **stock Codex TUI**, through Hpatch's
existing user-only commentary. Show who is doing what, distinguish communication
from follow-up work, and make the delivery delay explicit.

All implementation belongs in Hpatch. The Codex clone is a read-only reference.
Do not modify or rebuild Codex, add a TUI panel, change native collaboration
schemas, introduce another scheduler, or install a different client. A dashboard
is not a substitute for the requested inline commentary.

Start with diagnostic playback before implementing production forwarding. Use
the installed, unmodified Codex client for that experiment, not a binary built
from the previously edited clone.

## Existing support

There is already a router-local playback command:

```text
:hpatch_diag cat_write_translation
```

It replaces provider responses with fixed payloads, passes them through the normal
router transformations and host tools, and removes its generated transcript from
subsequent provider input. The dispatcher also accepts `subagent_commentary` with the stream, deferred,
and catalog-supported wait fixtures described below.

Other useful existing pieces:

- Tool commentary and shell/Code Mode runtime publication already exist.
- Child operation/runtime commentary is attributed to `agent_name`; the shared
  collector additionally projects it through observed ancestry into root commentary.
- Plaintext inter-agent replies already project into root commentary. Encrypted
  replies and task prompts cannot be treated as readable text.
- Codex supplies canonical agent names and parent-thread metadata. Hpatch can
  retain the fields it actually needs to associate child activity with a root.
- Native spawn, messaging, waiting, interruption, and result delivery remain
  Codex-owned. A provider response finishing is not proof that an agent's
  assignment has finished.

## What the user sees

Illustrative root commentary, generated from observed activity:

```text
[/root/hook_policy] Reading hook failure handling.
[/root/guard_review] Running the focused router tests.
[root -> /root/guard_review] Follow-up requested.
[/root/hook_policy -> /root/guard_review] Message requested: check the cancellation path too.
[/root/guard_review <- /root/hook_policy] Message received: check the cancellation path too.
[/root/hook_policy] Reply received: one failure path needs attention.
```

Agent-to-agent commentary identifies both sender and recipient, including sibling
agents and nested children. Distinguish a requested send from observed receipt;
show message text only when plaintext is available. Otherwise show the direction
and event without inventing or decrypting the payload. Project these events to the
same root feed without changing their actual recipient or waking another agent.

For updates delivered after the root resumes:

```text
Subagent activity since the last update:
- /root/hook_policy: checked failure handling; reply received.
- /root/guard_review: tests started; no result observed yet.
- /root/hook_policy -> /root/guard_review: cancellation-check message received.
```

These are examples of content, not permission to invent summaries. Prefer the
agent's existing authored tool/runtime commentary. Otherwise use a concise,
recognizable tool description. Do not copy entire shell programs, tool arguments,
environment assignments, or raw outputs into the feed.

Task text is optional evidence, not a required new model field. Show it only when
available as plaintext. An opaque task name is an identity label, not an inferred
objective. Received reply excerpts must be labelled as excerpts when shortened;
the original model-visible message and substantive answer remain intact.

## Proposed design

### 1. Collect existing child activity

Observe child requests/responses and existing runtime publications at the router
boundary. Reuse the existing commentary producer rather than asking agents to
send additional status messages or introducing an extra summarizing model call.

Keep only the bounded information required for presentation: originating thread,
canonical name, observed parent relationship, source event/call identity, event
kind, concise text, and observation time. Use request metadata for identity, not
path-looking strings inside message text.

Associate descendants with a root only through observed thread relationships.
Unknown ancestry must not cause broadcast delivery or guessing from a shared
routing-session identifier. Keep the child's normal output available even when
root projection is unavailable.

### 2. Extend collaboration commentary separately

Keep reserved collaboration schemas and executed arguments unchanged. At the
existing collaboration-call projection boundary, distinguish:

- spawn requested;
- message sent/requested, according to the available execution evidence;
- follow-up requested;
- waiting for agent updates;
- reply received, when the actual inter-agent envelope is observed.

Do not read encrypted `message` arguments. A call being emitted does not prove
successful delivery. Do not infer an agent's completion, failure, or interruption
from a generic HTTP terminal event, silence, or the word "completed" in prose.

Hpatch can add useful commentary beside stock collaboration rows. It cannot
rename or remove those native rows, including stock wait-result wording.

### 3. Project into the root's available response stream

Reuse router-owned assistant commentary and replay provenance. Coalesce ordinary
operation updates into a small per-agent latest-activity summary; retain distinct
reply/error notices within the same bounded auxiliary budget. Deduplicate by
source identity, not by matching text from different agents.

Deliver ready updates while a writable root response is available, and deliver
queued updates at the next eligible root response. Mark deferred batches as
activity observed since the last update, not as a claim about the exact current
state. Preserve event order within an agent; do not fabricate a total execution
order across concurrent agents.

This needs root-copy provenance: generated root messages must be removed on root
replay without removing original child messages or changing executable-call
history. Attribution must survive deferred delivery, session remapping, expiry,
and concurrent children. Apply byte limits after labels are added. Capacity or
rendering failures suppress auxiliary updates, never tools or results.

### 4. Be precise about live delivery

Once a root Responses stream has ended, Hpatch cannot write more commentary into
that closed response. The default design therefore guarantees **attributed,
deferred inline updates**, not uninterrupted live visibility throughout every
native wait.

There is nevertheless a worthwhile experiment: the inspected Codex tool runtime
can dispatch a tool before the response stream terminates. Test whether stock
Codex renders further commentary while such a tool is waiting. Do not repeat the
earlier assumption that tools necessarily start only after response completion.

Intentionally holding a terminal response open would be a separate production
decision. It could delay result handoff, interact with timeouts, or stall the next
root request. A successful display demo alone does not authorize that behavior.
Do not add artificial model turns, repeated polling calls, or unbounded stream
holds to conceal the closed-response limitation.

## Diagnostic-first implementation

The diagnostic dispatcher supports these commands:

```text
:hpatch_diag subagent_commentary
:hpatch_diag subagent_commentary stream
:hpatch_diag subagent_commentary deferred
:hpatch_diag subagent_commentary wait
```

The default runs a short deterministic sequence. Named cases isolate delivery
behavior. All messages clearly identify themselves as diagnostic fixtures.

| Case | What it establishes |
| --- | --- |
| Stream | Interleaved alpha/beta activity renders in the stock root TUI with correct attribution and no duplicate prefixes. |
| Deferred | Events queued after a response boundary arrive on the next diagnostic continuation, once, in the right root session. |
| Wait | Further commentary is attempted while a catalog-supported native wait is executing and the diagnostic SSE stream remains open. Record the observed display behavior rather than assuming success. |
| Replay and result | Returning to an ordinary user turn strips diagnostic/root-projection items while preserving unrelated history and the exact substantive result. |

Fixture events must traverse the proposed collection, attribution, queue, and
projection code. Merely printing preformatted agent labels would test appearance
but not the feature. Backend tests additionally cover capacity, cancellation,
concurrent roots, missing ancestry, malformed metadata, and late publications.

Keep playback local and deterministic: no provider calls, real subagent spawns,
encrypted-prompt fabrication, user scripts, or workspace edits. If a host tool is
needed for continuation or the wait case, use only a fixed, bounded payload through
the normal host authorization path. Never interrupt existing agents. Respect
native wait limits; if the required tool is absent, report that case unavailable
instead of substituting a different operation.

Synthetic child events do not prove real child execution or lifecycle discovery.
After local playback works, a separately requested smoke test with ordinary stock
Codex delegation can establish that real child traffic reaches the same collector.
Do not silently turn a provider-free diagnostic into a live model run.

## Delivery sequence and checks

1. **Playback proof:** implement the new diagnostic target and minimal shared
   collection/projection path. Exercise it in stock Codex and record immediate
   versus deferred display behavior. No production stream-lifetime changes.
2. **Production wiring:** attach actual child commentary and observed metadata,
   add collaboration-call descriptions, and enable bounded root projection using
   the delivery behavior established by playback.
3. **Focused validation:** run Hpatch's router tests and the diagnostic cases.
   Verify JSON/SSE parity, two concurrent roots, nested agents, duplicate delivery,
   missing/encrypted content, cancellation, expiry, and unchanged final answers.
4. **Documentation:** update the existing commentary and diagnostic requirements,
   their ownership contract, and the relevant README guidance. Keep README guidance explicit that no new Codex panel is part of this feature. Leave the
   Codex checkout untouched.

This work does not require compiling Codex, V8, or the full Codex test suite.
Existing Hpatch attribution is reused. Unrelated pending edits remain outside
this feature's scope.

## Acceptance

- The installed stock Codex client displays useful, attributed child activity in
  root commentary without a client patch or extra agent reporting calls.
- The playback command provides a reproducible demonstration with no provider
  access, and clearly separates synthetic coverage from real-agent coverage.
- Root and child identities cannot cross-contaminate concurrent sessions.
- Ordinary tool execution, original inter-agent messages, cancellation, and final
  answers are unchanged; generated commentary stays out of provider replay.
- The documentation states exactly when updates are immediate or deferred. If
  continuous wait-time display cannot be provided at the router boundary, report
  that constraint instead of changing Codex or claiming a live feed.

## Source pointers

- [Current diagnostic contract](../spec/router-diagnostics.md)
- [Current diagnostic dispatcher](../../internal/router/diagnostic_playback.go)
- [Commentary requirements](../spec/commentary.md)
- [Commentary ownership](../architecture/commentary.md)
- [Existing collaboration projection](../../internal/router/subagent_commentary.go)
- [Runtime broker](../../internal/router/commentary_publisher.go)
- Read-only Codex evidence: `core/src/responses_metadata.rs`,
  `core/src/tools/parallel.rs`, `core/src/stream_events_utils.rs`, and
  `core/src/tools/handlers/multi_agents_v2/wait.rs` beneath `codex-rs`.
