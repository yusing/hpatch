# Output, final state, and failure behavior

## REQ-OUTPUT-001 — Output, final state, and failure behavior

Every root entry point accepts one complete input and evaluates the entire script before an
external filesystem commit or translated patch is returned. Basic `Apply` returns only an error;
basic `Translate` returns only patch bytes and an error. `ApplyForHost`, `ApplyForHostRoot`,
`TranslateForHost`, and `TranslateForHostAt` return `HostTranslation`, which carries the rendered
report, final state, diagnostics, patch summary, target aliases, and evaluator metrics. Before
finalization, every changed file whose final path ends in `.go`
is parsed and formatted with Go's standard-library `go/format`; parse failures are collected
from every changed Go file before the complete transaction rejects. For at most 32
content-mutating commands in one invalid Go file, the evaluator replays command-group subsets
against the immutable baseline to select a one-minimal syntax-failing set, then attributes
each useful parser failure to the retained edit nearest its generated parser position. Larger
groups or an invalid baseline use nearest-edit attribution without subset replay. Supported
changed `.py`, `.js`, and `.ts` files are syntax-checked when Tree-sitter language support is
available and contribute all discovered failures to the same validation result. Parser
cascades are collapsed when blanking an earlier repair line removes a later parser failure.
Failures are deduplicated by originating command and physical heredoc value row, or by the
command's script row when no physical value row exists. Each retained location includes at
most two generated lines before and after the failing line; neighboring lines are capped at
64 runes and the failing line at 200. Supported baseline-aware indentation corrections are
applied before validation; unsupported extensions remain byte-exact or reject under
indentation policy.
An unchanged apply change set performs no filesystem operation and succeeds. An unchanged basic
translation returns an empty patch. A host variant additionally reports the already-satisfied
final state in `HostTranslation`.

Translation output contains file actions in deterministic first-touch order:

```text
*** Begin Patch
*** Update File: PATH
*** Move to: NEW_PATH
<unified diff hunks>
*** Delete File: PATH
*** Add File: PATH
+<content>
*** End Patch
```

Each action includes only syntax relevant to that file: additions use `Add File`,
deletions use `Delete File`, moves use `Update File` plus `Move to`, and content edits
use `Update File` hunks. A moved and edited file combines its content hunks and move in
one update action, with `Move to` immediately after `Update File`. Because OpenAI
`apply_patch` rejects an empty update action, a move with unchanged contents includes
a minimal verification hunk: one unchanged context line for a nonempty file, or an
equal remove/add of the empty line representation for an empty file. Translation is fully rendered before it is returned.

After evaluation succeeds, host variants carry one fully rendered final-state report in
`HostTranslation`. Apply host variants return it only after commit succeeds; translation host
variants return it with the complete patch; routed `functions.hpatch` emits it through the
restored carrier. Basic `Apply` and `Translate` do not return the report. Its line forms are:

```text
in PATH
last OP PATH COUNT ranges RANGE[, RANGE[, RANGE]] [ +N more]
files add=A update=U move=M delete=D
refs COMMAND OP PATH
LINE:HASH TEXT
```

The first line is `no active file` when `rm` leaves none. Otherwise it names the active
final path. The `last` line is `last none` when no mutation changed final content;
otherwise it names the last effective mutation operation, that file's surviving final
path, the number of affected target spans, and at most three verified immutable-baseline
ranges. Extra ranges are summarized by `+N more`. `RANGE` is a half-open
`START_LINE:START_COLUMN-END_LINE:END_COLUMN` pair in one-based Unicode coordinates; a
complete-line range includes its final terminator when present. The `files` line counts
net original-to-final actions.

One `refs` block follows for every effective content-mutating command on every surviving
edited file. `COMMAND` is the command's positive one-based nonblank script index, `OP` is
its authored mutation operation, and `PATH` is the file's final path after pending moves.
Blocks retain authored command order. Each block contains at most four distinct current
rows, ordered by final line number: the rows containing the first and last endpoints of
the command's aggregate rendered edit extent, the immediately preceding surviving row,
and the immediately following surviving row. Missing neighbors are omitted. Coincident
endpoint or context rows are emitted once within that block. A row may appear in separate
blocks when it identifies the context of separate source commands.

The projector derives each aggregate extent from that command's effective editor splices
in rendered final content, then maps both endpoints through language-formatting offsets.
A collapsed deletion endpoint maps to its surviving containing row; its available
neighboring rows provide boundary anchors. Logical-line clamping does not invent a
trailing empty row for a final terminator. An empty surviving file reports row `1` with
the hash of empty content. When the active final file has no `refs` block, the report
retains the existing fallback of up to three rows from the start of that file without a
`refs` header, even when other surviving files have reference blocks.

Every row has `REQ-READ-001` identity over the complete current final logical line.
`TEXT` contains at most the first 64 Unicode code points of line content, without a line
terminator or added ellipsis. Leading spaces are escaped as `\x20`, leading tabs as `\t`, and
all controls use their Go quoted form so indentation is visible and each row stays on one
report line. The hash still covers the complete untruncated content.
The projection is bounded by four rows per effective command, plus the three-row fallback;
it does not retain another original or final content copy, routed-read history, a word
diff, or translated patch text.

A successful report's `LINE:HASH` rows are current references for their named final paths
and may be used directly in the next invocation. An earlier row whose content is unchanged may
also be reused: its line is a hint and its hash relocates only when unique. The projection does
not guarantee every possible later target; when the exact target needed next is absent or
ambiguous, the caller obtains it with a focused hread. A row or range endpoint is never guessed
or reconstructed. An in-process successful host result also carries one structured target alias
for every effective nonempty `type` command whose authored target is a row or inclusive row range.
The alias maps that exact target and final path to the final rendered replacement extent after
language formatting. Deletions, insertions, text-occurrence targets, targetless initialization,
and ineffective commands produce no alias. Root APIs retain no target or editing state between invocations.

In routed mode, the router retains those aliases within the same session and workspace only after
a replayed carrier output exactly confirms the successful report. Before translating a later
script, it follows the aliases in retained call order. A failed, missing, or altered carrier
output confirms nothing. For rejected parseable line and inclusive-range commands, the same
rewrite boundary classifies only the emitted row-coordinate span relative to confirmed same-path
alias targets as `none`, `exact`, `contains`, `contained`, or `overlap`; it does not change target
rewriting or evaluation.

For host variants, the complete report is rendered before commit or patch return. Apply host
variants return it only after the external effect succeeds; router emission is auxiliary and
cannot retroactively change or roll back a successful effect. Basic `Apply` and `Translate`
discard the host-only report and structured state at their public boundary.

Root application stages new contents in same-directory temporary files before starting the commit. Parse, validation, read, and evaluation failures leave the initial tree unchanged.
A staging failure attempts to remove all temporary artifacts; cleanup failure returns
nonzero and identifies every artifact it could not remove. Commit-time filesystem failures
trigger rollback attempts using staged backups. Ordinary filesystems cannot provide a
portable crash-atomic transaction over multiple paths: termination, machine failure, or
rollback failure during commit can leave a partial change set. Such a failure must return
nonzero and name the affected paths; it must never report success or claim rollback
succeeded when it did not. Existing file permission bits are preserved; files created by
`new` use mode `0644`.

OpenAI `apply_patch` is a logical-line format and cannot preserve CRLF or standalone-CR
bytes when its output is applied by the tool. Translation therefore returns LF-only patch text and normalizes line endings only in its displayed before/after lines. It does not modify source files. Root application continues to preserve existing line endings outside explicitly inserted strings. Applying translated output to a non-LF file may normalize
that file to LF; this is a declared format limitation, not byte equivalence.

Basic `Apply` and `Translate` return errors for failures. Host variants place generic diagnostics
and structured failure data in `HostTranslation`; rendered generic diagnostics use the `hpatch:`
prefix. Command failures have the stable rendered form:

```text
OP: command N[, path "PATH"], reason REASON: MESSAGE
```

The visible command line omits source line, a repeated operation field, and category.
Structured host rejection data retain command index, source line, operation, path, generated
position, and localized value row when applicable; hook data also retain category. Validation
orders failures by command index and then localized value row. It emits one visible command
line per originating command and path. A command with several distinct repair locations uses
the message `N distinct syntax failures`, followed by bounded repair context for every
location; structured host data contain one rejection entry per location. Duplicate parser
messages that resolve to the same command and physical value row, or to the same inline script
row, remain one visible location. Independently parseable syntax failures may be reported
together before evaluation. A heredoc failure is owned by its header and may additionally
report its attributable source span. Control bytes are escaped and embedded newlines are
folded so one command failure remains one logical line.
Failures return no completed patch. Basic entry points return an error; host variants return
`HostTranslation` diagnostics without a successful final-state report. Malformed row syntax
receives a syntax diagnostic.

A stale row reports the actual current-line candidate and up to two neighboring baseline rows.
It also reports every baseline line whose hash makes the stale reference ambiguous, or states
that the hash is absent. A unique relocated hash resolves during evaluation and does not produce
a diagnostic. Range repair reports start and end independently. When both requested coordinates
are in bounds and ordered, it also renders one explicitly unverified current-coordinate range
candidate in exact target syntax with its inclusive span length; normal endpoint verification
remains authoritative. A missing literal occurrence
reports the verified anchor context. An edit conflict identifies the prior command and affected
immutable-baseline lines. If
a command depends on content introduced by another command, the diagnostic directs the agent to
apply the prerequisite independently, reread, and submit a later invocation. A missing row or
failure without a verified baseline does not choose repair context. Repair context is
supplementary: it never changes the host outcome, mutation, returned patch, or metrics classification.
When invalid generated source is localized to a fixed-heredoc mutation, each distinct rejection
identity includes the non-sensitive `value_line`. Transient root diagnostics describe every
bounded value-row context rather than mutation addresses. Routed target-only recovery diagnostics
add current hashed `C...` handles only when every rejection is `row-stale`; other failures expose
no recovery handle under `REQ-CORRECT-001`.

The public host result separates lifecycle `Outcome`, requested `Change`, routed `Attempt`,
actionable `Failures`, durable-safe `Rejections`, and `PatchSummary`. A valid no-op returns
`evaluated/already-satisfied`, sets `Change.AlreadySatisfied`, and has an empty patch. Failure
scope is `field-local`, `multi-command`, `new-script`, or `new-transaction`; suggestions contain
bounded existing repair context rather than inventing new validation rules.

Acceptance:

1. Basic `Apply` returns only an error after commit and basic `Translate` returns only the complete
   patch bytes or an error without mutation. Their host variants return `HostTranslation`, including
   the rendered final- or pending-state report. An already-satisfied translation succeeds with an
   empty patch; the host variant also returns the rendered already-satisfied state.
2. Active paths, bounded last-mutation ranges, per-command final-reference blocks, net file
   counts, Unicode columns, truncation, control escaping, moved files, deletions, and empty
   files produce the specified report without implying cross-invocation persistence.
3. One invocation editing multiple regions and files reports current final paths and rows
   for every effective content command in authored order. A later invocation can target an
   exact reported row without hread, while an unreported target requires a focused read and
   a saved pre-edit row still rejects as stale.
4. Changed Go files are formatted with the standard library before output, and invalid Go
   rejects the transaction without mutation; supported changed Python, JavaScript, and TypeScript files are syntax-checked and receive supported automatic indentation correction.
5. Malformed input, missing, stale, reversed, or incomplete targets, edit conflicts,
   unknown or future commands, invalid UTF-8, missing or non-regular files, path collisions,
   staging failure, translation failure, and cancellation produce no mutation, returned patch, or final-state report.
6. Injected external filesystem commit and rollback failures are reported without false
   atomicity claims and without a successful final-state report.
7. Failure to emit a fully rendered routed report after a successful external effect does not reverse that effect or record a complete report-input token estimate.
8. Stale rows, incomplete literal targets, and edit conflicts emit verified repair context;
   a missing row fails without guessing, and a failure with no active baseline emits its
   diagnostic alone.
9. Invalid Go localized inside a fixed `<<PATCH` value reports its physical body row in
   bounded repair context and structured host rejection identity without retaining body text.
10. One syntax-validation rejection includes every distinct actionable repair location from
    all changed files, groups visible diagnostics once per originating command and path,
    deduplicates parser cascades by repair row, and exposes enough current rejected-script rows
    for one atomic recovery payload to repair all locations.
