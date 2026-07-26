# Interface contract

## HP-CLI-001: Modes and informational output

`hpatch [--root ROOT] [--cwd CWD]` reads a complete script from standard input,
evaluates its complete change set in memory, stages required filesystem content, and
then commits it. It writes nothing on success unless metrics collection emits the
warning defined by `HP-METRICS-001`.

`hpatch translate [--root ROOT] [--cwd CWD]` performs the same parsing, filesystem
reads, and in-memory evaluation but never modifies a file. It writes one OpenAI
`apply_patch` envelope that represents the same net change set.

For normal and translate modes, omitted `--root` means the process current directory
and omitted `--cwd` means `.`. An explicit root must be absolute and is canonicalized
before it is opened. A relative cwd resolves beneath root. An absolute cwd is accepted
only when its canonical location is beneath root. Cwd must identify an existing
directory. The CLI opens root once and uses that pinned capability for the invocation.

`hpatch gain` reads no script and reports the persistent aggregate defined by
`HP-METRICS-001`. `hpatch --help` is the complete built-in agent reference for
stdin usage, process and editing commands, editor state, orchestration, trust boundaries,
and validation. `hpatch translate --help` summarizes translate-mode I/O and points to
top-level help. `hpatch --version` writes the module build version, or `devel` for an
unversioned build. Informational commands do not read stdin, resolve a working or
configuration directory, access metrics, or inspect project files. Any other argument
list is invalid.

Acceptance:

1. Given valid edits spanning multiple files, normal mode produces the specified final
   paths and contents with empty stdout and stderr.
2. For LF inputs, applying translate mode's stdout produces the same final paths and
   UTF-8 contents as normal mode. For other line endings, it represents the same
   logical-line edits subject to the normalization rule in `HP-OUTPUT-001`.
3. Translate mode leaves the source tree unchanged.
4. Gain mode leaves the source tree and metrics unchanged.
5. Each supported informational form writes its complete result to stdout with status
   zero and empty stderr without reading stdin or requiring a valid current directory.
6. Unsupported aliases, trailing arguments, and unknown/future options fail with no
   stdout.
7. A nested cwd changes relative path resolution while normal mutations and translated
   patch paths retain the same root-relative file identity.
8. A relative, absolute, or symlink path that escapes root fails without mutation or
   patch output.

## HP-METRICS-001: Persistent token and command metrics

Every recognized normal or translate invocation is classified after its terminal outcome.
A successful nonempty change set that parses, evaluates, translates, and completes its
requested output or mutation contributes paired estimates for two semantically equivalent
tool calls. A failed invocation contributes only its generated `hpatch` call estimate to
the ineffective-output counter; it contributes nothing to the effective `hpatch` or direct
`apply_patch` counters. Successful no-op invocations do not contribute token estimates.
`gain`, informational commands, and unsupported argument forms do not contribute metrics.

Both effective and ineffective hpatch estimates count the `functions.hpatch` tool
name followed by the complete free-form editing script. The successful direct side
counts the `functions.exec` tool name and a free-form program that passes the complete
translated patch envelope, serialized as one string argument, to `tools.apply_patch`.
All estimates use the tokenizer library's GPT-5 model mapping.

Script and patch text remain data in their respective tool calls and cannot alter the
fixed direct-call program. The patch is counted only as the nested patch tool's
model-authored input.

The estimates exclude provider-hidden protocol and reasoning tokens, assistant
commentary, server-generated identifiers, and tool results. They are reproducible
comparisons for these tool-call payloads, not authoritative API usage. A host with
a different tool name, formatting, schema, or token accounting can have different
actual output usage.

Classification is persisted only after the invocation's outcome is known. Translate mode
records a paired effective estimate after its complete patch reaches stdout; normal mode
records one after the staged changes commit. Stdin-read, parse, evaluation, translation,
stdout-write, and commit failures record only the canonical hpatch estimate as ineffective.
Every supported command reached by evaluation contributes one invocation. A supported
operation rejected by syntax parsing contributes one invocation and one error; an operation
whose path resolution or execution fails contributes one error after its invocation.
Unknown or future operations and failures outside command processing are not attributed to
a supported command. Successfully evaluated commands retain their invocation counts when a
later output or commit boundary fails. Successful no-op scripts contribute command counts
without contributing token estimates. In normal mode, failure to render the equivalent
patch after a successful commit emits a warning and records neither token classification,
but retains the successfully evaluated command counts. Failure to tokenize, lock, read,
write, or close metrics emits a concise `hpatch: warning:` diagnostic but does not change
the success or failure of the requested effect.

The aggregate is stored in `hpatch/metrics.bin` beneath the platform user configuration
directory returned by Go's `os.UserConfigDir`. Updates hold an exclusive interprocess
lock at `hpatch/metrics.lock`; gain reads hold a shared lock. The metrics file contains
two alternating fixed-size slots, each holding the effective hpatch, direct `apply_patch`,
ineffective hpatch, and per-command invocation and error counters, plus a generation and
checksum. A reader uses the valid current-format slot with the greatest generation, so an
interrupted write to the inactive slot leaves the preceding aggregate available. The file
reaches 512 bytes and does not grow further. Per-counter and aggregate overflow fails.
Persisted data with neither a valid current-format slot nor a valid mismatched-version slot
fails rather than producing a misleading report.

Only the latest metrics magic is decoded. A complete, checksummed slot whose eight-byte
magic starts with `HPATCH` but does not equal the current version resets the reported
totals to zero. A malformed slot, including a mismatched version with an invalid checksum,
does not qualify for reset. When a current-format slot is also valid, its totals take
precedence over mismatched-version slots. Other invalid data fails rather than producing
a misleading report.

Metrics writes use normal operating-system page-cache writeback and do not request a
per-invocation filesystem sync. This allows the operating system to coalesce physical
writes. Metrics persist across processes and normal restarts, but sudden power loss may
lose increments that the operating system had not yet flushed.

`hpatch gain` first writes six token rows: aggregate estimated effective hpatch output
tokens, estimated `apply_patch` output tokens, effective-only estimated reduction,
estimated ineffective hpatch output tokens, calculated total hpatch output tokens, and
estimated overall reduction. It then writes a stable-order compact table with one row per
supported patch command containing its invocations, errors, and calculated
`errors / invocations * 100` rate, followed by a total row across all commands. The
effective-only reduction is `(apply_patch - effective_hpatch) / apply_patch * 100`; the
overall reduction is `(apply_patch - total_hpatch) / apply_patch * 100`. Percentages are
rounded to one decimal place and are zero when their denominator is zero. With no metrics
file or only an obsolete record, all totals and percentages are zero. Gain reads no stdin
and does not create or rewrite a metrics file.

Acceptance:

1. Repeated successful normal and translate invocations persist cumulative paired
   estimates, failed invocations persist only ineffective hpatch estimates, and a later
   gain process reports both reductions plus per-command and aggregate invocation, error,
   and calculated error-rate metrics.
2. The hpatch estimate is the `functions.hpatch` tool name plus its free-form script;
   the direct estimate is the `functions.exec` tool name plus a program that passes the
   serialized patch envelope to `tools.apply_patch`.
3. Scripts and patches containing quotes or program-like text remain data and cannot
   alter the direct-call program used for counting.
4. Concurrent writers lose no records, and concurrent gain reads never observe a
   partial record.
5. The metrics file remains fixed-size, a damaged inactive slot falls back to the
   preceding valid aggregate, a valid mismatched `HPATCH` version resets totals when no
   current slot exists, malformed data does not count as a version mismatch, and a current
   slot takes precedence over mismatched versions.
6. Metrics collection failure warns without changing the success or failure of the
   requested edit or translated output.

## HP-SCRIPT-001: Script grammar

Blank lines are ignored. All other input is one command per line. Commands are:

```text
in PATH
new PATH
mv PATH
rm
sel LINE START:END
tsel LINE OCCURRENCE "JSON STRING"
bsel "START" "END"
rsel START:END
type "JSON STRING"
del
dup
```

Numbers are base-ten integers. Text lines, selection columns, and inclusive endpoints
are one-based. Cursor `0:0` denotes the position before the first code point of a file;
it is editor state, not a command operand. `OCCURRENCE` is nonzero: positive values
count exact, non-overlapping literal matches from the start; negative values count
them from the end. JSON operands use standard JSON string decoding. `type` and `bsel`
strings may contain encoded line terminators; `tsel` strings must stay within one
logical line. Both `bsel` strings must be nonempty and different.

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
2. File commands may be interleaved with text commands. File lifecycle commands
   observe preceding pending state, while selectors retain immutable baseline meaning.
3. Zero selection coordinates, malformed ranges, missing operands, invalid JSON,
   trailing operands, empty or identical block anchors, and unknown commands fail
   before filesystem mutation or patch output.
4. With root `/workspace` and cwd `bin/worktree`, script path `main.go` denotes
   `/workspace/bin/worktree/main.go` and translates as `bin/worktree/main.go`.

## HP-FILE-001: File commands

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

## HP-SELECT-001: Immutable baseline selections and cursor

Every existing logical file has one immutable baseline captured on first `in`. A new
file has an empty baseline. Every `sel`, `tsel`, `bsel`, and `rsel` command resolves
only against that baseline; recorded replacements, deletions, duplications, and
insertions never change selector text, line numbers, occurrences, scopes, or positions.
Text introduced by an earlier command is not selectable and cannot create an unrelated
literal or anchor collision. A selector whose baseline span overlaps content already
replaced or deleted by an earlier edit is rejected immediately with the prior command
and affected baseline lines identified.

Each active file has either a baseline cursor or a nonempty baseline selection. `in`
and `new` establish cursor `0:0`, before the first baseline code point. A cursor is an
insertion position; a selection is a half-open baseline span produced by the inclusive
commands below.

`sel` selects columns within one baseline logical line. Columns count Unicode code
points, including one code point per tab; the line terminator is not selectable.

`tsel` selects the requested exact literal occurrence within one baseline logical line.
The literal must be nonempty and cannot contain a line terminator. Matches do not
overlap. `-1` selects the last match.

`bsel` uses the current baseline selection as its search scope when a selection exists.
Otherwise its scope begins at the current baseline cursor and extends to baseline
end-of-file. It never wraps or falls back to the file beginning. Within that scope,
each decoded anchor must occur exactly once, counting overlapping occurrences for
ambiguity, and the end match must begin after the complete start match. It selects both
anchors and all baseline content between them. Missing, duplicate, reversed, or
overlapping anchors within the scope fail instead of selecting a nearby block.

`rsel` selects an inclusive range of complete baseline logical lines. For every
selected line it owns the line terminator when one is present. It records a linewise
selection so deletion removes complete lines and duplication creates another complete
adjacent line range, including when the selected final line has no terminator.

Every selection replaces the previous selection or cursor. After `type`, the cursor is
at the selected baseline span's end; an insertion at a cursor remains at that same
baseline position. `del` leaves the cursor at the selection start. `dup` leaves it at
the selection end and clears the selection because the inserted copy is not baseline
content.

Acceptance:

1. Unicode selectors constructed from the inspected pre-edit file retain the same
   meaning after earlier edits, independent of edit order and inserted line count.
2. Missing baseline lines, columns, ranges, literal occurrences, or block anchors fail
   rather than selecting pending or nearby content.
3. A `tsel` literal that merely overlaps but is not a non-overlapping requested
   occurrence does not count.
4. Any second baseline occurrence of a `bsel` anchor within its selection- or
   cursor-derived baseline scope makes the block ambiguous. Occurrences before or
   outside that baseline scope, and occurrences introduced by edits, do not collide.

## HP-EDIT-001: Baseline-coordinate edits

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

## HP-OUTPUT-001: Output and failure

Input is read completely and the entire script is evaluated before commit or stdout.
An unchanged normal-mode change set performs no filesystem operation. An unchanged
translate result emits no patch and fails because it cannot represent an update.

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

Normal mode stages new contents in same-directory temporary files before starting the
commit. Parse, validation, read, and evaluation failures leave the initial tree
unchanged. A staging failure attempts to remove all temporary artifacts; cleanup
failure returns nonzero and identifies every artifact it could not remove. Commit-time
filesystem failures trigger rollback attempts using staged backups. Ordinary
filesystems cannot provide a portable crash-atomic transaction over multiple paths:
termination, machine failure, or rollback failure during commit can leave a partial
change set. Such a failure must return nonzero and name the affected paths; it must
never report success or claim rollback succeeded when it did not.
Existing file permission bits are preserved; files created by `new` use mode `0644`.

OpenAI `apply_patch` is a logical-line format and cannot preserve CRLF or standalone-CR
bytes when its output is applied by the tool. Translate mode therefore emits LF-only
patch text and normalizes line endings only in its displayed before/after lines. It
does not modify source files. Normal mode continues to preserve existing line endings
outside explicitly inserted strings. Applying translated output to a non-LF file may
normalize that file to LF; this is a declared format limitation, not byte equivalence.

Failures emit one concise diagnostic to stderr, prefixed with `hpatch:`. Script
diagnostics identify the one-based nonblank command index, one-based source line,
operation, relevant operand or selected path when one exists, and a failure category
(`syntax`, `file`, `selection`, or `edit`). Control bytes in every diagnostic are escaped
and embedded newlines are folded so one failure remains one logical line. Failures
return nonzero and emit no stdout.

Acceptance:

1. Positive changes follow the mode-specific stdout behavior and translation action
   forms.
2. Malformed input, out-of-bounds selection, unrelated literal collision,
   unknown/future command, invalid UTF-8, missing/non-regular file, logical path
   collision, and staging failure produce no mutation and no patch output.
3. Injected commit and rollback failures are reported without false atomicity claims.

## HP-GUIDE-001: Agent guidance

The built-in top-level help owns the complete agent editing, orchestration,
trust-boundary, and validation reference. The project agent instruction file only
directs agents to run and follow `hpatch --help`; it does not duplicate language
semantics that can become stale.
