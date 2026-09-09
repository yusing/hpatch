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
`POST /v1/responses` requests identified by Codex metadata as
`responses_compaction_v2`. Neither path calls a provider. Other metadata-tagged
compaction implementations fail explicitly instead of requesting a provider
summary. Local handling applies in both router modes.

Compaction preserves all history items and their order. Its initial reducers
only change completed historical tool output:

- Successful Go test results with a structured result or native Codex exec header can omit routine run/pause/continue and
  pass lines. The exact call, exit status, package summaries, and other output
  remain.
- Successful search listings from recognized `rg`, `hgrep`, or `find` calls can
  be replaced with a reference to a later retained read result containing the
  exact same complete listing. The original call and completion metadata stay.
- The last tool result, failed or unfinished operations, live handles, unknown
  output formats, unsupported wrappers, compound commands, pipelines, dynamic
  shell expressions, and unrecognized commands remain unchanged.
- User corrections, commentary, reasoning, existing provider-encrypted items,
  decisions, identifiers, unknown fields, and current work are not blanket-pruned.

If no safe reduction is available, compaction fails with HTTP 422. It does not
discard protected context just to fit a budget, report a fabricated summary,
or fall back to provider compaction. The proposed 30k-token output target and
5k allowance remain an evaluation hypothesis, not an enforced limit or a proven
preservation guarantee. Encoded size is not the restored model context size.

The retained native timeline travels inline in an authenticated, encrypted
router-owned compaction item. Legacy output also carries original user messages
for Codex's own user-input handling. V2 emits exactly one completed compaction
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
Existing provider-owned compaction and reasoning payloads remain untouched.
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
