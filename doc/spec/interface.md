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
and validation. `hpatch --tool-help` emits a separate, shorter model-facing summary of
selector choice, baseline rules, and safety. It omits CLI usage and mode descriptions,
root and cwd options, metrics, and version material, and includes workspace-relative path
and parent-directory preparation guidance. It is not a generated slice of top-level help.
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
6. Tool help remains a concise model-facing summary of selector choice, baseline rules,
   safety, and tool-path guidance while excluding CLI-only material; top-level help remains
   the complete agent reference.
7. Unsupported aliases, trailing arguments, and unknown or future options fail with no
   stdout or final-state report.
8. A nested cwd changes relative path resolution while normal mutations and translated
   patch paths retain the same root-relative file identity.
9. A relative, absolute, or symlink path that escapes root fails without mutation,
   patch output, or final-state report.

## REQ-READ-001 — Routed hashline reader

In hpatch router mode, the agent receives a read-only `hread` custom tool beside
`hpatch`. Its grammar-constrained free-form input is a JSON string containing `PATH`,
optionally followed by a space and `START:END` for an inclusive bounded logical-line
range, for example `"editor.go" 40:80`. Line endpoints are positive one-based base-ten
integers, must be ordered, and must both exist; invalid syntax, a missing endpoint, or an
out-of-bounds range fails rather than clamping.

The reader resolves relative and absolute paths through the same pinned root-scoped
workspace boundary used by hpatch, accepts only regular UTF-8 files, and never mutates
the workspace. It emits each requested logical line as `HHHH: TEXT`. `TEXT` is exact
logical-line content without its terminator. `HHHH` is lowercase hexadecimal for the
first two bytes of SHA-256 over that exact content. A trailing file terminator does not
create an additional empty line.

Acceptance:

1. Whole-file and bounded reads emit exact UTF-8 content in source order with deterministic
   four-digit hashes that can be copied directly into routed selector operands.
2. A missing, non-regular, non-UTF-8, escaping, malformed-range, reversed-range, or
   out-of-bounds input returns a read diagnostic without mutation.
3. The router restores a replayed `hread` call to the original custom-tool shape just as
   it restores routed hpatch calls, without treating reads as editable correction history.
4. Reading and whole-file UTF-8 validation use bounded streaming storage, observe
   cancellation during processing, and reject before formatted output exceeds 16 MiB.

## REQ-METRICS-001 — Persistent token, command, and feature metrics

Every recognized normal or translate invocation is classified after its terminal outcome.
A successful nonempty change set that parses, evaluates, translates, and completes its
requested output or mutation contributes paired estimates for two semantically equivalent
tool calls. A failed invocation contributes only its generated `hpatch` call estimate to
the ineffective-output counter; it contributes nothing to the effective `hpatch` counter.
A failed routed invocation is represented downstream by an exec carrier that returns its
diagnostic and repair context. Its comparison baseline is the fixed direct-call program
carrying `*** Begin Patch\n*** End Patch\n`; that tokenized semantic baseline contributes
to the failed `apply_patch` output counter. The diagnostic carrier itself never counts as
`apply_patch` output. The complete failed hpatch call remains in the ineffective-output
counter and reduces the overall output savings. `gain`, informational commands, and
unsupported argument forms do not contribute metrics.

Both effective and ineffective hpatch estimates count the `functions.hpatch` tool name
followed by the editing payload the model emitted. When a correction names commands of a
rejected script, the shorter correction is charged while the rebuilt complete script is
used only for evaluation. The successful direct side counts the `functions.exec` tool name
and a fixed free-form program that passes the complete translated patch envelope,
serialized as one string argument, to `tools.apply_patch`, then returns that nested tool's
result. The router-only marker and hpatch final-state report are excluded from this semantic
baseline. All estimates use the tokenizer library's GPT-5 model mapping. Script and patch
text remain data and cannot alter the fixed direct-call program.

A final-state report successfully emitted by normal or translate mode contributes its
exact rendered text to a separate estimated state-report input-token counter. This is
model-input overhead because the tool result becomes subsequent model context; it is not
added to either model-output counter.

The host tool definition is also model input. The router obtains the session identity,
installed hpatch definition, and displaced native patch definition directly from the
routed request. The first classified request of a session counts those definitions once;
subsequent requests in the same session add nothing because the resent definition is
served from the provider's prompt cache. A host that supplies no session or definition
text leaves these counters at zero, and gain states which inputs were measured so a zero
is not read as a free tool. A failed or cancelled invocation emits no report
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

`tsel` attempts are additionally classified as single selection when `COUNT` is omitted
or one, and multiple selection when the operand is present and intended to select more
than one match; an invalid count remains attributable to the multiple attempt.

Terminal command errors carry stable internal reason identifiers so gain can distinguish
malformed or out-of-bounds coordinates, missing occurrences, invalid occurrence counts,
order or overlap, edit conflicts, missing active files, and other supported failure
families without making user-facing diagnostic wording part of the persisted format.

The aggregate is stored in `hpatch/metrics.bin` beneath the platform user configuration
directory returned by Go's `os.UserConfigDir`. Updates hold an exclusive interprocess
lock at `hpatch/metrics.lock`; gain reads hold a shared lock. The metrics file contains
	two alternating current-version fixed-size slots holding token, command, selector,
	error-reason counters, a generation, and a checksum. A reader uses the
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
ineffective hpatch output with the fixed direct-call program carrying the empty patch
semantic baseline. The downstream diagnostic carrier is excluded. The all-calls row
reports both totals and the overall output-token reduction.

Effective-only reduction compares effective hpatch output against generated `apply_patch`
output. Overall reduction is `(effective_apply_patch + failed_apply_patch - effective_hpatch
- ineffective_hpatch) / (effective_apply_patch + failed_apply_patch) * 100`. It is zero
when the `apply_patch` denominator is zero. No retry payload is inferred.

Gain then writes a separate input-token table for final-state reports, failure diagnostics,
and tool definitions. Hpatch and `apply_patch` values remain separate:
the report does not subtract definitions, convert input to output, or calculate a combined
input/output percentage. Unmeasured `apply_patch` input sources are labeled `not measured`.

Gain then writes stable-order compact tables for aggregate command invocation and error
rates; `tsel` and `rsel` selector counters; single and multiple `tsel` spans;
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
3. Each `tsel` feature metric reconciles with its aggregate command counter.
4. Single and multiple `tsel` attempts and stable terminal reasons remain independently
   attributable. Per-command reason counts reconcile with both aggregate command errors and
   aggregate reason totals.
5. Every definition-bearing request increments the definition-request counter, while the
   hpatch and baseline definition tokens accumulate only once per distinct session. An
   absent session or definition leaves definition counters zero and reports which inputs
   were measured.
6. Failed hpatch invocations contribute their complete output to the ineffective counter;
   the failed `apply_patch` counter receives the fixed direct-call program carrying the
   empty patch envelope, while the downstream diagnostic carrier is excluded.
7. A correction is charged as the shorter payload the model emitted for both effective and
   ineffective invocations while evaluation uses the rebuilt complete script.
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

Outside a heredoc body, blank lines are ignored and every other physical line begins one
command. Commands are:

```text
in PATH
new PATH
mv PATH
rm
tsel HASH "QUOTED STRING" [COUNT]
rsel START_HASH END_HASH
type "QUOTED STRING"
type <<TAG
BODY
TAG
del
copy
cut
paste
commit
```

Each selector `HASH`, `START_HASH`, and `END_HASH` is exactly four lowercase hexadecimal
digits copied from `hread`. Cursor `0:0` denotes the position before the first code point
of a file.

`HASH` identifies the inclusive lower search boundary for `tsel`. Optional `COUNT`
defaults to one and must be a positive base-ten integer. Inline quoted operands retain
the existing JSON escape repertoire and Unicode decoding, while additionally accepting
a literal horizontal tab. A literal quote, backslash, carriage return, line feed, NUL, or
C0 control other than horizontal tab must remain escaped. `type` strings may contain
encoded line terminators; `tsel` strings must be nonempty and stay within one logical
line.

A multiline `type` operand uses `type <<TAG`, where `TAG` matches
`[A-Za-z0-9_.-]{1,64}` and may optionally be wrapped in matching single or double quotes
on the header only. Every following physical line is literal UTF-8 payload data until
a line exactly equal to the unquoted `TAG`; no escape, interpolation, or indentation
processing occurs.
The payload consists of the bytes between the header terminator and the start of the
closing delimiter, so a nonempty final body line contributes its physical line terminator.
The header, body, and delimiter form one command whose command index is the header's;
body lines retain their physical source lines but are never parsed as commands. An
unterminated or oversized heredoc is one syntax failure at its header and cannot consume
unbounded input.

Paths are nonempty filesystem paths normalized with the host OS path rules. Relative
paths resolve from cwd and are stored as root-relative identities. Absolute paths are
accepted when their cleaned spelling lies beneath the canonical absolute name used to
open root and are then converted to root-relative identities. Equivalent absolute
symlink aliases are not resolved outside the root capability. Root-scoped lookup rejects
paths that escape through symlinks and, following Go's `os.Root` contract, paths through
absolute symlink targets. Translation always emits root-relative paths; a downstream
patch consumer must apply the envelope from the same root. Trailing operands and unknown
commands are invalid. `commit` accepts no operand.

Acceptance:

1. Existing valid JSON-quoted operands retain their decoded values, and a literal tab in
   an inline operand decodes as one tab without manual escaping.
2. A `type` heredoc inserts its literal multiline body, including quotes, backslashes,
   tabs, and physical line terminators, without interpreting body lines as commands.
3. An invalid delimiter, unterminated heredoc, forbidden inline control character,
   malformed escape, or trailing operand fails before filesystem mutation, patch output,
   or final-state reporting; an unterminated body produces one header-owned error.
4. A malformed selector hash fails before filesystem mutation, patch output, or
   final-state reporting.
5. Optional `tsel COUNT` establishes that many separate literal selections and omission
   remains equivalent to count one.
6. File commands may be interleaved with text commands. File lifecycle commands observe
   preceding pending state within their generation, while selectors retain that
   generation's immutable-baseline meaning.
7. Missing operands, invalid counts, and unknown or future commands fail before filesystem
   mutation, patch output, or final-state reporting.
8. With root `/workspace` and cwd `bin/worktree`, script path `main.go` denotes
   `/workspace/bin/worktree/main.go` and translates as `bin/worktree/main.go`.

## REQ-CORRECT-001 — Compact rejected-script correction

After a routed hpatch script is rejected, a correction payload may transform the rejected
script with one operation per nonblank command header:

```text
N: COMMAND     replace command N
N: accept      apply hpatch's displayed safe correction for command N
-N             delete command N
+N: COMMAND    insert before command N
N+: COMMAND    insert after command N
```

A replacement or insertion whose command is `type <<TAG` consumes its heredoc body and
closing delimiter as part of that one correction operation. All indices refer to the
original rejected script before any correction operation is applied. Replacements,
acceptances, and deletions may name an index at most once and conflict with each other for
the same index. An acceptance is valid only when the immediately repairable rejected
script retained an exact correction for that command; it never approves the rejected
mutation itself. Multiple insertions at one anchor are allowed and retain payload order;
their position is relative to the original anchor even when that anchor is deleted. Every
nonblank line outside a correction heredoc must be a correction operation.

The router validates all operations, retained acceptances, and referenced indices before
rebuilding the script. It then reparses and reevaluates the complete transformed script
against the unchanged workspace. A correction failure changes nothing. A successful
transformation becomes the base for a later correction, retains the correction-chain
correlation ID, increments the attempt, and charges metrics for only the compact payload
the agent emitted.

Acceptance:

1. `N: COMMAND` remains compatible with existing replacement corrections.
2. `N: accept` substitutes exactly the safe correction displayed for command N; an absent
   or stale suggestion rejects without evaluating or mutating the workspace.
3. `-N`, `+N: COMMAND`, and `N+: COMMAND` can remove obsolete commands and insert new
   commands without resending the complete script.
4. Multiple same-anchor insertions preserve payload order, including when the anchor is
   deleted, while duplicate replacement/acceptance/deletion or an absent index rejects
   the complete correction.
5. A correction heredoc is one operation; an invalid or unterminated correction heredoc
   produces one bounded diagnostic and does not reinterpret its body as operations.
6. Every corrected script is revalidated atomically against the unchanged workspace and
   retains the established correlation and emitted-payload metrics behavior.

## REQ-FILE-001 — File commands

An invocation begins with generation zero. The first `in PATH` for an existing logical
file loads the UTF-8 contents visible in that generation and captures them as the file's
immutable generation baseline. It selects that baseline, clears the prior text selection,
and places its cursor at `0:0`. Returning to the same logical file within the generation
reuses the captured baseline, resets its cursor and selection, and retains its recorded
baseline-coordinate edits.

`new PATH` creates and selects a pending file with an empty generation baseline at cursor
`0:0`. It fails if the logical path already exists in the current generation. Before a
`commit`, it accepts at most one effective content insertion because every cursor
insertion targets the same empty-baseline position. A created file does not reach the
filesystem unless the complete script validates and its requested external output or
mutation succeeds.

`mv PATH` moves the active logical file to an unoccupied logical path in pending state.
The destination becomes active and preserves the current baseline cursor or selection.
Recorded edits move with the file. Later `in` commands resolve the new path, not the old
one. Multiple moves across generations collapse to the original-to-final move in the net
change set.

`rm` marks the active logical file as deleted in pending state, then clears the active
file, cursor, and selection. Removing an existing baseline file after a content edit in
the same generation is a conflict; pending edits are never silently discarded. Removing
an unedited existing file yields deletion of its original path, including after a move.
Removing a file created in the same generation cancels that creation. A later generation
may remove content materialized by an earlier `commit`, and the final net change remains
relative to the invocation's original workspace.

`commit` validates and materializes every pending file and content change as the next
immutable in-memory generation baseline. It performs no filesystem mutation and emits no
intermediate patch or report; a later failure or cancellation rejects the complete
invocation. Pending edit sets are cleared. If the active file still exists, it remains
active under its current path with no selection and cursor `0:0`; if it was deleted, no
file remains active. A no-op `commit` succeeds with the same state-reset behavior.

At successful script end, hpatch finalizes pending content for normal or translate output
without applying the explicit barrier's cursor or selection reset.

`in` fails for a missing or deleted logical path. `new` and `mv` fail on a logical
destination collision in the current generation, including a path occupied in the
initial tree or by pending state. `mv`, `rm`, selection commands, and edit commands fail
without an active file. Initial files must resolve to regular UTF-8 files when first
selected. The parent directory of a `new` or `mv` destination must already exist; hpatch
does not create directories that are absent from the script's file-operation model.
Generation materialization tracks only touched logical files and does not bypass existing
script, content, or translated-output capacity limits.

Acceptance:

1. A script can edit multiple files by switching among them; reselecting a path within a
   generation retains pending edits but exposes the same generation baseline at cursor
   `0:0`.
2. After `commit`, selectors can address content and paths materialized by earlier
   commands, while the filesystem and translated output remain unchanged until the whole
   invocation succeeds.
3. `commit` clears the selection, resets a surviving active file to cursor `0:0`, follows
   its pending move, and clears the active file after deletion; a no-op barrier succeeds.
4. File creation, content edits, moves, and removals may span generations and collapse to
   one original-to-final net change set.
5. Repeated effective writes to one generation's new-file baseline position, removal
   after an existing-file content edit in the same generation, missing paths, destination
   collisions, use-after-delete, and file commands without an active file fail before
   external mutation or patch output.
6. Failure or cancellation after any number of `commit` barriers exposes no intermediate
   filesystem change, patch, or final-state report.

## REQ-SELECT-001 — Immutable generation-baseline selections and cursor

Each current logical file has one immutable baseline per generation. A new file has an
empty generation baseline. Routed selectors name every addressed logical line with the
four-digit `HASH` emitted by `REQ-READ-001`. Resolution scans the immutable baseline and
succeeds only when exactly one logical line has that hash. Zero matches and multiple
matches reject atomically; repeated identical content and distinct-content 16-bit hash
collisions are both ambiguous. Range endpoints resolve independently and must remain
ordered by their resolved baseline positions. Because the public identity is 16 bits,
different content that changes to the same uniquely occurring hash is indistinguishable
and retains an approximately 1-in-65,536 random false-acceptance residual.

Pending replacements, deletions, and insertions never change selector hashes, scopes,
matches, or positions. Text introduced by an earlier command is not selectable in the
same generation and cannot create an unrelated literal collision. After `commit`,
materialized content forms the next baseline and is selectable there. A selector whose
current-generation baseline selection overlaps content already replaced or deleted by an
earlier pending edit is rejected atomically with the prior edit and affected baseline
lines identified.

Each active file has either a generation-baseline cursor or a nonempty ordered set of
disjoint generation-baseline selections. `in`, `new`, and a `commit` that retains the
active file establish cursor `0:0`, before the first baseline code point. A cursor is an
insertion position; every selection is a half-open baseline span produced by the
inclusive commands below. `rsel` produces one selection; `tsel` may produce more than
one. Empty baselines have no selectable line.

`tsel` starts searching at column 1 of the uniquely hash-resolved baseline anchor and
continues forward through EOF. It establishes the first `COUNT` separate exact literal
matches as an ordered selection set. Matching is left-to-right and resumes after each
complete match, so selected matches cannot share baseline bytes. Matches may occur on
different logical lines, but the literal itself cannot contain a line terminator.
Omitted `COUNT` is one.

When the suffix from the hash-resolved anchor does not yield `COUNT` matches, the command
fails without changing state. It never broadens the search before that verified anchor,
even when earlier baseline matches would complete the requested count.

`rsel` selects an inclusive range of complete baseline logical lines between its uniquely
hash-resolved start and end anchors after validating endpoint order. For every selected
line it owns the line terminator when one is present. It records one linewise selection
so deletion removes complete lines and linewise clipboard content retains its
complete-line identity, including when the selected final line has no terminator.

Every selector replaces the previous cursor or selection set. An edit over multiple
selections applies the same action to every selection atomically and then collapses state
to one cursor at the final selected match in baseline order: `type` uses that selection's
end, while `del` and `cut` use its start. An insertion at a cursor remains at that same
baseline position. `copy` preserves the selection set. `paste` inserts after every active
selection, then clears the set and leaves one cursor at the final selection's baseline
end; at a cursor it retains the existing single insertion behavior. `commit` clears
either state and resets a surviving active file to `0:0` in the next generation.

Acceptance:

1. Within a generation, selectors constructed from `hread` hashes retain the same meaning
   after earlier pending edits; after `commit`, selectors use hashes from the materialized
   next baseline.
2. `tsel` finds separate non-overlapping matches from the inclusive hash anchor through
   EOF, may select matches on different lines, never includes source text between its
   selected matches, and rejects an incomplete requested count.
3. An incomplete hash-anchored `tsel` suffix never selects matching content before its
   resolved anchor, even when the whole baseline contains exactly `COUNT` matches.
4. A uniquely occurring hash resolves to its logical line; a missing hash, duplicate
   content, or truncated-hash collision fails rather than selecting one candidate.
5. `rsel` resolves both hash endpoints independently, rejects reversed resolved order, and
   selects every complete logical line in the inclusive resolved range.
6. Missing baseline anchors, literal matches, or content already claimed by pending edits
   fail rather than selecting pending or nearby content; materialized prior-generation
   content is selectable.

## REQ-EDIT-001 — Baseline-coordinate edits

`type STRING` records insertion of the decoded string at the generation-baseline cursor,
or the same replacement over every span in the active selection set. `del` requires a
selection set and records an empty replacement for every selected span. `copy` requires a
selection set, stores its immutable baseline content in invocation-local clipboard state,
and preserves the set; because only `tsel` creates multiple selections and all of its
selected spans contain the same literal, one shared clipboard value represents them.
`cut` stores the same shared content and deletes every selected span. `paste` requires
clipboard content and records one insertion after every active selection, or one insertion
at the cursor. Each multi-selection edit is one atomic command: failure or conflict at
any generated edit records none of them. No edit action reads pending mutated content.

Recorded edits must be disjoint in current-generation baseline coordinates. Two
replacements or deletions that overlap are rejected. An insertion strictly inside a
replaced or deleted span is rejected. Multiple effective insertions at one baseline
position are rejected as ambiguous. An insertion exactly at a replacement boundary is
permitted and appears on that side of the replacement. Conflict diagnostics identify the
prior command and the overlapping generation-baseline line or position. All conflicts
reject the complete script before filesystem mutation or patch output. A `commit` first
requires this pending edit set to validate; after it materializes the next baseline, later
edits no longer conflict merely because they address content changed in the prior
generation.

When replacing a linewise selection that owns a final LF, CRLF, or standalone-CR
terminator, `type` preserves that exact terminator if the replacement does not end in a
terminator. A replacement-supplied terminator is authoritative and is not doubled. No
terminator is synthesized for an unterminated selected final line. Consequently, `rsel`
followed by `type "replacement"` retains separation from the following line; `type ""`
leaves an empty logical line, while `del` or `cut` removes the selected lines.

Characterwise clipboard content copies the selected baseline bytes exactly; a `tsel`
selection set copies its common literal once. Linewise clipboard content preserves
complete-line identity. When pasted at a boundary lacking a line terminator, it adds only
the missing boundary terminator, using the active baseline's first line-ending style or LF
when none exists. Clipboard state survives `commit` within the invocation, but pasted
content becomes selectable only after a later `commit`.

Existing line-ending bytes outside explicitly inserted text are preserved. At each
`commit`, hpatch orders the disjoint pending edits, materializes one next-generation
content value from the current immutable baseline, and clears the pending edit set.

At successful script end, it renders pending edits into the original-to-final change set
while preserving the active post-edit cursor or selection for final-state projection.

Acceptance:

1. Independent generation-baseline replacement, deletion, insertion, copy, cut, and paste
   produce the specified result in either script order when their pending spans are
   disjoint; one multi-selection command applies to every selected match or none.
2. Overlapping and nested ranges, an insertion inside a replaced range, repeated insertion
   at one position, and removal of an edited existing file in the same generation fail
   atomically with the prior edit and baseline collision identified.
3. Text introduced by an earlier pending edit cannot be selected or create a selector
   collision until `commit`; after the barrier it is ordinary immutable baseline content.
4. LF, CRLF, and standalone-CR linewise replacement preserve the selected final
   terminator unless replacement text supplies one; an unterminated final line remains
   unterminated.
5. Clipboard content survives a generation barrier, while selections and cursors reset as
   specified by `REQ-SELECT-001`.

## REQ-OUTPUT-001 — Output, final state, and failure

Input is read completely and the entire script is evaluated before an external filesystem
commit or stdout. Script `commit` barriers only advance the in-memory generation and
never create an externally visible partial result. Before finalization, every changed file
whose final path ends in `.go` is parsed and formatted with Go's standard-library
`go/format`; a parse failure rejects the complete transaction, while non-Go files receive
no language validation. An unchanged normal-mode change set performs no filesystem
operation but still reports its final active editor state. An unchanged translate result
emits no patch and fails because it cannot represent an update; it emits no final-state
report.

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

After every command, in-memory generation barrier, and the requested normal filesystem
commit or translated patch write succeed, the CLI writes one final-state report to
stderr. For cursor state its first line is `in PATH LINE:COLUMN`; for one selection it
is `in PATH START_LINE:START_COLUMN-END_LINE:END_COLUMN`. For multiple selections it is
`in PATH N selections FIRST_START_LINE:FIRST_START_COLUMN-LAST_END_LINE:LAST_END_COLUMN`;
the range is a bounded envelope and does not claim that intervening content is selected.
If `rm` leaves no active file, the report is `no active file`. The report describes only
the completed invocation; file, cursor, and selection state do not persist into a later
invocation.

Coordinates and previews describe the rendered post-edit content, not immutable-baseline
positions. Cursor affinity is after replacement or insertion for `type`, at the deletion
join for `del`, and after the pasted insertion for `paste`. A selection left active is
reported as a range rather than a fictitious cursor. The active path reflects pending
moves. A new empty file reports position `1:1` and one empty preview line numbered 1.

After the header, the report writes up to three total logical lines nearest the cursor or
first selection start: normally the preceding, containing, and following lines; at a
boundary, the first or last three available lines without duplication. Each row is
`HHHH: TEXT`, matching `REQ-READ-001` so reported content can supply a later selector
hash. `TEXT` contains at most the first 64 Unicode code points of rendered line content,
without a line terminator or added ellipsis. Tabs are preserved and other control
characters are escaped so each preview stays on one output line. The complete report is
rendered before commit or patch output, but it is emitted only after that mode-specific
effect succeeds. A report-write failure after the effect is best-effort and cannot
retroactively change the successful effect or claim rollback.

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

Failures emit concise diagnostics to stderr, prefixed with `hpatch:`. Independently
parseable syntax failures may be reported together before evaluation. Script diagnostics
identify the one-based nonblank command index, one-based source line, operation, relevant
operand or selected path when one exists, and a failure category (`syntax`, `file`,
`selection`, or `edit`). A heredoc failure is owned by its header and may additionally
report its physical source-line span. Control bytes in every diagnostic are escaped and
embedded newlines are folded so one failure remains one logical line. Failures return
nonzero and emit no stdout or final-state report. Malformed hash syntax receives a syntax
diagnostic rather than selecting a candidate line.

A command failure after a hash resolves uniquely additionally writes repair context on
the lines following its diagnostic. Selectors resolve against a baseline the caller
cannot see, so a diagnostic alone can force a blind retry. An incomplete `tsel` match
reports the resolved anchor context. An edit conflict reports which current-generation
baseline lines earlier commands already claim and, when a later selector can safely be
rerun after materialization, identifies the selector command before which `commit`
belongs. Every repair block includes an `HHHH: TEXT` window of baseline lines around the
resolved anchor and escapes non-tab control characters so each rendered line stays on one
output line. A missing or ambiguous hash has no uniquely addressed line and emits its
diagnostic without choosing repair context. Repair context is supplementary: it never
changes exit status, stdout, mutation, or metrics classification.

Acceptance:

1. Normal success has empty stdout and one rendered final-state report on stderr after
   commit; translate success has patch-only stdout and one pending-state report on stderr
   after the patch is completely written.
2. Rendered cursor affinity, selection ranges, moved paths, empty files, hashline
   preview windows, Unicode columns, 64-code-point truncation, and control escaping
   produce the specified report without implying cross-invocation persistence.
3. Changed Go files are formatted with the standard library before output, and invalid Go
   rejects the transaction without mutation; non-Go files receive no language validation.
4. Malformed input, malformed, missing, ambiguous, or reversed hash selection, unrelated
   literal or whitespace collision, unknown or future command, invalid UTF-8, missing or
   non-regular file, logical path collision, staging failure, translation failure, and
   cancellation produce no mutation, patch output, or final-state report.
5. Injected external filesystem commit and rollback failures are reported without false
   atomicity claims and without a successful final-state report; script `commit` barriers
   remain externally invisible.
6. Failure to write a fully rendered report after a successful external effect does not
   reverse that effect or record a complete report-input token estimate.
7. An incomplete literal selection and an edit conflict each emit repair context sufficient
   to correct the command without rereading the file; a missing or ambiguous selector hash
   fails without guessing, and a failure with no active baseline emits its diagnostic alone.

## REQ-GUIDE-001 — Agent guidance

The built-in top-level help owns the complete agent editing, orchestration,
trust-boundary, validation, final-state-report, and metrics reference. Tool help is a
separate, shorter model-facing summary of selector choice, baseline rules, and safety; it
is not a generated slice of top-level help. Tool help excludes CLI usage and mode
descriptions, options, metrics, and version material.

Both references document the router-exposed `hread` tool, copyable hash-only references,
compact inline quoting, heredoc `type` (tool help teaches the grammar-constrained
`<<PATCH` delimiter), immutable generation baselines, explicit `commit`, selection-set
clipboard operations, `rsel` for multiline regions, and forward multi-selection
`tsel COUNT` with a strict uniquely hash-resolved lower boundary. The router appends only
its compact correction syntax and correction-chain rules to tool help. Host-specific
base-prompt overrides may steer tool choice without duplicating the full editing language;
complete semantics remain in top-level help and this interface contract.
