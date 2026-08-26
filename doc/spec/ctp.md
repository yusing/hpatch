# Compact token protocol contract

## REQ-CTP-001 — Lossless model-visible data-plane encoding

`hpatch-router --model-protocol ctp1` enables Compact Token Protocol version 1 for Hpatch-mode
Responses traffic. `--model-protocol native` is the default. CTP is a reversible representation
layer between the existing Hpatch request projection and the upstream model provider; it is not a
second Responses protocol or an edit-engine feature.

CTP leaves the Responses control plane native. Request and response objects, item types, roles,
instruction priority, identifiers, statuses, reasoning configuration and items, stream framing,
usage, conversation selection, and compaction lifecycle retain their provider contracts. CTP may
change only eligible model-visible request strings and assistant text. Tool names, newly emitted
tool inputs, function arguments, grammar definitions, and JSON schemas remain native.
Model guidance MUST present CTP decoding as an inline representation and direct the model to
continue the task's ordinary workflow without inspection or tool calls for CTP itself.
Model guidance MUST confine CTP tags, references, and response definitions to assistant text. It
MUST direct shell calls to omit `workdir` by default and to use only an exact existing absolute path
when an override is necessary; placeholder paths are not executable values.

### Exact-string dictionary

CTP uses exact strings rather than templates or parameterized values. A definition contains no
reference and expands without normalization, Unicode folding, reformatting, or newline conversion.
Identifiers contain lowercase base-36 digits and share one namespace within a compact request and
its provider response.

`contrib/codex/file-editing-instructions.md` owns the durable interpretation and emission rules.
The router selects the native Codex instruction carrier: a nonempty string-valued top-level
Responses `instructions` field when present, otherwise the first textual developer message in
`input`. It appends the request dictionary without changing the original carrier prefix or its
priority:

```text
!ctp1 D
0="exact repeated string"
1="another exact string\n"
END
```

Each definition line is `ID=VALUE`, where `VALUE` is one JSON string. The fixed dictionary syntax
has no schema header. A request without an existing instruction carrier remains native.

CTP is active for a request only when the router appends that complete dictionary block to the end
of the selected instruction carrier. Without an appended dictionary, every request string remains
native, including text beginning with a CTP tag, and the model emits assistant text natively. This
inactive rule applies when the request has no positive definition or when its stable compact
admission projection is not smaller.

In an active request, a reference-bearing string begins with `!ctp1 R` and a line feed. `@{ID}` expands one definition.
Only this braced lowercase base-36 form is reserved inside a reference-bearing string. Every other
`@`, including email addresses, decorators, and mentions, remains literal. `@@{ID}` represents the
literal text `@{ID}`.

```text
!ctp1 R
Email alice@example.com and reuse @{0}; preserve @@{0} literally.
```

An ordinary string without an exact CTP tag is already native. To represent literal text beginning
with `!ctp1 R`, `!ctp1 L`, or `!ctp1 D` followed by a line feed, the encoder prefixes `!ctp1 L` and
a line feed. The decoder removes only that literal tag. These forms make every valid Responses text
string representable without reserving ordinary `@` characters.

### Input dictionary discovery and admission

The router discovers repeated exact substrings anywhere within eligible strings using the
repository GPT-5 tokenizer. Candidates begin and end at token boundaries and never cross a
Responses string boundary. The router seeds discovery with one token more than the encoded reference cost,
extends matching seeds to maximal exact spans, and trims their edges to readable whitespace or
punctuation boundaries. Repeated occurrences within one string must not overlap.

Candidate scoring charges the measured dictionary line, every reference, the reference tag on each
affected string, and one additional virtual token for the definition and for each reference. The
virtual indirection charge prevents marginal token savings from filling model-visible input with
opaque lookups. Candidates are ordered by descending remaining saving, then stronger readable
boundaries, longer byte length, and bytewise value. At each byte position the encoder prefers the
longest matching selected definition. A definition used fewer than twice after overlap resolution
is removed.

The router serializes a stable admission projection containing the instruction carrier, immutable
pre-model input prefix, and current tool descriptions, including dictionary, reference tags, literal
escapes, and every changed field. It compares that projection with its native form using the same
GPT-5 token estimator. CTP is admitted only when the compact projection is strictly smaller.
Otherwise the request remains byte-equivalent to the native representation and no response decoder
is installed. Once admitted, the frozen definitions encode eligible appended history without letting
that history reverse admission; an individual full request may therefore be larger while preserving
the provider-cache prefix. Metrics still compare the complete native and forwarded requests.

Definitions live for one upstream request. Every turn and compaction request rebuilds its dictionary
after ordinary replay and Hpatch projection from the current tool descriptions and the immutable
input prefix before the first model-authored item. The selected definitions may encode eligible
strings throughout the request, but appended model history cannot change the dictionary or rewrite
an already encoded prefix. The router retains no adaptive dictionary, cross-session codebook,
training state, or provider-cache state. Prompt caching remains provider-owned.

Eligible request strings are non-carrier message text, tool descriptions, historical custom-tool
inputs and outputs, and historical function arguments and outputs. The selected instruction carrier
remains native apart from its appended dictionary. CTP does not rewrite binary or image content,
reasoning items, identifiers, unknown fields, tool names or choices, grammar definitions, JSON
schemas, or executor carriers that are not model-visible.

### Model-defined response extension

Assistant text may extend the inherited dictionary with unused identifiers before referencing the
new values:

```text
!ctp1 D
2="an exact novel response string"
END
!ctp1 R
Reuse inherited @{0} and new @{2}; then reuse @{2} again.
```

The line feed ending the `!ctp1 R` tag is framing rather than decoded content. Every later byte,
including a final line feed, belongs to assistant text. Model guidance MUST preserve requested
leading and trailing whitespace and end a compact body immediately after its final literal byte or
reference when the native output has no final line feed. Definitions for repeated lines MUST exclude
a trailing separator when the final native line has none; separators then appear only between
references.

The response dictionary and reference body occupy one assistant-text value. Definitions extend the
response namespace in item and content order and remain available to later assistant-text values in
that response. A definition is an exact nonrecursive JSON string. Redefinition, an invalid definition,
an unknown reference, or a reference that precedes its definition fails response transformation.
Response definitions do not persist into later requests.

The router strips response dictionary blocks and restores compact assistant text before Hpatch
translates native registered calls. It never interprets CTP syntax in tool names, custom-tool inputs,
or function-call arguments. This keeps CTP out of Hpatch, shell, and other execution paths.

For streaming Responses traffic, compact deltas may remain provider-owned while an assistant text
item is pending. A terminal text event is decoded against the current response dictionary, and its
following completed content-part projection commits new definitions for later text. Completed item
projections are decoded from the dictionary snapshot captured when that item was added. The terminal
`response.completed` object is decoded independently from the inherited request dictionary, in
response order. Repeated event projections therefore cannot redefine entries. Terminal assistant
items and response objects are exact native text.

Decoded strings and serialized JSON or SSE output remain within the transport's existing 64 MiB
upstream JSON buffer budget. Provider error responses pass through without CTP decoding and discard
request-local state.

### Metrics

Router metrics expose auxiliary compression counters for admitted requests. Input counts compare
the complete post-Hpatch native request with the compact upstream request. Output counts compare
provider-emitted compact assistant text, including any model-defined dictionary, with decoded
assistant text once per logical content item. Streaming output is counted from each terminal
`response.output_text.done` event; repeated content-part, item, and completed-response projections
do not contribute again. Tool payloads never contribute output compression counters.

The aggregate and each session also count native and compact UTF-8 bytes, request and response
dictionary definitions and framing bytes, encode and decode operations and nanoseconds, and decode
failures. Each session retains bounded input observations with admission and request sequence and
bounded logical assistant-output observations. Dropped counters expose truncation. Observation
records contain only sizes, counts, decisions, timing, and sequence, never dictionary values or text.

Both sides use the repository GPT-5 token estimator. Ratios are `compact_tokens / native_tokens`,
and saving is `(native_tokens - compact_tokens) / native_tokens`. Missing admitted input or assistant
text produces unavailable ratios rather than invented zero-token savings. These estimates do not
replace provider usage observed before local CTP and Hpatch restoration.

Admission counters distinguish a missing instruction carrier, no positive definition candidate,
and a stable admission projection that was not smaller without retaining prompt text.

### Acceptance

1. Native mode preserves request and response behavior without adding instructions, dictionaries,
   or response state.
2. CTP/1 changes only token-positive eligible request strings and restores exact native text before
   downstream consumers observe provider output.
3. Without a router-appended request dictionary, all CTP-looking request and response text is native.
4. Repeated substrings can be discovered within one string; ordinary `@` remains literal and an
   exact reference lookalike round-trips through its narrow escape.
5. Model-defined exact strings extend the inherited response dictionary in order, while redefinition,
   unknown references, forward references, and malformed definitions fail honestly.
6. Tool names, new tool inputs, function arguments, schemas, reasoning, usage, identifiers, and native
   instruction priority remain unchanged.
7. Non-streaming and streaming assistant text restore the same bytes, streaming metrics count only
   terminal logical text, and repeated event copies do not leak or duplicate response definitions.
8. Stable-projection profitability includes all dictionary, reference, tag, and escape overhead; a
   nonprofitable or definition-free projection remains native.
9. Decoded size checks fail before an oversized string, JSON response, or SSE event reaches downstream
   translation.
10. Passthrough mode rejects `--model-protocol ctp1` and never adds a compacting provider boundary.
11. Appending model-authored items or tool results cannot change the dictionary or admission derived
    from unchanged tool descriptions and an unchanged pre-model input prefix; admitted definitions
    remain available to eligible strings in the appended history.
12. CTP guidance treats decoding as inline representation and does not add an inspection or tool
    step to the task's ordinary workflow.
