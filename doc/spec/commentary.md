# Router commentary

## REQ-COMMENTARY-001 — User-only operation and subagent commentary

In Hpatch router mode, every non-strict function tool in the ordinary Responses `tools` catalog
with an object parameter schema receives one optional string property named `commentary`. A
nonblank authored value is shown as assistant commentary immediately before the call and is removed
before execution. An omitted or blank value produces no operation commentary. Strict tools,
provider-owned `additional_tools`, and tools that already own a `commentary` property keep their
schemas and arguments unchanged and receive no generic operation commentary. The opt-in [third-party subagent bridge](subagents.md) separately projects
collaboration schemas and restores native identities before commentary handling. Collaboration tools and tools whose
purpose is user messaging receive no generic operation commentary.

The central model instructions direct the agent to attach progress only through a supported tool's
commentary field or documented runtime mechanism. The agent does not originate standalone assistant
messages with `phase: "commentary"`; those messages are router-owned.

JSON and streaming responses preserve the same ordering. The streaming path buffers an eligible
function call until its complete arguments can be validated and stripped. The router retains the
provider's exact original call in the existing bounded session history, removes only its generated
message from later input, and restores the original call before provider replay. Malformed
router-owned commentary fails before the tool call is exposed.

Code Mode may publish runtime progress with `await commentary(value)`. The JavaScript parser
replaces only the reserved awaited call while preserving ordinary strings, comments, and unrelated
identifiers. Bash and POSIX shell programs may publish expanded text through the reserved
`commentary` command; the command writes nothing and succeeds without changing surrounding shell
control flow, redirections, output, or exit status. Other interpreters receive no runtime
commentary handling, and shell calls without an authored command receive no default.

Runtime publications use authenticated routes on the router's existing HTTP server.
Code Mode routes retain per-call identity. Bash and POSIX shell commentary is thread-scoped,
not attributed to an original tool-call ID. Workers discover private connection details using
the inherited `CODEX_THREAD_ID` and its thread-bound runtime. The shell transformation MUST NOT
add flags or inline environment assignments to carry those details. Concurrent shell workers
in one thread share delivery without guessing which original call produced a publication;
one worker finishing must not retire the other workers' publisher.
Ready streaming publications precede completed, failed, and incomplete terminal responses;
later publications and publications from JSON responses appear at the start of the next
non-concurrent request for the same session. Once a carrier has been handed off, an interrupted
provider response or early transform release does not cancel its publisher. Code Mode routes
prepared for carriers that were never handed off are cancelled instead. Shell thread routes
survive individual call and response lifetimes and expire when idle or at router shutdown.
Draining ready publications consumes each message once without retiring a still-running publisher;
subsequent publications remain deliverable through the same route. Code Mode completion retires
its route after queued publications are drained. Expiry releases queued events and route capacity.
Routes, events, request bodies, and retention time are bounded. Capacity, network, publication, and
rendering failures remain auxiliary and do not replace the tool result.
Shell commentary replay provenance follows stable thread identity across routing-session changes.
Its retention is independently bounded: exhausted commentary capacity suppresses new commentary,
never evicts executable-call replay or recovery records or prevents later tool calls.
The router retains shell provenance until shutdown, including across publication-route expiry,
for at most 256 threads and 16,384 message IDs in total. Reaching either bound leaves existing
replay provenance intact rather than reclaiming it for new commentary.
At a subagent stream terminal, ready shell commentary appears before substantive output inside
the terminal response object, not as a later standalone completed assistant item that could
replace the subagent's final answer.

In Hpatch router mode, a namespaced Codex `spawn_agent` function call produces one assistant
commentary message immediately before the unchanged call. It shows the model and reasoning effort
from explicit call arguments, falling back to the parent request when an argument is absent. It
shows `agent_type` as the role when present, using Codex's trimmed role-name semantics.
Role, model, and reasoning effort use inline-code formatting. Task names are left to Codex's
native display. The router does not read or project the encrypted `message` argument or read
Codex configuration files to produce commentary.

Namespaced `followup_task`, `wait_agent`, and `interrupt_agent` calls
receive distinct follow-up-requested, waiting, and interruption-requested commentary.
`send_message` adds no request notice, leaving interaction display to Codex. A requested call
is not proof of execution, delivery, or lifecycle completion. Sender and requested target labels
accompany projected communication notices. Reserved schemas and executed arguments remain unchanged;
encrypted message arguments are never read or exposed.

When a request receives an actual Codex inter-agent envelope addressed to its
canonical agent name, commentary identifies both recipient and sender. Valid
plaintext `MESSAGE` and `FINAL_ANSWER` payloads are shown in full as received replies,
never as excerpts. Replies exceeding the auxiliary rendering budget are omitted
from commentary without changing the original envelope. Encrypted envelopes show receipt
and direction only. Malformed items produce no projection. Original envelopes
and substantive answers remain intact in model-visible history.

Router-authored subagent commentary uses deterministic router-owned message IDs. The router removes
those messages from later provider-bound input while preserving the original collaboration calls,
tool outputs, and inter-agent messages. A response already accompanied by its deterministic
commentary is not projected again.

Every terminal root-agent or subagent response with provider usage includes one commentary message
reporting input (`i`), cached input (`ci`), output (`o`), and reasoning (`r`) tokens before the
provider-authored output. JSON and streaming responses use the same provider-authoritative counts
and rendering. A streamed subagent response carries usage in its terminal response object without
emitting a later standalone item that collaboration could mistake for the child result. Usage
commentary cannot become the terminal substantive result.

Child operation and runtime commentary carries a `[/root/worker] ` prefix from the request’s
canonical `agent_name` when `subagent_kind` identifies a child. Root and older unnamed clients
retain unprefixed commentary. An identical existing prefix is not duplicated. Runtime capabilities
bind their author at creation; thread provenance retains that author across route expiry and
session remapping, and deferred publications never borrow the draining request’s identity.
Runtime author admission and rendered publications share the 16 KiB auxiliary byte budget.
An oversized author suppresses capability creation; oversized rendered text is not retained,
while completion handling and substantive tool execution remain unchanged. This local budget
does not restrict valid Codex names or reject requests.
Hpatch retains observed canonical names and parent-thread relationships for bounded
root projection. It never infers ancestry from a name, message payload, or shared
routing-session ID. Missing ancestry, cycles, conflicting identity, or exhausted
auxiliary capacity suppress projection, not child output or tool execution.

Child operation, shell, and Code Mode commentary enters the same collector as
child collaboration notices, received inter-agent envelopes, and existing critical-error
notices. Errors are collected from the originating request before session-level deduplication,
never attributed from another request's retained session queue. Projecting an error
does not acknowledge the original session notice or
change its failure semantics. Each child keeps
its latest ordinary activity plus distinct notices, ordered by observation within
that child. Deduplication uses originating thread and source event identity, not
shared text. This is observed activity, not an inferred task objective or lifecycle
state; provider completion is not agent completion.

Root SSE transforms offer ready activity at response-event boundaries, including
before the terminal. JSON responses offer ready activity before substantive output.
Events observed before the current response are labelled “Subagent activity since
the last update.” No production response is held open, and no polling or model
turn is created. During an idle stream there may be no event boundary to deliver
through; once a response closes, updates wait for the next eligible root response.
This guarantees attributed deferred inline updates, not continuous wait-time display.

The collector retains at most 256 thread identities, 16,384 source identities,
1,024 pending events, and 64 pending events per child. Pending events expire after
one hour. Rendered root copies share a 16 KiB budget per response, after labels
are added. Exact root-copy IDs remain bound to stable root thread identity until
shutdown, across session remapping and event expiry. Capacity exhaustion never
evicts executable-call history or existing replay provenance. Root copies are
removed by exact retained ID from every replay, including a first child request
with inherited root history, without removing original child messages or tool results.

Acceptance:

1. Spawn-request commentary shows `agent_type` when present and the selected model and reasoning effort
   before the unchanged call without reading or projecting its encrypted `message` argument.
2. Collaboration tools remain unchanged. Spawn, follow-up, wait, and interruption commentary describes requests without claiming successful delivery or lifecycle completion; send-message calls add no request notice.
3. Received inter-agent envelopes identify both parties, including siblings and nested children. Plaintext replies are shown in full or omitted when they exceed the auxiliary rendering budget; encrypted content remains opaque and original model-visible items stay exact.
4. JSON and streaming responses expose the same messages and preserve the collaboration calls.
   Streaming buffers only a matched call until its complete arguments are available.
5. Router-authored messages are removed from every later provider request and are not repeated when
   the matching message is already present in Codex history.
6. Every terminal root-agent or subagent response with provider usage reports `i`, `ci`, `o`, and
   `r` exactly once before provider-authored output, without changing the terminal substantive
   result, provider usage object, or captured metrics.
7. Extensible ordinary function tools accept optional authored commentary, while strict,
   provider-owned, pre-owned-commentary, collaboration, and user-messaging schemas remain exact.
8. JSON and streaming calls show authored commentary before the executable item, remain silent
   without it, execute without the router-owned argument, and restore the exact provider call during replay.
9. Code Mode transforms nested reserved awaited forms inside-out without transforming occurrences
   in strings, template text, comments, regular expressions, properties, or unrelated identifiers.
10. Bash and POSIX shell commentary publishes expanded text without changing stdout, stderr,
    control flow, or exit status; absent publisher capacity leaves the command a successful no-op.
11. Ready and deferred runtime publications retain their routing identity: thread-scoped for shell,
    per-call for Code Mode. They remain bounded, are removed exactly on replay, and cannot replace
    a successful or failed tool result. Identical concurrent shell calls do not require guessed
    call attribution; completion of one does not interrupt the others' commentary.
12. Central model instructions keep agent-authored progress on supported tool calls and reserve
    standalone assistant commentary messages for router output.

### Critical session errors

Router failures that block work or require action produce bounded, actionable
user-only notices, not raw request data or event logs. Success and ordinary
cancellation are silent. Deduplication is by routing session and failure category.
Notices without a writable response remain queued for that session. A failed
render/write does not consume them. Ready root streaming notices precede provider
output; child notices appear before substantive output only in the terminal
response object, never as a later standalone child result. Exact retained IDs are
removed from subsequent provider-bound input, including passthrough requests.

The queue retains at most 256 session/category entries until shutdown. Concurrent
responses cannot claim the same pending notice. Repeats after delivery are
summarized only at shutdown; excess distinct entries become one overflow count.
The launcher reports pending notices and repeat counts after Codex exits. Delivery
remains auxiliary: HTTP failures, tool errors, exit codes, and substantive results
are preserved. A queue cannot deliver through an absent or broken transport.
