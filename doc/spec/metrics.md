# Persistent token, command, target, and failure metrics

## REQ-METRICS-001 — Persistent token, command, target, and failure metrics

Each host API invocation returns evaluator counters in the metrics fields of `HostTranslation`; obtaining that result does not persist them. The host supplies the visible
hpatch payload, comparison carrier, rendered report visibility, session attribution, and other
transport evidence to `RecordHostMetrics`, which is the only root persistence boundary. The router
calls `RecordHostMetrics` after the routed outcome is known. Basic `Apply` and `Translate` neither
return `HostTranslation` nor automatically persist metrics.

A successfully recorded nonempty change set contributes paired estimates for two semantically
equivalent tool calls. A recorded failed invocation contributes only its host-supplied generated
`hpatch` call estimate to the ineffective-output counter; it contributes nothing to the effective
`hpatch` counter. A failed routed invocation is represented downstream by a Code Mode carrier that returns its
diagnostic and repair context. Its comparison baseline is the fixed direct-call program
carrying `*** Begin Patch\n*** End Patch\n`; that tokenized semantic baseline contributes
to the failed translated counter. The diagnostic carrier itself never counts as translated
hpatch output. The complete failed hpatch call remains in the ineffective-output counter and
reduces the overall output savings. Metrics reads and unsupported host calls do not contribute metrics.

Every routed contributed-tool call classified by `REQ-PLUGIN-001` contributes a row keyed by
plugin identity and tool name. Its emitted estimate counts the model-visible tool name followed
by the exact input the model emitted. Its translated estimate counts the validated Code Mode
carrier name followed by the router's canonical serialized stock payload. The stock payload is
the execution carrier unless an exec translator supplies a validated stock command. In that case,
the stock carrier uses the semantic name `functions.exec`, and the router renders the command
through the same canonical exec wrapper, template, and parameters.
Provider-generated item IDs, call IDs, status, and JSON or SSE envelopes are excluded from both
shapes. Plugins supply content evidence but not token counts or outer carrier serialization. A
translated row's reduction is `(translated - emitted) / translated * 100`, may be negative, and
is `n/a` when translated tokens are zero. Router-side input rejection uses a separate failed row
with `n/a` reduction. Executor failures after Codex accepts the execution carrier do not
retroactively become router translation failures.

For hpatch, emitted estimates count the model-visible tool name followed by the exact payload.
A correlated routed recovery chain settles once: its hpatch side is the encoded initial
`functions.hpatch` call plus every encoded `functions.hpatch_recover` call, while its comparator
side is exactly one final `functions.exec` carrier containing the generated `apply_patch` program.
Rejected recovery attempts add their emitted payload but no additional comparator. A successful
recovery atomically compensates every provisional failed row and records one combined `hpatch`
output row against the final comparator; it never records a separate `hpatch_recover` output row.
An abandoned chain retains the combined ineffective tokens and the initial failed comparator.
Per-attempt router telemetry remains individual, and the `hpatch_recover` definition remains
ordinary definition input overhead.
All estimates use the tokenizer library's GPT-5 model mapping. Tool inputs and translated
payloads remain data and cannot alter the fixed programs used for counting.

A final-state report contributes its exact rendered text to the state-report input-token counter
only when the host supplies evidence that the report became model-visible. This is model-input
overhead because the tool result becomes subsequent model context; it is not added to either
model-output counter.

The host tool definitions are also model input. The router obtains the session identity, the
exact serialized collection of installed built-in and plugin tool objects, its stable per-plugin
and per-tool definition breakdown, the displaced native patch definition, and the displaced
request-specific `exec_command` fragments directly from the routed request. The first classified
request of a session counts these inputs once. Each removed fragment is tokenized independently,
without synthetic separator text. Subsequent requests in the same session add nothing because
the resent definition is served from the provider's prompt cache. The two removed-definition
counters remain separate. The installed-definition total is authoritative; per-tool rows and a
shared framing row reconcile it without being added again when computing net input. A host that
supplies no session or definition leaves these counters at zero, and the structured metrics presentation states which inputs were measured so a zero is not read as a free tool.

A failed or cancelled invocation emits no report and contributes zero report-input tokens. A partial or failed routed report emission does not count as a complete emitted report. For each completed contributed
tool execution, the current input estimate tokenizes the current stdout followed by current stderr.
The stock estimate tokenizes the optional stock stdout followed by stock stderr. The current result
is also the stock result when the executor omits the optional result. Exit status, provider-hidden
protocol and reasoning tokens, assistant commentary, and server-generated identifiers are excluded.

A contributed tool's input reduction is `(stock - current) / stock * 100`, may be negative, and is
`n/a` when stock tokens are zero. Its signed input overhead is `current - stock`. The sum of these
signed tool-result overheads contributes to net added input but does not add plugin rows to the
input-overhead source table. The router's end-to-end Responses and per-session usage totals remain
authoritative for provider-consumed model input. These token counts are reproducible estimates rather
than provider billing totals.

Each terminal Responses lifecycle log attributes the observed provider input, cached input,
uncached input, output, and reasoning counts to one logical router request. It never logs the
cache key, authorization material, prompt body, instructions, tool definitions, or input history.
Requests without terminal usage omit token-count fields rather than reporting zero usage.

The router's in-memory metrics snapshot also attributes successful and rejected hpatch
translations and rejected-call diagnostic input tokens to the request session. Each session
retains the latest 32 evaluator rejection identities: command index, physical source line,
operation, target kind when known, stable reason, affected path when known, the physical
multiline value row when localized, and the generated line and column reported by language
syntax validation when applicable. For a rejected line or range target that the router could
parse before evaluation, the identity also carries `none`, `exact`, `contains`, `contained`, or
`overlap` for its inclusive row-coordinate span relative to confirmed same-path prior replacement
targets. The relation is derived without retaining row hashes or target content. A command with
several distinct repair locations retains one
identity per location as defined by `REQ-OUTPUT-001`. Each session also retains the latest
128 routed attempt identities: chain/call identity, attempt, recovery marker, and outcome,
emitted and comparison token counts, evaluated command count, and its bounded rejection
identities. These count limits are reinforced by per-session text-byte limits, so an oversized
rejection identity is not retained. Session records use the same session identity as request
lifecycle metrics and are not written to `metrics.bin`. Each record also carries the client's
own display title for that session when the client exposes one, resolved once per session and
treated as an optional label rather than a counter. They retain neither scripts, replacement
text, diagnostics, nor repair context. Proxy failures that occur before evaluator invocation do
not fabricate evaluator rejection identities.
The snapshot also exposes aggregate counters so a benchmark can reconcile routed calls with
client-visible file-change items without inferring failures from stderr envelopes.

Each session retains the latest 128 completed Responses request observations. An observation carries
only its router request sequence, terminal lifecycle outcome, total and upstream duration, whether
provider usage was observed, and the provider input, uncached-input, output, and reasoning-token
counts. A separate dropped-observation counter exposes retention truncation. Request bodies,
response bodies, credentials, and provider identifiers are never retained by this telemetry.

With CTP/2 enabled, aggregate and per-session metrics record considered, active, and missing-carrier
requests and sum native and compact tokens and UTF-8 bytes for active requests and decoded assistant
text. They also sum encoded strings, visible-line references, content-local dictionary definitions
and framing bytes, encode and decode operations and nanoseconds, and response-decode failures. Each
session retains the latest 128 input observations and 128 assistant-output observations with their
request sequence, representation sizes, framing counts, activation decision, and encode timing.
Independent dropped counters expose truncation. These observations retain sizes and decisions only,
never dictionary values, locators, or text. Streaming decode timing counts each transformed upstream
event, while assistant-output observations still count each logical terminal text exactly once under
`REQ-CTP-001`.

Validated compaction requests bypass CTP/2 and therefore add no considered-request observation.

`RecordHostMetrics` persists classification only after the host supplies the terminal outcome and
visible carrier evidence. For router translation it records a paired effective estimate after the
complete patch is available; for host application it records one only after the staged changes
commit. The router records report-input tokens only after the complete final-state report is
emitted. Parse, evaluation, translation, carrier, and commit failures record only the supplied
hpatch estimate as ineffective. Successful no-op host results contribute evaluator command counts
and, when model-visible, a report estimate without paired effective token estimates. Failure to
render an equivalent patch after a successful host commit records no paired token classification,
but retains the supplied evaluator counters and completed visible-report metrics.

Every supported command reached by evaluation contributes one invocation. A supported
operation rejected by syntax parsing contributes one invocation and one error when its
operation and attempted variant are structurally recognizable. An operation whose path
resolution or execution fails contributes one error after its invocation. Unknown or
future operations and failures outside command processing are not attributed to a
supported command. Successfully evaluated commands retain their invocation counts when a
later output or filesystem-commit boundary fails. Supported command counters are:

```text
in  new  mv  rm  type  add
```

Every structurally recognized explicit target attempt increments one target counter:

```text
line  range  text-single  text-multiple
```

Targetless `type VALUE` initialization has no target counter. Anchored and unanchored text
targets use the same counters. A text target with omitted count or count one is
`text-single`; an explicit count intended to exceed one is
`text-multiple`, including an invalid multiple count. Unknown commands are syntax failures
but do not receive supported-command or target attribution.

Terminal command errors carry stable internal reason identifiers grouped as:

```text
script-syntax
row-missing
row-stale
occurrence-missing
invalid-count
target-order
edit-conflict
active-file
initialization
file-path
language-syntax
other
```

The aggregate is stored in `hpatch/metrics.bin` beneath the platform user configuration
directory returned by Go's `os.UserConfigDir`. Updates hold an exclusive interprocess lock at
`hpatch/metrics.lock`; structured metrics reads hold a shared lock. The current-version metrics format uses
two alternating bounded slots holding global hpatch counters and a keyed collection of
plugin-and-tool definition, call, emitted, translated, failed-translation, current-result, and
stock-result counters, plus a persistence generation and checksum. A reader uses the valid
greatest persistence generation, so an interrupted write to the inactive slot leaves the
preceding aggregate available. The file does not grow after its current-version slots are
created. Per-counter, per-tool, collection, and aggregate overflow fails without changing the
tool result.

Only the latest metrics magic is decoded. A complete, checksummed slot whose eight-byte magic
starts with `HPATCH` but does not equal the current version resets the reported totals to zero.
A malformed slot, including a mismatched version with an invalid checksum, does not qualify
for reset. When a current-format slot is also valid, its totals take precedence over
mismatched-version slots. Other invalid data fails rather than producing a misleading report.
Metrics writes use normal operating-system page-cache writeback and do not request a
per-invocation filesystem sync; sudden power loss may lose increments that the operating
system had not yet flushed.

The router dashboard and `GET /api/metrics` expose the persisted aggregate as structured output.
They include one stable output-token row per plugin and tool with activity, optional adjacent
failed-translation rows, an all-tools row, installed-definition reconciliation, and the hpatch
failed semantic baseline. A separate recovery table has stable `white-space error`,
`indentation shift`, and `luna misuse` rows.

The same surfaces expose current-versus-stock input-token rows, final-state report and failure
diagnostic overhead, displaced native-definition credits, installed-definition totals, net added
input, command invocation and error rates, target counters, terminal reasons, and nonzero
command-and-reason pairs. Percentages are rounded to one decimal place and are zero when their
denominator is zero. With no metrics file or only an obsolete record, all totals and percentages
are zero. A metrics read does not create or rewrite the file. Tokenization, locking, persistence,
or presentation failure remains auxiliary and cannot change the requested effect.

Acceptance:

1. Host variants expose evaluator counters in `HostTranslation` without persistence. Repeated
   router calls to `RecordHostMetrics` persist cumulative paired hpatch estimates and completed
   visible-report input estimates; failed recorded invocations persist only ineffective hpatch
   estimates and zero report-input tokens.
2. Every successfully translated contributed-tool call persists a plugin-and-tool output row whose
   emitted count uses the exact model-visible call shape. Its translated count uses the validated
   stock carrier when supplied and otherwise the validated execution carrier. A stock carrier does
   not change execution, history, replay, or runtime-failure classification.
3. Every completed executor result persists current and stock input estimates for its plugin and
   tool. An omitted stock result produces equal estimates and zero reduction without a second
   execution. A zero-token stock result reports `n/a`.
4. Structured router metrics report stable per-plugin and per-tool output rows, optional adjacent failed rows, and one
   all-tools output row. They report a separate input table with current, stock, reduction, and one
   all-tools row.
5. The input-overhead table has no plugin child rows. Net added input includes the signed difference
   between current and stock tool-result estimates.
6. The six supported hpatch command counters and four target counters reconcile with
   aggregate command attempts and errors.
7. Every definition-bearing request increments the definition-request counter, while the exact
   installed tool collection, its reconciling per-tool breakdown, and the displaced baseline
   definition accumulate only once per distinct session. An absent session or definition
   leaves definition counters zero and reports which inputs were measured.
8. Failed hpatch invocations contribute their complete output to the ineffective counter; the
   failed translated counter receives the fixed direct-call program carrying the empty patch
   envelope, while the downstream diagnostic carrier is excluded.
9. A recovery is charged as the shorter payload the model emitted for both effective and
   ineffective invocations while evaluation uses the rebuilt complete script.
10. Tool inputs and translated payloads containing quotes or program-like text remain data and
   cannot alter the canonical programs used for counting.
11. Concurrent writers lose no records, concurrent structured metrics reads never observe a partial
   aggregate, and an interrupted or damaged latest state falls back to the preceding valid
   aggregate.
12. A valid mismatched `HPATCH` version resets totals when no current state exists; malformed
    data does not count as a version mismatch, and current state takes precedence.
13. `RecordHostMetrics` failure warns without changing the success or failure of the requested
    edit, translated carrier, executor result, or final-state report; omitting the call leaves
    persistence unchanged.
14. Router snapshots attribute successful and rejected hpatch translations, diagnostic token
    totals, at most the latest 128 recovery-aware attempt identities, and at most the latest
    32 structured evaluator rejection identities, including parseable same-path row-span relation
    to confirmed prior replacement targets, to their request sessions without persisting scripts,
    row hashes, replacement text, diagnostics, repair context, or new per-session records in
    `metrics.bin`; per-session text-byte limits may retain fewer identities.
