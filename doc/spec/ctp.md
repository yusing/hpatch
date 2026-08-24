# Compact token protocol contract

## REQ-CTP-001 — Lossless model-visible data-plane encoding

`hpatch-router --model-protocol ctp1` enables Compact Token Protocol version 1 for Hpatch-mode
Responses traffic. `--model-protocol native` is the default. CTP is a reversible representation
layer between the existing Hpatch request projection and the upstream model provider; it is not a
second Responses protocol or an edit-engine feature.

CTP leaves the Responses control plane native. Request and response objects, item types, roles,
instruction priority, item and call identifiers, status, reasoning configuration and items,
stream framing, usage, conversation selection, and compaction lifecycle retain their provider
contracts. The profile may change only eligible model-visible request strings and assistant text.
Tool names, tool calls, tool grammar definitions, and JSON schemas remain native.

### Representation

CTP/1 has four model-readable primitives:

```text
LIT(bytes)                 an ordinary native string
DEF(scope, id, bytes)      one exact byte string named by a short identifier
REF(scope, id)             an exact reference to a prior definition
TABLE(fields, rows)        a header followed by uniform pipe-delimited rows
```

`contrib/codex/model-instructions.md` owns the durable interpretation and emission rules for these
primitives. The router selects the native Codex instruction carrier: a nonempty string-valued
top-level Responses `instructions` field when present, otherwise the first textual developer
message in `input`. It appends only request-scoped dictionary data to that carrier. The original
instruction bytes remain an unencoded prefix at their native priority. A request without either
carrier uses the native representation. Each definition row has this form, with `VALUE` encoded as one JSON
string:

```text
CTP/1
T|D|id|value
D|0|"exact repeated bytes"
D|1|"another exact value"
END
```

A reference-bearing model-visible string begins with the exact tag `!ctp1 R` and a line feed. Its
remaining bytes are a literal template in which `@ID;` is `REF(request, ID)` and `@@` is one
literal `@`. For example:

```text
!ctp1 R
Use @0; and preserve @@example literally.
```

Definitions never contain references. Identifiers are lowercase, unpadded base-36 values. A
decoder performs one left-to-right pass, replacing each reference with the exact definition bytes
and each escape with `@`; no normalization, reformatting, Unicode folding, or newline conversion is
allowed. An ordinary string without either exact tag is `LIT` and remains byte-equivalent. To
represent a literal whose bytes begin with either reserved tag, the encoder emits `!ctp1 L`, a line
feed, and the complete literal bytes. The decoder removes only that literal tag. The two tags make
every byte string representable without treating a malformed lookalike as compact data.

Tool definitions continue to use the provider's native structured representation. Their eligible
description strings may contain request definitions, but tool names, tool choices, and historical
call names remain native. Historical tool inputs and function arguments remain eligible request
strings. The model emits every new custom-tool input, function-call argument, and tool name in the
native representation, and the CTP response decoder never interprets those fields.

### Admission and scope

The router deterministically discovers definitions from exact repeated physical-line segments in
eligible strings. A segment includes its line terminator when present; a final unterminated segment
is distinct. Candidates shorter than 24 bytes or occurring fewer than twice are discarded. The
remaining candidates are ordered by descending estimated net token saving, then descending byte
length, then bytewise value. The estimate charges the candidate's definition row, every resulting
reference, and one reference tag for each affected string. The router retains positive candidates
in that order, assigns their base-36 identifiers, then admits the complete definition
representation only when its serialized request, including the one shared dictionary block and every
literal escape, is strictly smaller under the repository's GPT-5 token estimator.

If the complete definition representation is not strictly smaller than the native post-Hpatch
request, CTP forwards that request natively and installs no response decoder. This comparison
includes the dictionary block, literal escapes, and every changed field; a local saving cannot hide larger
protocol overhead elsewhere.

Definitions live for one upstream request. Every turn and compaction request rebuilds its table
from the exact model-visible payload after ordinary replay and Hpatch projection. The router
retains no adaptive dictionary, cross-session codebook, training state, or provider cache state.
Prompt caching remains provider-owned.

Eligible request strings are non-carrier message text, tool descriptions, custom-tool inputs and
outputs, and function-call arguments and outputs. The selected instruction carrier remains native
apart from its appended dictionary. CTP does not rewrite binary or image content, reasoning items,
identifiers, arbitrary unknown fields, grammar definitions, JSON schemas, or executor carriers that
are not model-visible. Unsupported shapes remain native rather than being approximated.

### Model output and restoration

Every literal assistant-text output is representable: ordinary strings remain literal, while a
value that begins with a reserved tag uses the literal-tag form. A model may use request definitions
only in assistant text that exactly reuses input bytes by emitting the reference-tag form. Novel
assistant prose remains literal. Tool names, custom-tool inputs, and function-call arguments remain
native even when their bytes match a definition. This keeps CTP out of Hpatch, shell, and other tool
execution paths. Output savings are therefore opportunistic and limited to exact input reuse in
assistant text.

For a non-streaming response, the CTP decoder restores an echoed top-level instruction carrier and
tagged assistant text in the complete output before Hpatch translates native registered calls. It
leaves every custom-tool and function-call item uninterpreted. For a streaming response, it restores
assistant message items, text content parts, terminal-text events, and assistant text inside response objects.
It does not decode custom-tool input or function-call argument events. Delta event boundaries remain
provider-owned and may carry the compact assistant-text representation while its terminal item is
pending; terminal assistant items and response objects are exact. Response usage is observed before
local CTP and Hpatch restoration and remains the authoritative provider token record.

Router metrics expose auxiliary CTP compression counters for admitted representations. Input
counts compare the complete post-Hpatch native request with the compact upstream request. Output
counts compare compact and decoded assistant text once per logical content item. Streaming output
is counted only from the terminal `response.completed` response object; intermediate event copies
are decoded but do not contribute metrics. Both sides use the same GPT-5 token estimator as
admission. Ratios are `compact_tokens / native_tokens`, and token saving is
`(native_tokens - compact_tokens) / native_tokens`. When no request admits CTP, both ratios are
unavailable rather than inferred from baseline usage. These estimates do not replace provider
usage. Admission counters also distinguish a missing instruction carrier, no positive definition
candidate, and a complete representation that was not smaller, without retaining prompt text.

An exact reference or literal tag opts that one assistant-text output string into decoding. An
unknown or malformed assistant-text reference fails response transformation. The same bytes in a
tool name, custom-tool input, or function-call argument remain literal native data. Decoding checks
the expanded length before allocation or downstream emission and rejects a decoded JSON response or
decoded SSE event that would exceed the transport's existing 64 MiB upstream JSON buffer budget.
A provider error response is returned without CTP decoding, and the request-local state is
discarded.

Acceptance:

1. Native mode preserves the current request and response behavior without adding instructions,
   dictionaries, or response state.
2. CTP/1 changes only token-positive model-visible representation and decodes every admitted request
   string exactly under unit round trips, including both reserved tags, malformed-looking literal
   references, `@`, Unicode, CRLF, and absent final newlines.
3. Hpatch projection and replay run before request encoding; response decoding runs before Hpatch
   JSON or SSE restoration. Native Codex carriers, history, and recovery ancestry never contain CTP
   references.
4. Roles, identifiers, status, usage, reasoning, tool schema and grammar structure, response event
   types, and compaction control remain native.
5. Non-streaming and streaming tool calls reach the existing translator without CTP decoding or
   aliasing. An echoed top-level instruction carrier and tagged assistant text are restored, while
   CTP-looking tool names, inputs, and arguments remain literal native values.
6. Malformed compact assistant text fails honestly before downstream display, while an unprofitable
   or unsupported request falls back to the complete native post-Hpatch request.
7. Passthrough mode rejects `--model-protocol ctp1`; it never adds a compacting provider boundary
   without the Hpatch-owned request and response seams.
8. Golden request fixtures prove one profitable repeated-line encoding and one native fallback with
   exact estimated token counts; implementations cannot satisfy admission solely by declining every
   candidate.
9. Router metrics report separate input and assistant-output native and compact token totals for
   admitted CTP representations, without counting tool payloads; streaming output is observed only
   from the terminal completed response.
