# Router-owned commentary projection

## CTR-COMMENTARY-001 — Router-owned operation and subagent commentary projection

The Responses router owns optional commentary schema projection for extensible ordinary function
tools, authored commentary for eligible structured calls, removal of only its own argument, assistant
message rendering, and exact replay restoration. Provider-owned and strict schemas remain exact, except for the separately owned opt-in
[third-party collaboration projection](subagents.md).
Collaboration calls remain outside operation commentary and pass through without generated
request notices or commentary-specific buffering. The existing bounded Hpatch call history
retains original call identity and router message IDs; JSON and SSE transformers share that
state without adding another replay store.

The router also owns one bounded authenticated in-process publication broker. Code Mode lowering
uses the JavaScript syntax owner when available and routes the evaluated expression through the
existing shell worker carrier. The CGO-disabled detector only fails closed for the reserved awaited
form. The Bash/POSIX evaluator intercepts the reserved command after ordinary expansion and turns
it into a successful no-output command. Both runtime paths use opaque capabilities and the
same broker; they do not own executor results, shell process status, or Codex session control.
Code Mode retains per-call capabilities. Shell uses a shared thread capability discovered through
private runtime data keyed by inherited `CODEX_THREAD_ID`, with no added command flags or inline
environment assignments. The runtime owner binds discovery to the current worker and owns its
private descriptor cleanup. Discovery and publication failures are silent and auxiliary.
Shell publications retain thread identity, not an inferred original call ID. Deferred and terminal
drains select the originating shell thread as well as the routing session, so a different thread
sharing that session cannot consume its publications. Per-call Code Mode delivery stays session-scoped. Shell worker
completion cannot retire a shared thread route; idle expiry and router shutdown own that lifetime.
Exact shell replay provenance follows stable thread identity rather than the current routing
session and has a separate bounded budget. Commentary retention cannot reclaim tool-call history
or prevent tool-call admission. Child terminals prepend ready runtime commentary inside the terminal
response object without emitting standalone completed assistant items after the child's answer.
The response transformer owns each Code Mode subscription until its carrier/history handoff boundary;
thereafter publisher completion and broker expiry own its lifetime. Transform release cancels only
unhanded subscriptions. Publications ready at every stream terminal status are drained before the
terminal event, while later and JSON publications are drained by the next non-concurrent request
for the retained session. Token and
session drains share one completion-sensitive primitive: consume queued events once, retain active
publishers, and retire completed Code Mode routes after delivery. Both drain boundaries expire stale routes
and release their queued-event accounting. Limits and publication failures are auxiliary.

Child operation and runtime commentary carries a `[/root/worker] ` prefix from the request’s
canonical `agent_name` when `subagent_kind` identifies a child. Root and older unnamed clients
retain unprefixed commentary. An identical existing prefix is not duplicated. Runtime capabilities
bind their author at creation; thread provenance retains that author across route expiry and
session remapping, and deferred publications never borrow the draining request’s identity.
Runtime author admission and rendered publications share the 16 KiB auxiliary byte budget.
An oversized author suppresses capability creation; oversized rendered text is not retained,
while completion handling and substantive tool execution remain unchanged. This local budget
does not restrict valid Codex names or reject requests.
`subagent_activity.go` owns bounded observation and root-copy provenance, independent
of executable-call recovery. The request boundary supplies only observed canonical
identity and parent-thread metadata. Stable thread relationships, never session IDs
or path-looking message text, select the root. Conflicting identities fail closed.
The commentary producer and publication broker feed the collector without consuming
child output. Runtime capabilities bind their originating thread at creation.
The collector does not call back into the broker or proxy while holding its lock.

The collector coalesces ordinary child operations and retains distinct completed child-authored
commentary, received replies, and critical-error notices. It deduplicates source identities per
thread and expires pending
events. Non-evicting thread/source and exact root-copy provenance budgets prevent
replay leakage after session remapping or expiry without displacing tool history.
Exact retained root-copy IDs are stripped from any provider replay, including
new child requests inheriting root history before their ancestry is registered.
Error collection observes the originating request's safe description at record
time, before session deduplication, without acknowledging the original notice.
The root transformer drains atomically at JSON and SSE event boundaries under a
per-response rendered byte budget. Root copies precede substantive output; idle or
closed streams defer delivery rather than extending stream lifetime. Concurrent
root responses cannot drain the same event twice. Codex retains scheduling,
recipient selection, interruption, waiting, and assignment lifecycle ownership.

Codex owns collaboration-call display. The router leaves collaboration schemas, executable
arguments, and streaming call framing exact and never reads encrypted message arguments.
Completed child assistant commentary enters the existing collector at JSON output and SSE
completed-item boundaries. The original child item remains unchanged; root copies use observed
ancestry and source identity without replacing final answers or entering provider replay.

The input boundary recognizes actual inter-agent envelopes addressed to the current canonical
agent, including sibling and nested traffic. Plaintext replies are shown in full, never excerpted;
replies exceeding the auxiliary rendering budget are omitted. Encrypted receipt is direction-only.
Original model-visible envelopes remain unchanged. Deterministic router IDs suppress repeated
local commentary on replay.

The terminal response transformer also owns one user-only commentary projection of the provider's
input, cached-input, output, and reasoning usage when a completed root or subagent response
contains a final assistant answer and no client-dispatched tool calls. Rendering uses full metadata-style labels
on separate lines with inline-code numeric values. Intermediate commentary and failed or
incomplete responses do not trigger usage commentary. Final-answer phase identifies the answer;
unphased assistant answers support older clients. Counts from the shared terminal-payload parse
accumulate by stable originating thread, independently of routing-session and compaction lifetimes.
Root and child totals remain separate, and repeated terminal observations within a request count once.
`thread_usage.go` owns bounded, non-evicting totals until router shutdown; ancestry and author
metadata do not own token attribution. The
projection precedes provider-authored output so it cannot replace a collaboration result. The
streaming path does not emit a later standalone usage item for a subagent turn because the Codex
collaboration runtime selects the last completed assistant item as the child result. The provider
usage object remains authoritative; the projection remains in the terminal response object and
does not participate in model-origin output accounting. It remains present in transport byte
and token totals. `internal/commentaryid` owns the reserved operation/runtime and subagent/usage
message ID namespaces shared by rendering, replay, and capture classification; message text and
phase do not establish generated provenance.

The router owns a separate bounded critical-notice queue because request failures
may occur before tool-call history or publication capabilities exist. The launcher
owns that queue's lifetime through router shutdown and terminal fallback. The
response transformer reserves notices by routing session, confirms only successful
writes, and strips exact generated IDs on replay. It never changes provider or
executor failure semantics. Operational log sinks are not part of this boundary.
