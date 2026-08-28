# Router-owned compact provider representation

## CTR-CTP-001 — Router-owned compact provider representation

One CTP/2 owner in `internal/router` sits after Hpatch request projection and before provider
serialization. It receives ordinary parsed Responses fields, preserves the existing top-level or
developer-message instruction carrier, transforms each eligible string independently, and returns
one response transformer with the request's visible prior-output sources. It does not parse
HPATCH/2, change the tool registry, own provider usage, retain cross-request history, define model
instructions, or define another executor carrier. Persistent CTP/2 interpretation and emission
guidance belongs to `contrib/codex/file-editing-instructions.md`.

Content-local dictionaries hide discovery and profitability behind one string interface. Tool
outputs use a deeper visible-line path that can reference exact line ranges from preceding visible
tool outputs and falls back to that content-local interface. Both paths compare the complete JSON
string representation before selecting compact output. Each request rebuilds sources in input order,
so appending history preserves earlier bytes and compaction or branching removes unavailable state.

On responses, the CTP/2 transformer runs before the existing Hpatch transformer. It restores
content-local dictionaries and visible-line references only in assistant text for complete JSON
output and SSE terminal text. Tool names, inputs, and arguments remain native for ordinary registry
routing, translation, history, recovery, and carrier rendering. The transport owns the minimal
response-transformer composition needed to preserve that order and discard request-local sources on
every terminal path.

The CTP/2 owner derives auxiliary native-versus-compact token pairs from the same tokenizer used for
per-string selection. Input counts cover each active whole request after Hpatch projection; output
counts cover decoded assistant text once per logical content item, using each terminal
`response.output_text.done` event for streams while excluding repeated projections. The in-memory
router metrics retain aggregates plus bounded per-session request and CTP input/output observations,
including bytes, encoded-string and visible-reference counts, dictionary size, codec timing, and
dropped-observation counters. Counting failure cannot replace a successful request, response, or
provider usage record. Activation counters expose a missing instruction carrier without retaining
prompt text.

CTP/2 operates inside native Responses envelopes and may rewrite only the representation identified
by `REQ-CTP-001`; that requirement retains the provider-owned fields and native fallback contract.
The existing validated compaction bypass precedes this seam, so requests without active CTP/2
guidance remain native in both directions.
