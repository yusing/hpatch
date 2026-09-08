# Router commentary

## REQ-COMMENTARY-001 — User-only operation and subagent commentary

In Hpatch router mode, every non-strict function tool in the ordinary Responses `tools` catalog
with an object parameter schema receives one optional string property named `commentary`. A
nonblank authored value is shown as assistant commentary immediately before the call and is removed
before execution. An omitted or blank value uses a concise tool-specific default. Strict tools,
provider-owned `additional_tools`, and tools that already own a `commentary` property keep their
schemas and arguments unchanged and receive defaults only. The opt-in [third-party subagent bridge](subagents.md) separately projects
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
shows `agent_type` as the role when present, using Codex's trimmed role-name semantics. It does not
read or project the encrypted `message` argument. The router does not read Codex configuration
files to produce commentary.

A namespaced `followup_task` call and other collaboration calls receive no router-authored
commentary and remain unchanged.

When the root agent receives a plaintext Codex inter-agent `MESSAGE` or `FINAL_ANSWER`, the next
response begins with commentary identifying the sender and preserving the exact payload. Encrypted
or malformed inter-agent items remain unchanged and produce no projection.

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

Acceptance:

1. Spawn commentary shows `agent_type` when present and the selected model and reasoning effort
   before the unchanged call without reading or projecting its encrypted `message` argument.
2. Follow-up and other collaboration tools remain unchanged and receive no commentary.
3. Plaintext `MESSAGE` and `FINAL_ANSWER` items from a subagent to `/root` produce one commentary
   containing the exact payload without removing or changing the model-visible item.
4. JSON and streaming responses expose the same messages and preserve the collaboration calls.
   Streaming buffers only a matched call until its complete arguments are available.
5. Router-authored messages are removed from every later provider request and are not repeated when
   the matching message is already present in Codex history.
6. Every terminal root-agent or subagent response with provider usage reports `i`, `ci`, `o`, and
   `r` exactly once before provider-authored output, without changing the terminal substantive
   result, provider usage object, or captured metrics.
7. Extensible ordinary function tools accept optional authored commentary, while strict,
   provider-owned, pre-owned-commentary, collaboration, and user-messaging schemas remain exact.
8. JSON and streaming calls show one explicit or default message before the executable item,
   execute without the router-owned argument, and restore the exact provider call during replay.
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
