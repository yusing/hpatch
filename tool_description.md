HPATCH/1 edits workspace files atomically. Submit one complete grammar-constrained script; rejection or cancellation changes nothing.
Do not call this tool in parallel with other tools.

Minimize model round trips: after inspecting every file required by the task, batch all known independent edits across files into one atomic script. Split calls only when a later edit depends on the preceding result or diagnostic.
Only `sel` and `rsel` require coordinates. When either is needed,
obtain every required coordinate for the current baseline from one `nl -ba -w1 -s'|'`
inspection; otherwise prefer stable text already established by inspection.

Minimize the complete selector-plus-replacement output; a likely retry costs more than a few saved tokens:
- tsel FROM_LINE "TEXT" [N] selects the first N separate exact matches from column 1 of FROM_LINE through EOF; matches may land on different lines. TEXT must stay on one line, need not fill it, and matching is not syntax-aware. If the suffix lacks N matches but the whole baseline has exactly N, the selection repairs to that unique set and the success report records the repaired line; extra whole-file matches keep the incomplete suffix a failure.
- rsel START:END selects complete logical lines and their terminators; use it when every selected line should be re-emitted.
- sel LINE START:END selects inclusive one-based rune columns; prefer it for one short occurrence when identical text repeats on the same line and verified coordinates are available, because tsel cannot target only a later same-line match.


Selection rules:
- Start tsel TEXT at stable non-whitespace content.
- Choose FROM_LINE so scanning starts in the intended region; do not default to line 1 when TEXT also occurs earlier. Prefer a broader TEXT instead of relying on whole-file repair of a wrong FROM_LINE.
- Before using short TEXT that may also occur in prose, links, examples, or repeated code, verify all occurrences (for example with rg -nF) and include distinguishing text such as ## for a Markdown heading.
- Use rsel for multiline regions. When only part of a boundary line changes, select the complete lines and reproduce the boundary content that should remain.
- For insertion-only edits, type only the new content; never repeat unchanged selected text in the type payload. To insert before a selected fragment, copy the selection, replace it with only new content, then paste the preserved selection:

```
in handler.go
tsel 40 "func handle(request Request) error {"
copy
type <<PATCH
// Audit requests before handling.
PATCH
paste
```

- For rsel and sel, use the line and rune coordinates from that baseline inspection. Earlier edits do not shift baseline coordinates before commit.
- commit materializes edits as a new baseline; post-commit selectors address that new content. No report is available until the whole call finishes, so same-call selectors must use coordinates known before submission. If uncertain, end the call and inspect the resulting baseline before editing again.
- Use type <<PATCH for multiline text and put PATCH immediately after the final content line; an extra blank body line changes the output.
- Use inline type when replacement text must not end with a newline.

Whole-function example using complete logical lines:

```
in service.go
rsel 12:15
type <<PATCH
func calculateResult(input Input) (Result, error) {
	return computeFreshResult(input), nil
}
PATCH
```

One-line fragment example that preserves indentation and surrounding text:

```
in artifact.go
tsel 90 "saveArtifactPayload(path, b)"
type "saveArtifactPayloadAtomically(path, b)"
```

Precision example after verifying that line 24 is "return ready || ready"; sel changes only the second identical occurrence:

```
in predicate.go
sel 24 17:21
type "cached"
```

Commands:
- in PATH selects an existing UTF-8 file; new PATH selects a pending empty file.
- mv DESTINATION moves the active file; rm removes it.
- type "TEXT" replaces selections or inserts at the cursor. type <<PATCH supplies literal multiline text.
- del deletes selections; copy preserves and stores them; cut stores and deletes them; paste inserts the clipboard after selections or at the cursor.
- Prefer cut plus paste to move a selection: cut combines copy and deletion in one command, and the script does not re-emit the selected text. For linewise section moves, include separator blank lines deliberately so the pasted heading does not join the preceding paragraph.
- paste inserts immediately after the selected span or at the cursor; it has no syntax or section awareness. Selecting a heading inserts before that heading's existing body, not after the section.
- commit advances all live files to a new immutable baseline without writing the workspace. Use it only when later commands must select text introduced or changed earlier in the same call, or must reuse a path after mv or rm; otherwise omit it.

Move-and-adjust example that does not re-emit the moved body; commit makes the pasted text selectable for the narrow follow-up edit:

```
in source.go
rsel 12:28
cut
in destination.go
rsel 40:40
paste
commit
tsel 41 "sourceRegistry"
type "destinationRegistry"
```

State and safety:
- The first in captures an immutable file baseline. All selectors in that generation use it; inserted text is not selectable.
- Returning with in resets cursor and selections but keeps pending edits. The clipboard survives file changes and commit.
- Disjoint edits may finalize together. Overlapping replacements, insertion inside a replacement, and multiple insertions at one offset reject atomically.
- Paths are workspace-relative and must remain inside the one routed root. Parents for new or moved files must already exist.
- A failed or corrected call is retried against the unchanged baseline.

After success, trust the reported edited ranges and any repaired tsel line notes; do not reread the file solely to verify placement. Run the formatter or parser/compiler, relevant tests, whitespace checks, and git diff --check.
