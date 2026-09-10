# Provider-free context compaction

## REQ-COMPACTION-001 — Router-owned context pruning

Codex owns when to compact: its effective model context window, automatic
compaction threshold and counting scope, model changes, and manual compaction
requests remain unchanged. Hpatch does not rewrite Codex settings or schedule
an earlier trigger.

The launcher preserves the `OpenAI` provider identity for its fixed ChatGPT
upstream while routing the base URL to the local listener. Codex uses that
identity to select remote compaction for both manual and automatic triggers;
an unrecognized provider name selects model summarization instead.
The invocation also disables Codex request compression because the local router
accepts uncompressed JSON, not ChatGPT Zstd request bodies.

The router handles `POST /v1/responses/compact` locally. It also handles streaming
`POST /v1/responses` and WebSocket `response.create` requests identified by
Codex metadata as `responses_compaction_v2`. None of these paths calls a provider.
Other metadata-tagged
compaction implementations fail explicitly instead of requesting a provider
summary. Local handling applies in both router modes. WebSocket continuation and
resumption restore local envelopes before projection, without forwarding local
response IDs or treating the restored timeline as provider-cached history.
A locally completed compaction replaces its WebSocket history with the capsule;
it does not retain the unpruned parent beside it. Steering cannot carry local
envelopes.

Compaction preserves user/developer instructions, corrections, authorization,
and the active execution frontier. Older ordinary-assistant narration can omit an
earlier byte-identical body when a later occurrence remains. Other prose, including
routine progress, stays unchanged; agent-message content remains byte-exact for
client reconciliation. Decisions, qualifications, outcomes, unresolved issues,
and continuation state remain intact. The approved policy permits loss of unmarked historical
details from finished operations inside an ongoing task. It does not claim that
completion proves irrelevance.

Before lossy retirement, evidence reducers can shorten redundant output:

- Successful Go test results with a structured result or native Codex exec header can omit routine run/pause/continue and
  pass lines. The exact call, exit status, package summaries, and other output
  remain.
- Successful search listings from recognized `rg`, `hgrep`, or `find` calls can
  be replaced with a reference to a later retained read result containing the
  exact same complete listing. The original call and completion metadata stay.
- Repeated file excerpts in completed successful tool results can be replaced
  with references to identical source rows retained in later results. Matching
  requires at least four consecutive complete `LINE:HASH` rows and 256 bytes.
  Unique text, call identity, completion metadata, and the later evidence remain.
  This includes Code Mode's completed `input_text` result arrays carrying
  structured shell output; it does not require interpreting the script.

Finished-operation retirement:
- Keep the newest eight tool invocations and their results, live/unmatched or
  duplicate identities, unknown completion states, and explicitly
  referenced calls or retained-script producers. Referenced verified rows and
  numeric ranges may survive exactly in factual records instead of pinning their
  whole successful group. Reference matching decodes nested argument/result
  envelopes and supported escaped text; suspicious encodings retain evidence.
- Recognize native shell/exec calls, terminal stdin polls, static result-preserving
  Code Mode exec/poll carriers, static OpenAI documentation search/fetch/OpenAPI
  calls, and router-generated awaited apply-patch carriers. Static JSON-compatible
  JavaScript literals are parsed without execution; dynamic expressions remain native.
  A literal shell-carrier progress notice is retained verbatim and checked against
  the corresponding result part; its evidence references remain protected.
  Successful documentation results retain provenance, identities, hierarchy,
  pagination, annotations, and unknown metadata while recognized unmarked bodies
  may shrink. In truncated documentation, only historical body fragments with
  unambiguous boundaries may shrink; provenance, warnings, references, and
  uncertain material remain. Reduction must not fabricate missing structure or
  represent a truncated result as complete. A known successful
  result need not shrink individually for its complete group to be profitable.
  Dynamic scripts and status-overwriting projections are not retirement evidence.
- Replace eligible calls/results with factual, versioned assistant-role records,
  not executable-looking truncated calls. Preserve relative timeline order;
  consecutive newly generated historical records may share explanatory framing.
  Keep exact shell invocation arguments, call identity, observed completion
  metadata, test outcome lines, and diagnostic excerpts. Standard Python
  tracebacks preserve the entire remaining output because multiline exceptions
  and notes have no reliable generic end marker.
- A completed failed command is eligible for removal of positively identified,
  unreferenced historical bulk, not removal of its failure. Keep its exact
  invocation, exit status, error and diagnostic blocks, traceback chains,
  unresolved details, and referenced evidence. Terminal completion is not
  success or proof that a failure was resolved. Live and unknown completion
  states remain protected.
- For successfully applied translated patches, keep affected paths, operation
  kinds, a patch digest, and exact application facts, diagnostics, and referenced
  evidence. Unreferenced verified source rows in a successful report may shrink
  under the same source-evidence rules as other completed output. The digest
  identifies the omitted patch body; it is not a retrieval mechanism.
- Preserve visible reasoning summaries. Retire opaque reasoning only with its
  complete eligible reasoning/tool group, before the recent frontier. A group
  containing unknown, crossing, or protected calls stays native.
  Evaluate savings for the complete reasoning/tool group, not each call in
  isolation: a small completion record may grow when the eligible group shrinks.
  Reference closure and profitability settle to a stable result before any
  replacements are applied. If profitability restores original content, every
  reference exposed by that content is followed before selection completes.
  Older completed shell outputs may also shrink without changing native calls or
  reasoning in a blocked group. Whole-output references stay intact; referenced
  full or partial verified rows and numeric ranges remain exact. Live,
  unknown, and recent output is not made eligible by this output-only pass;
  failed output obeys the diagnostic-preservation rule above.
  Existing provider-owned compaction items remain untouched.
  Factual records use a compact versioned representation with exact invocation
  values, ordered output parts, and unknown metadata. Unreferenced transport item
  IDs and transport turn/time bookkeeping may be omitted from retired records;
  explicitly referenced turn/time values remain exact. This does not change
  native event identity or discard substantive content.
- Preserve recorded reference targets across repeated compaction. Previously
  retired details cannot be reconstructed if work is reopened. No archival or
  model-operated retrieval facility is implemented yet.

Older native items with unique stable IDs may omit unreferenced nested transport
turn IDs and timestamps. Older ordinary assistant narration may also omit an
unreferenced transport item ID: the supported local Codex legacy and V2 workflows
do not carry those messages alongside the compaction item. User and agent-message
identities remain stable for carried-history reconciliation. Roles, phases,
content classifications, opaque payloads, substantive content, and unknown metadata
remain with their existing owners. Turn/time cleanup leaves no-ID, duplicate-ID,
malformed, unknown-kind, and recent items unchanged. Legacy and V2 carried messages
still reconcile by their original stable identity.

Operation completion is not task completion. Execution records report observed
facts without inventing scope closure, successful validation, or a workspace
version. User corrections and visible reasoning/decision text are not blanket-pruned.

If no supported reduction is available, compaction fails with HTTP 422. It does not
discard protected context just to fit a budget, report a fabricated summary,
or fall back to provider compaction. The target replay must retain 50,000 or fewer
visible-string tokens using the first native replay boundary and `o200k_base`,
excluding opaque reasoning and request/tool framing. This acceptance target is
not a universal cap on arbitrary histories or a complete provider-context count.
Encoded size and downstream projection savings do not establish this target.

The retained native timeline travels inline in an authenticated, encrypted
router-owned compaction item. Legacy output also carries original real-user messages
for Codex's own user-input handling. Historical canonical instructions and
environment context stay only in the envelope, avoiding false fresh injections. V2 emits exactly one completed compaction
item. On subsequent requests, the router restores the timeline before any
projection or forwarding. It reconciles carried native items without duplicating
matched messages, preserves newly injected context and post-compaction input,
and restores full items when Codex retained truncated versions. No-ID truncations
must uniquely match the authenticated original; ambiguous matches fail explicitly.
Unreconciled carried user content, including unsupported multimodal truncation,
also fails rather than becoming a duplicated request.
Fresh canonical instructions retain their current position even when their text
matches an older instruction.

The versioned local payload is never sent upstream as provider-encrypted state.
Existing provider-owned compaction payloads remain untouched; reasoning retirement follows the complete-group rule above.
Malformed, unknown-version, nested, unauthenticated, or unreadable local envelopes
fail closed. Both encoded requests and restored history obey router memory bounds.

The installation-owned key is `compaction.key` in Hpatch's configuration directory.
It is created lazily with owner-only permissions, shared safely across simultaneous
routers, and retained across process exits. Resuming on another installation needs
the same key. The original key is never silently replaced. There is no transcript
archive, session cache requirement, or model-operated retrieval step.

Acceptance checks cover local HTTP completion without provider calls, native
restoration with fresh context and suffixes, repeated compaction, restart and
concurrent key creation, damaged or missing keys, and conservative output pruning.
Installed Codex 0.153.4 has passed loopback legacy and V2 round trips with
synthetic ChatGPT authentication: automatic compaction with both counting scopes,
and the manual compact operation used by `/compact`. A large user request is
truncated by the client's V2 retention step, then restored in full without
duplication. Other versions and resumed client flows need corresponding runtime
coverage. Paired outcome evaluation is still required before claiming
an optimal output budget or task-critical semantic preservation on real histories.
