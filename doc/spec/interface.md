# Interface contract

## REQ-CLI-001 — Modes and informational output

`hpatch [--root ROOT] [--cwd CWD]` reads a complete script from standard input,
evaluates its complete change set in memory, stages required filesystem content, and
then commits it. After a successful commit it writes the final active-file state report
defined by `REQ-OUTPUT-001` to stderr. Stdout remains empty.

`hpatch translate [--root ROOT] [--cwd CWD]` performs the same parsing, filesystem
reads, and in-memory evaluation but never modifies a file. It writes one OpenAI
`apply_patch` envelope that represents the same net change set to stdout, then writes
the pending final active-file state report to stderr.

For normal and translate modes, omitted `--root` means the process current directory
and omitted `--cwd` means `.`. An explicit root must be absolute and is canonicalized
before it is opened. A relative cwd resolves beneath root. An absolute cwd is accepted
only when its canonical location is beneath root. Cwd must identify an existing
directory. The CLI opens root once and uses that pinned capability for the invocation.

`hpatch gain` reads no script and reports the persistent aggregate defined by
`REQ-METRICS-001`. `hpatch --help` is the complete built-in agent reference for
stdin usage, process and editing commands, editor state, orchestration, trust boundaries,
and validation. `hpatch --tool-help` derives a custom-tool-focused reference from the
same agent workflow, editing-command, baseline-state, selector, and final-state-report
sections. It omits CLI usage and mode descriptions, root and cwd options, metrics,
and version material, then adds workspace-relative path and parent-directory preparation
guidance.
`hpatch translate --help` summarizes translate-mode I/O and points to top-level help.
`hpatch --version` writes the module build version, or `devel` for an unversioned build.
Informational commands do not read stdin, resolve a working or configuration directory,
access metrics, or inspect project files. Any other argument list is invalid.

Acceptance:

1. Given valid edits spanning multiple files, normal mode produces the specified final
   paths and contents, empty stdout, and one final-state report on stderr.
2. For LF inputs, applying translate mode's patch-only stdout produces the same final
   paths and UTF-8 contents as normal mode. For other line endings, it represents the
   same logical-line edits subject to the normalization rule in `REQ-OUTPUT-001`.
3. Translate mode leaves the source tree unchanged and reports the pending result rather
   than the unchanged source tree.
4. Gain mode leaves the source tree and metrics unchanged.
5. Each supported informational form writes its complete result to stdout with status
   zero and empty stderr without reading stdin or requiring a valid current directory.
6. Tool help contains the current agent workflow, editing language, state rules, final
   report, and tool-path guidance while excluding CLI-only material.
7. Unsupported aliases, trailing arguments, and unknown or future options fail with no
   stdout or final-state report.
8. A nested cwd changes relative path resolution while normal mutations and translated
   patch paths retain the same root-relative file identity.
9. A relative, absolute, or symlink path that escapes root fails without mutation,
   patch output, or final-state report.

## REQ-METRICS-001 — Persistent token, command, and feature metrics

Every recognized normal or translate invocation is classified after its terminal outcome.
A successful nonempty change set that parses, evaluates, translates, and completes its
requested output or mutation contributes paired estimates for two semantically equivalent
tool calls. A failed invocation contributes only its generated `hpatch` call estimate to
the ineffective-output counter; it contributes nothing to the effective `hpatch` counter.
A failed invocation is represented downstream by the router's empty `apply_patch` carrier,
`*** Begin Patch\n*** End Patch\n`. Its tokenized carrier contributes to the failed
`apply_patch` output counter. The complete failed hpatch call remains in the
ineffective-output counter and reduces the overall output savings. `gain`, informational
commands, and unsupported argument forms do not contribute metrics.

Both effective and ineffective hpatch estimates count the `functions.hpatch` tool name
followed by the charged editing script. The charged script is the script on standard input
unless the host sets `HPATCH_CHARGED_SCRIPT` to a nonempty value, in which case that value
is charged instead. A host sets it when the evaluated script was rebuilt from a shorter
payload the model actually emitted, such as a correction naming individual commands of a
rejected script; charging the rebuilt script would measure the repair as costing the
complete retry it replaced. The variable never affects evaluation, which always reads the
complete script from standard input, and it never affects the apply_patch baseline, which
reflects the edit actually applied. The successful direct side counts the
`functions.exec` tool name and a free-form program that passes the complete translated
patch envelope, serialized as one string argument, to `tools.apply_patch`. All estimates
use the tokenizer library's GPT-5 model mapping. Script and patch text remain data and
cannot alter the fixed direct-call program.

A final-state report successfully emitted by normal or translate mode contributes its
exact rendered text to a separate estimated state-report input-token counter. This is
model-input overhead because the tool result becomes subsequent model context; it is not
added to either model-output counter.

The host tool definition is also model input. When the host names a session in
`HPATCH_SESSION_ID` and supplies definition text in `HPATCH_TOOL_DEFINITION`, the first
classified invocation of that session counts that text once into a definition input-token
counter, and subsequent invocations of the same session add nothing, because a definition
resent on every request is served from the provider's prompt cache. `HPATCH_BASELINE_TOOL_DEFINITION`
supplies the native patch tool definition hpatch displaces; it is counted the same way and
only the nonnegative difference is attributable to hpatch. A host that names no session or
supplies no definition text leaves these counters at zero, and gain states which inputs
were measured so a zero is not read as a free tool. A failed or cancelled invocation emits no report
and contributes zero report-input tokens. A partial or failed report write does not count
as a complete emitted report. Other tool results, provider-hidden protocol and reasoning
tokens, assistant commentary, and server-generated identifiers remain excluded. These
are reproducible estimates rather than authoritative API usage.

Classification is persisted only after the invocation's outcome is known. Translate mode
records a paired effective estimate after its complete patch reaches stdout; normal mode
records one after the staged changes commit. Each records report-input tokens only after
the complete final-state report is emitted. Stdin-read, parse, evaluation, translation,
stdout-write, and commit failures record only the canonical hpatch estimate as ineffective.
Successful no-op scripts contribute command counts and an emitted report estimate without
paired effective token estimates. In normal mode, failure to render the equivalent patch
after a successful commit emits a warning and records neither paired token classification,
but retains command and fully emitted report metrics.

Every supported command reached by evaluation contributes one invocation. A supported
operation rejected by syntax parsing contributes one invocation and one error when its
operation and attempted variant are structurally recognizable. An operation whose path
resolution or execution fails contributes one error after its invocation. Unknown or
future operations and failures outside command processing are not attributed to a
supported command. Successfully evaluated commands retain their invocation counts when a
later output or commit boundary fails.

`tsel` attempts are additionally classified as single occurrence when `COUNT` is omitted
or one, and multiple occurrence when the operand is present and intended to select more
than one occurrence; an invalid count remains attributable to the multiple attempt.

`bsel` and `bsel_next` have independent command counters. Each successful block selection
is additionally classified as exact or whitespace-recovered; whitespace-recovered means
at least one anchor had zero exact matches and the horizontal-whitespace fallback made the
command succeed. Terminal command errors carry stable internal reason identifiers so gain
can distinguish malformed or out-of-bounds coordinates, missing or ambiguous occurrences
and anchors, invalid occurrence counts, order or overlap, edit conflicts, missing active
files, and other supported failure families without making user-facing diagnostic wording
part of the persisted format.

The aggregate is stored in `hpatch/metrics.bin` beneath the platform user configuration
directory returned by Go's `os.UserConfigDir`. Updates hold an exclusive interprocess
lock at `hpatch/metrics.lock`; gain reads hold a shared lock. The metrics file contains
two alternating current-version fixed-size slots holding token, command, selector,
recovery, and error-reason counters plus a generation and checksum. A reader uses the
valid current-format slot with the greatest generation, so an interrupted write to the
inactive slot leaves the preceding aggregate available. The file does not grow after its
current-version slots are created. Per-counter and aggregate overflow fails.

Only the latest metrics magic is decoded. A complete, checksummed slot whose eight-byte
magic starts with `HPATCH` but does not equal the current version resets the reported
totals to zero. A malformed slot, including a mismatched version with an invalid checksum,
does not qualify for reset. When a current-format slot is also valid, its totals take
precedence over mismatched-version slots. Other invalid data fails rather than producing
a misleading report. Metrics writes use normal operating-system page-cache writeback and
do not request a per-invocation filesystem sync; sudden power loss may lose increments
that the operating system had not yet flushed.

`hpatch gain` first writes an output-token table comparing successful calls, failed
calls, and all calls. The successful row compares effective hpatch output with the
generated `apply_patch` output and reports its reduction. The failed row compares
ineffective hpatch output with the tokenized empty `apply_patch` carrier emitted by the
router. The all-calls row reports both totals and the overall output-token reduction.

Effective-only reduction compares effective hpatch output against generated `apply_patch`
output. Overall reduction is `(effective_apply_patch + failed_apply_patch - effective_hpatch
- ineffective_hpatch) / (effective_apply_patch + failed_apply_patch) * 100`. It is zero
when the `apply_patch` denominator is zero. No retry payload is inferred.

Gain then writes a separate input-token table for final-state reports, failure diagnostics,
carried host metadata, and tool definitions. Hpatch and `apply_patch` values remain separate:
the report does not subtract definitions, convert input to output, or calculate a combined
input/output percentage. Unmeasured `apply_patch` input sources are labeled `not measured`.

Gain then writes stable-order compact tables for aggregate command invocation and error
rates; `sel`, `tsel`, and `rsel` selector counters; single and multiple `tsel` spans;
error reasons; and each error attributed to the command that raised it. The last table
lists only nonzero command-and-reason pairs, and renders a single `none` row when no
errors are recorded. Every error appears in both the aggregate reason table and the
attributed table, so the two reconcile. Percentages are rounded to one decimal place and are zero when their
denominator is zero. With no metrics file or only an obsolete record, all totals and
percentages are zero. Gain reads no stdin and does not create or rewrite a metrics file.
Failure to tokenize, lock, read, write, or close metrics emits a concise
`hpatch: warning:` diagnostic but does not change the success or failure of the requested
effect.

Acceptance:

1. Repeated successful normal and translate invocations persist cumulative paired
   estimates and fully emitted report-input estimates; failed invocations persist only
   ineffective hpatch estimates and zero report-input tokens.
2. Gain reports output and input token classes in separate tables, calculates reductions
   only between output-token quantities, and performs no input/output price conversion.
3. Each selector metric reconciles with its aggregate command counter.
4. Single and multiple `tsel` attempts, `bsel` and `bsel_next`, exact and
   whitespace-recovered successes, and stable terminal reasons remain independently
   attributable. Per-command reason counts reconcile with both aggregate command errors
   and aggregate reason totals.
5. Each measured invocation accumulates its hpatch and baseline definition input tokens;
   `HPATCH_SESSION_ID` counts distinct sessions without changing that accumulation. An
   absent session or definition leaves definition counters zero and reports which inputs
   were measured.
6. Failed hpatch invocations contribute their complete output to the ineffective counter;
   the failed `apply_patch` counter receives the exact empty carrier emitted by the router.
7. A nonempty `HPATCH_CHARGED_SCRIPT` is charged as model output for both effective and
   ineffective invocations while evaluation still uses standard input; an absent or empty
   value charges the evaluated script.
8. Scripts and patches containing quotes or program-like text remain data and cannot
   alter the direct-call program used for counting.
9. Concurrent writers lose no records, concurrent gain reads never observe a partial
   record, and a damaged inactive slot falls back to the preceding valid aggregate.
10. A valid mismatched `HPATCH` version resets totals when no current slot exists;
    malformed data does not count as a version mismatch, and a current slot takes
    precedence over mismatched versions.
11. Metrics collection failure warns without changing the success or failure of the
    requested edit, translated output, or final-state report.

## REQ-SCRIPT-001 — Script grammar

Blank lines are ignored. All other input is one command per line. Commands are:

```text
in PATH
new PATH
mv PATH
rm
sel LINE START:END
tsel LINE OCCURRENCE "JSON STRING" [COUNT]
bsel "START" "END"
bsel_next "START" "END"
rsel START:END
type "JSON STRING"
del
dup
```

Line numbers and inclusive endpoints are one-based base-ten integers matching
`[1-9][0-9]*`. Signed, zero, incomplete, and nonnumeric line operands are invalid.
Cursor `0:0` denotes the position before the first code point of a file.

`OCCURRENCE` is nonzero: positive values count exact, non-overlapping literal matches
from the start; negative values count them from the end. Optional `COUNT` defaults to one
and must be a positive base-ten integer. JSON operands use standard JSON string decoding.
`type`, `bsel`, and `bsel_next` strings may contain encoded line terminators; `tsel`
strings must be nonempty and stay within one logical line. Both anchors of a block
selector must be nonempty and different.

Paths are nonempty filesystem paths normalized with the host OS path rules. Relative
paths resolve from cwd and are stored as root-relative identities. Absolute paths are
accepted when their cleaned spelling lies beneath the canonical absolute name used to
open root and are then converted to root-relative identities. Equivalent absolute
symlink aliases are not resolved outside the root capability. Root-scoped lookup rejects
paths that escape through symlinks and, following Go's `os.Root` contract, paths through
absolute symlink targets. Translation always emits root-relative paths; a downstream
patch consumer must apply the envelope from the same root. Trailing operands and unknown
commands are invalid.

Acceptance:

1. JSON escapes allow spaces, quotes, tabs, and newlines in inserted or anchored text.
2. Signed, zero, malformed, or mixed line ranges fail before filesystem mutation,
   patch output, or final-state reporting.
3. Optional `tsel COUNT` selects one contiguous occurrence span and omission remains
   equivalent to count one.
4. File commands may be interleaved with text commands. File lifecycle commands observe
   preceding pending state, while selectors retain immutable baseline meaning.
5. Missing operands, invalid JSON, invalid counts, trailing operands, empty or identical
   block anchors, and unknown or future commands fail before filesystem mutation, patch
   output, or final-state report.
6. With root `/workspace` and cwd `bin/worktree`, script path `main.go` denotes
   `/workspace/bin/worktree/main.go` and translates as `bin/worktree/main.go`.

## REQ-FILE-001 — File commands

The first `in PATH` for an existing logical file loads and captures its immutable UTF-8
baseline. It selects that baseline for subsequent commands, clears the prior text
selection, and places its baseline cursor at `0:0`. Returning to the same logical file
reuses the captured baseline, resets its cursor and selection, and retains its recorded
baseline-coordinate edits. The baseline is never rebuilt from pending content.

`new PATH` creates and selects a pending file with an empty baseline at cursor `0:0`.
It fails if the logical path already exists. It accepts at most one effective content
insertion because every cursor insertion targets the same empty-baseline position. A
created file does not reach the filesystem unless the complete script validates,
stages, and commits successfully.

`mv PATH` moves the active logical file to an unoccupied logical path. The destination
becomes active and preserves the current baseline cursor or selection. Recorded edits
move with the file, and the immutable baseline identity does not change. Later `in`
commands resolve the new path, not the old one. Multiple moves of one original file
collapse to its original-to-final move in the net change set.

`rm` marks the active logical file as deleted, then clears the active file, cursor, and
selection. Removing an existing baseline file after any content edit is a conflict and
rejects the complete script; edits are never silently discarded. Removing an unedited
existing file yields deletion of its original path, including after a move. Removing a
file created in the same script cancels that creation. Later text or lifecycle
operations require another `in` or `new`.

`in` fails for a missing or deleted logical path. `new` and `mv` fail on any logical
destination collision, including a path occupied in the initial tree or by pending
state. `mv`, `rm`, selection commands, and edit commands fail without an active file.
Initial files must resolve to regular UTF-8 files when first selected. The parent
directory of a `new` or `mv` destination must already exist; hpatch does not create
directories that are absent from the script's file-operation model.

Acceptance:

1. A script can edit multiple files by switching among them; reselecting a path retains
   recorded edits but exposes the same baseline at cursor `0:0`.
2. One complete-content `type` can create a file, and `mv` and `rm` produce the specified
   net additions, moves, and deletions.
3. Repeated effective writes to one new-file baseline position, removal after an
   existing-file content edit, missing paths, destination collisions, use-after-delete,
   and file commands without an active file fail before commit or patch output.

## REQ-SELECT-001 — Immutable baseline selections and cursor

Every existing logical file has one immutable baseline captured on first `in`. A new
file has an empty baseline. Every `sel`, `tsel`, `bsel`, `bsel_next`, and `rsel` command
resolves only against that baseline; recorded replacements, deletions, duplications, and
insertions never change selector text, line references, occurrences, scopes, or positions.
Text introduced by an earlier command is not selectable and cannot create an unrelated
literal or anchor collision. A selector whose baseline span overlaps content already
replaced or deleted by an earlier edit is rejected immediately with the prior command
and affected baseline lines identified.

Each active file has either a baseline cursor or a nonempty baseline selection. `in` and
`new` establish cursor `0:0`, before the first baseline code point. A cursor is an
insertion position; a selection is a half-open baseline span produced by the inclusive
commands below. Empty baselines have no selectable line.

`sel` selects columns within one resolved baseline logical line. Columns count Unicode
code points, including one code point per tab; the line terminator is not selectable.

`tsel` collects exact, non-overlapping literal occurrences within one resolved baseline
logical line. With positive `OCCURRENCE`, it selects from that occurrence's start forward
through the end of `COUNT` consecutive occurrences. With negative `OCCURRENCE`, it
selects backward from that occurrence through `COUNT` consecutive occurrences and
normalizes the resulting contiguous span into ascending baseline order. Source content
between the first and last selected token is included. The complete requested group must
exist. Omitted `COUNT` is one, so existing single-occurrence behavior is unchanged.

`bsel` always uses the complete active-file baseline as its search scope, independent of
the current cursor or selection. `bsel_next` explicitly retains stateful search: it uses
the current baseline selection when one exists, otherwise the suffix from the baseline
cursor through EOF, and never wraps. Both commands resolve `START` uniquely within their
scope, then search for `END` only after the complete resolved start; end occurrences
before the start do not collide. `END` must occur uniquely in that suffix and must not
overlap `START`.

For each block anchor, exact occurrences are authoritative when any exist. Only when the
exact count is zero does matching retry with every nonempty run of ASCII space or tab in
the anchor equivalent to one nonempty run of ASCII space or tab in the baseline. This
fallback cannot omit whitespace, cross a line terminator, or normalize other Unicode
whitespace. A leading whitespace run has one candidate at the start of the corresponding
baseline run rather than one candidate per byte. Matching returns original baseline byte
boundaries. Missing or ambiguous exact or fallback anchors fail instead of guessing.
Both selectors include both anchors and all baseline content between them.

`rsel` selects an inclusive range of complete baseline logical lines after validating
endpoint order and bounds. For every selected line it owns the line terminator when one
is present. It records a linewise selection so deletion removes complete lines and
duplication creates another complete adjacent line range, including when the selected
final line has no terminator.

Every selection replaces the previous cursor or selection. After `type`, the cursor is
at the selected baseline span's end; an insertion at a cursor remains at that same
baseline position. `del` leaves the cursor at the selection start. `dup` leaves it at
the selection end and clears the selection because the inserted copy is not baseline
content.

Acceptance:

1. Absolute selectors constructed from the inspected pre-edit file retain the same
   meaning after earlier edits.
2. Positive and negative multi-occurrence `tsel` spans include intervening source text;
   missing initial or complete occurrence groups fail, and overlapping token candidates
   retain non-overlapping occurrence semantics.
3. `bsel` finds unique anchors before or after the cursor; `bsel_next` respects its
   selection- or cursor-derived scope and never wraps.
4. An end occurrence before the resolved start does not collide, while any second start
   in scope or second end after start remains ambiguous.
5. A unique spaces-versus-tabs fallback succeeds and maps to original baseline bytes;
   whitespace-equivalent collisions, newline crossing, absent whitespace, and unrelated
   Unicode whitespace do not select a block.
6. Missing baseline lines, columns, ranges, literal occurrences, or block anchors fail
   rather than selecting pending or nearby content.

## REQ-EDIT-001 — Baseline-coordinate edits

`type STRING` records insertion of the decoded string at the baseline cursor, or
replacement of the selected baseline span. `del` requires a selection and records
replacement of that span with empty content. `dup` requires a selection, copies its
immutable baseline content, and records that copy as an insertion at the selection end.
No edit action reads an intermediate mutated representation.

Recorded edits must be disjoint in baseline coordinates. Two replacements or deletions
that overlap are rejected. An insertion strictly inside a replaced or deleted span is
rejected. Multiple effective insertions at one baseline position are rejected as
ambiguous. An insertion exactly at a replacement boundary is permitted and appears on
that side of the replacement. Conflict diagnostics identify the prior command and the
overlapping baseline line or position. All conflicts reject the complete script before
filesystem mutation or patch output.

When replacing a linewise selection that owns a final LF, CRLF, or standalone-CR
terminator, `type` preserves that exact terminator if the replacement does not end in
a terminator. A replacement-supplied terminator is authoritative and is not doubled.
No terminator is synthesized for an unterminated selected final line. Consequently,
`rsel` followed by `type "replacement"` retains separation from the following line;
`type ""` leaves an empty logical line, while `del` removes the selected lines.

For characterwise selections, `dup` copies the baseline bytes exactly. For linewise
selections, the duplicate is an adjacent complete range; if the baseline range ends at
an unterminated final line, the baseline file's first line-ending style is inserted
between the original and copy, or LF if the baseline has no line ending. The duplicate
remains unterminated and is not selectable within the same script.

Existing line-ending bytes outside explicitly inserted text are preserved. After the
complete script validates, hpatch orders the disjoint baseline edits, materializes one
final content value from the immutable baseline, and passes that value to the shared
normal/translate change set.

Acceptance:

1. Independent baseline-coordinate replacement, deletion, insertion, and duplication
   produce the specified result in either script order.
2. Overlapping and nested ranges, an insertion inside a replaced range, repeated
   insertion at one position, and removal of an edited existing file fail atomically
   with the prior edit and baseline collision identified.
3. Text introduced by an earlier edit cannot be selected or create a selector collision.
4. LF, CRLF, and standalone-CR linewise replacement preserve the selected final
   terminator unless replacement text supplies one; an unterminated final line remains
   unterminated.

## REQ-OUTPUT-001 — Output, final state, and failure

Input is read completely and the entire script is evaluated before commit or stdout.
An unchanged normal-mode change set performs no filesystem operation but still reports
its final active editor state. An unchanged translate result emits no patch and fails
because it cannot represent an update; it emits no final-state report.

Translate output contains file actions in deterministic first-touch order:

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
equal remove/add of the empty line representation for an empty file. Translation is
fully rendered before stdout is written.

After every command and the requested normal commit or translated patch write succeed,
the CLI writes one final-state report to stderr. For cursor state its first line is
`in PATH LINE:COLUMN`; for selection state it is
`in PATH START_LINE:START_COLUMN-END_LINE:END_COLUMN`. If `rm` leaves no active file,
the report is `no active file`. The report describes only the completed invocation;
file, cursor, and selection state do not persist into a later invocation.

Coordinates and previews describe the rendered post-edit content, not immutable-baseline
positions. Cursor affinity is after replacement or insertion for `type`, at the deletion
join for `del`, and after the inserted copy for `dup`. A selection left active is reported
as a range rather than a fictitious cursor. The active path reflects pending moves. A new
empty file reports position `1:1` and one empty preview line numbered 1.

After the header, the report writes up to three total logical lines nearest the cursor or
selection start: normally the preceding, containing, and following lines; at a boundary,
the first or last three available lines without duplication. Each row is `LINE TEXT`.
`TEXT` contains at most the first 64 Unicode code points of rendered line content, without
a line terminator or added ellipsis. Control characters are escaped so each preview stays
on one output line. The complete report is rendered before commit or patch output, but it
is emitted only after that mode-specific effect succeeds. A report-write failure after
the effect is best-effort and cannot retroactively change the successful effect or claim
rollback.

Normal mode stages new contents in same-directory temporary files before starting the
commit. Parse, validation, read, and evaluation failures leave the initial tree
unchanged. A staging failure attempts to remove all temporary artifacts; cleanup failure
returns nonzero and identifies every artifact it could not remove. Commit-time filesystem
failures trigger rollback attempts using staged backups. Ordinary filesystems cannot
provide a portable crash-atomic transaction over multiple paths: termination, machine
failure, or rollback failure during commit can leave a partial change set. Such a failure
must return nonzero and name the affected paths; it must never report success or claim
rollback succeeded when it did not. Existing file permission bits are preserved; files
created by `new` use mode `0644`.

OpenAI `apply_patch` is a logical-line format and cannot preserve CRLF or standalone-CR
bytes when its output is applied by the tool. Translate mode therefore emits LF-only
patch text and normalizes line endings only in its displayed before/after lines. It does
not modify source files. Normal mode continues to preserve existing line endings outside
explicitly inserted strings. Applying translated output to a non-LF file may normalize
that file to LF; this is a declared format limitation, not byte equivalence.

Failures emit one concise diagnostic to stderr, prefixed with `hpatch:`. Script
diagnostics identify the one-based nonblank command index, one-based source line,
operation, relevant operand or selected path when one exists, and a failure category
(`syntax`, `file`, `selection`, or `edit`). Control bytes in every diagnostic are escaped
and embedded newlines are folded so one failure remains one logical line. Failures return
nonzero and emit no stdout or final-state report. Block-selection diagnostics identify
whole-file versus state-derived scope and exact versus whitespace-tolerant ambiguity;
malformed line syntax receives a syntax diagnostic rather than selecting a nearby line.

A command failure that addressed an existing baseline additionally writes repair context
on the lines following its diagnostic. Selectors resolve against a baseline the caller
cannot see, so a diagnostic alone forces a blind retry that costs a whole script; repair
context supplies the measurements that failure implies. A rejected column range reports the
addressed line's rune-column count, restates that one tab is one column, and lists the
rune-column span of each whitespace-separated token on that line. Token spans are used
rather than sampled columns because a sampled character usually recurs on the line and
cannot be located unambiguously. An out-of-range line or line range reports the file's
line count. A missing or ambiguous block anchor reports each anchor's occurrence lines
within the searched scope and restates that a block selection includes both anchors. An
edit conflict reports which baseline lines earlier commands already claim. Every repair
block includes a window of baseline lines around the addressed line, marks that line, and
escapes control characters so each rendered line stays on one output line. A failure with
no active baseline, including a missing file, emits its diagnostic alone. Repair context is
supplementary: it never changes exit status, stdout, mutation, or metrics classification.

Acceptance:

1. Normal success has empty stdout and one rendered final-state report on stderr after
   commit; translate success has patch-only stdout and one pending-state report on stderr
   after the patch is completely written.
2. Rendered cursor affinity, selection ranges, moved paths, empty files, three-line
   boundary windows, Unicode columns, 64-code-point truncation, and control escaping
   produce the specified report without implying cross-invocation persistence.
3. Malformed input, malformed or out-of-bounds line selection, unrelated literal or
   whitespace collision, unknown or future command, invalid UTF-8, missing or non-regular
   file, logical path collision, staging failure, translation failure, and cancellation
   produce no mutation, patch output, or final-state report.
4. Injected commit and rollback failures are reported without false atomicity claims and
   without a successful final-state report.
5. Failure to write a fully rendered report after a successful external effect does not
   reverse that effect or record a complete report-input token estimate.
6. A rejected column range, out-of-range line, unresolved or ambiguous anchor, and edit
   conflict each emit repair context sufficient to correct the command without rereading
   the file; a failure with no active baseline emits its diagnostic alone.

## REQ-GUIDE-001 — Agent guidance

The built-in top-level help owns the complete agent editing, orchestration,
trust-boundary, validation, final-state-report, and metrics reference. Tool help reuses
its agent workflow through final-state-report sections rather than maintaining a second
editing-language definition, and adds only custom-tool path guidance. Tool help excludes
CLI usage and mode descriptions, options, metrics, and version material. Both references
document `bsel` as whole-file and `bsel_next` as explicitly stateful, horizontal-whitespace
tolerance, and optional `tsel COUNT`. The project agent instruction file only directs
agents to run and follow `hpatch --help`; it does not duplicate language semantics that
can become stale.
