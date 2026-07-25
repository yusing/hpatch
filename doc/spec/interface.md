# Interface contract

## HP-CLI-001: Modes

`hpatch` reads a complete script from standard input, evaluates its complete change set
in memory, stages required filesystem content, and then commits it. It writes nothing
on success.

`hpatch translate` performs the same parsing, filesystem reads, and in-memory
evaluation but never modifies a file. It writes one OpenAI `apply_patch` envelope that
represents the same net change set.

Any other argument list is invalid.

Acceptance:

1. Given valid edits spanning multiple files, normal mode produces the specified final
   paths and contents with empty stdout and stderr.
2. For LF inputs, applying translate mode's stdout produces the same final paths and
   UTF-8 contents as normal mode. For other line endings, it represents the same
   logical-line edits subject to the normalization rule in `HP-OUTPUT-001`.
3. Translate mode leaves the source tree unchanged.

## HP-SCRIPT-001: Script grammar

Blank lines are ignored. All other input is one command per line. Commands are:

```text
in PATH
new PATH
mv PATH
rm
sel LINE START:END
tsel LINE OCCURRENCE "JSON STRING"
rsel START:END
type "JSON STRING"
del
dup
```

Numbers are base-ten integers. Text lines, selection columns, and inclusive endpoints
are one-based. Cursor `0:0` denotes the position before the first code point of a file;
it is editor state, not a command operand. `OCCURRENCE` is nonzero: positive values
count exact, non-overlapping literal matches from the start; negative values count
them from the end. JSON operands use standard JSON string decoding. Paths are nonempty
filesystem paths normalized with the host OS path rules. Relative paths resolve from
the process working directory; absolute paths remain absolute. Trailing operands and
unknown commands are invalid.

Acceptance:

1. JSON escapes allow spaces, quotes, tabs, and newlines in inserted text.
2. File commands may be interleaved with text commands and every command observes all
   preceding in-memory changes.
3. Zero selection coordinates, malformed ranges, missing operands, trailing operands,
   and unknown commands fail before filesystem mutation or patch output.

## HP-FILE-001: File commands

`in PATH` selects an existing logical file for subsequent commands. It may occur any
number of times. Selecting a file clears the prior text selection and places its cursor
at `0:0`. Returning to a previously selected path exposes pending edits already made to
that file in this script.

`new PATH` creates and selects a pending empty file at cursor `0:0`. It fails if the
logical path already exists. A created file does not reach the filesystem unless the
complete script evaluates and commits successfully.

`mv PATH` moves the active logical file to an unoccupied logical path. The destination
becomes active and preserves the current cursor or selection. Pending edits move with
the file. Later `in` commands resolve the new path, not the old one. Multiple moves of
one original file collapse to its original-to-final move in the net change set.

`rm` marks the active logical file as deleted, then clears the active file, cursor, and
selection. Later text or lifecycle operations require another `in` or `new`. Deleting
a file created in the same script cancels that creation. Deleting an original file
supersedes its pending content edits and moves and yields deletion of its original
path.

`in` fails for a missing or deleted logical path. `new` and `mv` fail on any logical
destination collision, including a path occupied in the initial tree or by pending
state. `mv`, `rm`, selection commands, and edit commands fail without an active file.
Initial files must resolve to regular UTF-8 files when first selected.
The parent directory of a `new` or `mv` destination must already exist; hpatch does not
create directories that are absent from the script's file-operation model.

Acceptance:

1. A script can edit multiple files by switching among them, and reselecting a path
   retains its pending content.
2. `new`, consecutive `type` commands, `mv`, and `rm` produce correct net additions,
   moves, and deletions.
3. Missing paths, destination collisions, use-after-delete, and file
   commands without an active file fail before commit or patch output.

## HP-SELECT-001: Selections and cursor

Each active file has either a cursor or a nonempty selection. `in` and `new` establish
cursor `0:0`, before the first code point. A cursor is an insertion point; a selection
is a half-open internal span produced by the inclusive commands below.

`sel` selects columns within one current logical line. Columns count Unicode code
points, including one code point per tab; the line terminator is not selectable.

`tsel` selects the requested exact literal occurrence within one current logical line.
The literal must be nonempty and cannot contain a line terminator. Matches do not
overlap. `-1` selects the last match.

`rsel` selects an inclusive range of complete current logical lines. It records a
linewise selection so duplication creates another complete adjacent line range,
including when the selected final line has no terminator.

Every selection replaces the previous cursor or selection. Commands observe current
contents and current line numbering after earlier edit actions.

Acceptance:

1. Selection works on Unicode text and current line numbering after earlier edits.
2. Missing lines, columns, ranges, or literal occurrences fail rather than selecting
   a nearby value.
3. A literal that merely overlaps but is not a non-overlapping requested occurrence
   does not count.

## HP-EDIT-001: Edit actions

`type STRING` inserts the decoded string at the cursor, or replaces the selection with
it. It then leaves a cursor immediately after the inserted text. Thus these commands
on a new file produce `foo bar`:

```text
type "foo"
type " "
type "bar"
```

`del` requires a selection, removes it, and leaves a cursor at its start.

`dup` requires a selection and inserts a copy immediately after it. For characterwise
selections the copy is exact. For linewise selections, the duplicate is an adjacent
complete range; if the original range ends at an unterminated final line, the file's
existing line-ending style is inserted between copies and the duplicate remains
unterminated. The duplicated copy becomes the current selection.

Existing line-ending bytes outside explicitly inserted text are preserved. When
linewise duplication needs a separator, the first existing line terminator is used,
or LF if the file has none.

Acceptance:

1. Consecutive typing, replacement, deletion, character duplication, and complete
   line-range duplication produce the specified text and editor state.
2. A later selection sees text and line-number changes from earlier actions.
3. CRLF input remains CRLF except where an explicit JSON string inserts another
   sequence.

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

Failures emit one concise diagnostic to stderr, prefixed with `hpatch:` and, for script
errors, the script line number. They return nonzero and emit no stdout.

Acceptance:

1. Positive changes follow the mode-specific stdout behavior and translation action
   forms.
2. Malformed input, out-of-bounds selection, unrelated literal collision,
   unknown/future command, invalid UTF-8, missing/non-regular file, logical path
   collision, and staging failure produce no mutation and no patch output.
3. Injected commit and rollback failures are reported without false atomicity claims.

## HP-GUIDE-001: Agent guidance

The project provides one concise agent instruction file covering both modes, the full
command grammar, cursor and coordinate rules, sequential multi-file state, and the need
to inspect relevant source before selecting it. Examples demonstrate consecutive
typing into a new file, replacement, occurrence deletion, line duplication, movement,
and removal without adding aliases or commands.
