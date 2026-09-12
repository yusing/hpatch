# Provider-free context compaction

## REQ-COMPACTION-001 — Router-owned context pruning

Codex owns when to compact: its effective model context window, automatic
compaction threshold and counting scope, model changes, and manual compaction
requests remain unchanged. Mekugi does not rewrite Codex settings or schedule
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

### Evidence-preserving passes

The initial passes preserve user/developer instructions, corrections, authorization,
and the active execution frontier. Older ordinary-assistant narration can omit an
earlier byte-identical body when a later occurrence remains. Other prose, including
routine progress, stays unchanged; agent-message content remains byte-exact for
client reconciliation. Decisions, qualifications, outcomes, unresolved issues,
and continuation state remain intact. The approved policy permits loss of unmarked historical
details from finished operations inside an ongoing task. It does not claim that
completion proves irrelevance.

Before lossy retirement, evidence reducers can shorten redundant output:

- Terminal results from recognized direct `go test` calls retain pass/fail status
  and the distinct failed test names reported by `--- FAIL:` lines. Detailed
  runner output, diagnostics, package names, and successful test names are
  discarded, including for recent or explicitly referenced completed results.
  Live and unknown Go test results remain native.
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
- By default keep the newest eight tool invocations and their results. The
  budget policy below may relax this warm recency buffer, never the newest
  invocation/result, live/unmatched or duplicate identities, unknown completion
  states, or explicitly referenced calls and retained-script producers. The
  terminal Go test reduction above still applies inside this buffer. Referenced
  verified rows and numeric ranges may survive exactly in factual records instead of pinning their
  whole successful group. Reference matching decodes nested argument/result
  envelopes and supported escaped text; suspicious encodings retain evidence.
- Recognize native shell/exec calls, terminal stdin polls, static result-preserving
  Code Mode exec/poll carriers, static OpenAI documentation search/fetch/OpenAPI
  calls, and router-generated awaited apply-patch carriers. Static JSON-compatible
  JavaScript literals are parsed without execution; dynamic expressions remain native.
  A literal shell-carrier progress notice is retained verbatim and checked against
  the corresponding result part; its evidence references remain protected.
- Successful documentation results retain provenance, identities, hierarchy,
  pagination, annotations, and unknown metadata while recognized unmarked bodies
  may shrink. Selected provenance or diagnostic lines that are themselves
  oversized retain bounded prefix and suffix evidence with an explicit truncation
  marker. In truncated documentation, only historical body fragments with
  unambiguous boundaries may shrink; other provenance, warnings, references, and
  uncertain material remain. Reduction must not fabricate missing structure or
  represent a truncated result as complete. A known successful
  result need not shrink individually for its complete group to be profitable.
  Dynamic scripts and status-overwriting projections are not retirement evidence.
- Replace eligible calls/results with factual, versioned assistant-role records,
  not executable-looking truncated calls. Preserve relative timeline order;
  consecutive newly generated historical records may share explanatory framing.
  Keep exact shell invocation arguments, call identity, and observed completion
  metadata. Except for recognized terminal Go test results, keep test outcome lines
  and diagnostic excerpts. Standard Python tracebacks preserve the entire remaining
  output because multiline exceptions and notes have no reliable generic end marker.
- A completed failed command is eligible for removal of positively identified,
  unreferenced historical bulk, not removal of its failure. Except for recognized
  terminal Go test results, keep its exact invocation, exit status, error and
  diagnostic blocks, traceback chains, unresolved details, and referenced evidence.
  A terminal Go test keeps its invocation and exit status plus reported failed test
  names only. Terminal completion is not success or proof that a failure was resolved.
  Live and unknown completion states remain protected.
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
  unknown, and output inside the selected recent frontier is not made eligible
  by this output-only pass except for recognized terminal Go test results;
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

### Budget-first selection

The working-set selector targets 50,000 visible-string tokens with a maximum
30,000-token overshoot: no newly completed compaction may retain more than
80,000 tokens under this metric. Count the selected native item array at the
first replay boundary using `o200k_base`, excluding opaque `encrypted_content`,
request/tool framing, and native image URL payloads. Ordinary historical images
become text placeholders under the policy below. Encoded size and downstream
projection savings do not establish this target. This is not a complete provider-context count and does not
cap fresh instructions or subsequent input appended after the selected snapshot.

Selection evaluates retention plans in this order: keep the newest eight
operations and eight outputs; keep eight operations but only one output from
output-only reduction; then keep four, two, or one operations, still protecting
the newest output. These counts constrain eligibility, not hard protections:
user/developer authority, live/unknown states, references, diagnostic preservation
and complete reasoning/tool-group rules apply to every plan. No plan infers
irrelevance merely from age or completion. Metadata and narration reduction keep
their existing frontier. Output reduction precedes more aggressive group
retirement so native invocation and reasoning context can survive historical bulk.

Each candidate is compiled independently from the same original history and
measured before sealing. Stop at the first candidate reaching the target;
otherwise retain the smallest candidate within the overshoot allowance, preferring
the earlier plan on equal token counts. Do not escalate an already-small history
when the normal pass cannot shrink it. Repeated items each contribute to the
metric even when tokenization of their identical bytes is cached for the request.

If every evidence-preserving candidate exceeds 80,000 tokens, a budget-first
pass selects directly from the original history, targeting 50,000 tokens. This
pass is content-independent: unfamiliar tools, unique prose, diagnostic-heavy
output, large newest results, and oversized user text are not exempt from
the text budget. Developer/system instructions, model instructions, and canonical
context such as `AGENTS.md` remain byte-exact in every pass. If those required
items and the omission notice alone exceed 80,000 tokens, compaction fails
explicitly instead of truncating them. Otherwise the lossy policy overrides the
initial passes' exact-preservation rules. It does not prove irrelevance.

The pass first distributes bounded excerpts across the timeline, prioritizing
the latest real-user request, other user requests and recent/live work,
older decision/reasoning text, then older execution evidence. Before allocation,
eligible text can collapse adjacent byte-identical repetitions of short token
sequences, retaining representative occurrences and an explicit omitted count.
Different text between runs, including corrections, remains distinct. Repetition
mapping preserves the prepared content parts, unknown parts, and their metadata; only
a separately reported budget excerpt may replace the part array. Repetition
markers must save tokens, not just bytes. This is not fuzzy deduplication;
similar diagnostics with different values do not match.
Required instructions never enter repetition reduction.

Coverage is separated into request-chain, discussion, and execution families.
Before execution bulk can consume capacity, the request chain receives up to a
third of the available working space and discussion receives up to half of the
space then remaining. Weighted shares within each family redistribute capacity
as complete small items fit. The newest readable assistant or agent report first
receives up to a quarter of discussion's share, so a bounded continuation report
is not fragmented merely by a long older discussion. This is a recency preference,
not semantic recognition of a handoff or an unlimited exemption.
Remaining execution capacity and any unused surplus
can serve unfinished items; these reservations are not fixed per-item ceilings.
A latest acknowledgement must not displace the request it answers, and many
unknown tool results cannot crowd out every older decision. The target is not a
quota: a complete working set can be much smaller.
Required instructions are reserved first; the overshoot allowance can provide
working space when that mandatory floor consumes the normal target.
Small high-priority items stay exact. Oversized items keep bounded prefix/suffix
excerpts and selected constraint/status lines. Whole historical items may be
dropped if the excerpt framing itself would exhaust the budget. All rendered
items and omission markers participate in the final token measurement.

At every compaction, ordinary historical native image parts in messages and
tool outputs become literal `[Image]` text parts before text-budget selection.
Surrounding text, message identity, and non-positional metadata stay intact;
positional classifications reflect the replacement. This image-only pass does
not require unrelated text or opaque reasoning to be retired to manufacture
token savings. Subsequent text-pressure selection may still excerpt or drop
history under the rules above.

Fresh images on ordinary turns and mandatory instruction/context images remain
unchanged. There is no separate historical image-count or encoded-byte allowance;
the existing request/envelope size limits still apply. Native image payloads are
not tokenized as text. The placeholder explicitly accepts visual-detail loss,
without assuming earlier reasoning recorded a sufficient description. Carried
original user/agent images must not reappear after restoration.

Each pressure snapshot carries a content-free diagnostic report inside its
encrypted envelope, outside model input. It records original/retained token
counts per source item, priority and allocation reason, exact retention,
repetition reduction, excerpts, historical representation, and whole-item drops.
The aggregate includes the new omission notice and separate original/retained
image counts and encoded-byte totals, never fabricated vision-token usage. Installed-client probes log
these reports and the next request's visible-string count; the latter may also
include fresh context and later input. Reports are diagnostic evidence, not
claims that omitted content was irrelevant.

Native tool invocations, results, and reasoning become non-executable historical
observations together, avoiding truncated executable calls or partial native
reasoning/tool groups. Observed arguments, identities, failures, and live handles
have retention priority but are not an unlimited exemption. Opaque reasoning is
not interpreted. A visible notice identifies the lossy selection and warns that
older reference-retention notes may point to evidence no longer present. This
is extractive compaction, not a fabricated semantic summary; it cannot guarantee
task-critical semantic preservation for arbitrary content.

If neither text-token reduction nor historical image replacement is available for
an already-small history, compaction fails before sealing. Token-counting and
input/envelope-validation failures also prevent sealing.
Removing a V2 trigger is not token savings, and trigger-only input cannot produce
an empty capsule. Never fabricate a summary or provider usage, or fall back to
provider compaction. For WebSocket
`response.create`, the same admission failure emits an error event with status
422 and then closes the connection.

The retained timeline travels inline in an authenticated, encrypted
router-owned compaction item. Legacy output also carries selected real-user messages
for Codex's own user-input handling. Historical canonical instructions and
environment context stay only in the envelope, avoiding false fresh injections. V2 emits exactly one completed compaction
item. On subsequent requests, the router restores the timeline before any
projection or forwarding. It reconciles carried native items without duplicating
matched messages, preserves newly injected context and post-compaction input,
and restores selected items when Codex retained truncated versions. No-ID truncations
must uniquely match an authenticated original; ambiguous matches fail explicitly.
When budget pressure changes or drops carried user/agent messages, version 2
envelopes include their originals and replay positions solely for client
reconciliation. Those originals are never restored into model input or exposed
as a retrieval facility. They let full and client-truncated messages match without
resurrecting deliberately omitted content, including across repeated compaction.
Version 1 envelopes remain readable. Reconciliation evidence is subject to the
same bounded envelope decoding as the selected timeline.
Receipts preserve original timeline order and group aliases for the same item,
including when consecutive messages are dropped in different compactions.
Evidence plans carrying receipts keep original item positions instead of
consolidating adjacent records across possible instruction insertion points.
Message excerpts preserve native identity and non-positional metadata; content
classifications are rebuilt to match the selected part array.
Unmatched history from an earlier capsule fails closed when multiple envelopes
overlap; it must not be appended as if it were fresh context.
Unreconciled carried user content, including unsupported multimodal truncation,
also fails rather than becoming a duplicated request.
Fresh canonical instructions retain their current position even when their text
matches an older instruction.

The versioned local payload is never sent upstream as provider-encrypted state.
Existing provider-owned compaction payloads remain untouched; reasoning retirement follows the complete-group rule above.
Malformed, unknown-version, nested, unauthenticated, or unreadable local envelopes
fail closed. Both encoded requests and restored history obey router memory bounds.

The installation-owned key is `compaction.key` in Mekugi's configuration directory.
It is created lazily with owner-only permissions, shared safely across simultaneous
routers, and retained across process exits. Resuming on another installation needs
the same key. The original key is never silently replaced. There is no transcript
archive, session cache requirement, or model-operated retrieval step.

Acceptance checks cover local HTTP completion without provider calls, native
restoration with fresh context and suffixes, repeated compaction, restart and
concurrent key creation, damaged or missing keys, and conservative output pruning.
Budget checks cover candidate independence, output-first selection, non-monotonic
costs, exact target/overshoot boundaries, no-op and cancellation failures, repeated
item accounting, preserved native evidence, and admission before envelope sealing.
Pressure checks cover large unfamiliar output, live/failed exec results, unique
prose, user/agent messages, dynamic scripts, many small messages, required
instruction floors, carried truncation, and repeated compaction.
Installed Codex 0.154.0 has passed loopback legacy and V2 round trips with
synthetic ChatGPT authentication: automatic compaction with both counting scopes,
and the manual compact operation used by `/compact`, including pressure selection
for oversized requests. Pressure probes require the buried correction and complete
test-case output to survive repetitive user bulk within a small working set.
These scripted model fixtures establish client compatibility and specified fact
retention, not model reasoning quality. Dense, distinct histories also check
coverage of decisions, multiple failures, live handles, and current corrections.
A client-truncated user request is restored to the selected
full message or budget excerpt without duplication. Other versions and resumed client flows need corresponding runtime
coverage. Paired outcome evaluation is still required before claiming
an optimal output budget or task-critical semantic preservation on real histories.
