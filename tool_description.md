HPATCH/1 edits workspace files atomically. Submit one complete grammar-constrained script; rejection or cancellation changes nothing.
Do not call this tool in parallel with other tools.

Use the smallest coherent batch whose selectors you independently verified from `hread` output or an
hpatch report. Batch known independent edits when their selectors and placement are certain, but
isolate uncertain or coordinate-dependent edits until their required result is available. Every
submitted script remains atomic. When a later edit depends on content or paths introduced earlier in
the same script, use `commit` to advance the immutable baseline only if its new selectors are already
known; otherwise finish, inspect, and submit a separate script. After rejection, prefer the router's
indexed correction operations over resending an unchanged complete script. Never reconstruct or guess
a selector `HASH`.

Minimize the complete selector-plus-replacement output; a likely retry costs more than a few saved tokens:
- tsel HASH "TEXT" [N] resolves HASH only when exactly one immutable-baseline logical line has it, then selects the first N separate exact matches from column 1 of that line through EOF. Matches may land on different lines. TEXT must stay on one line, need not fill it, and matching is not syntax-aware. If fewer than N matches exist at or after the resolved anchor, the command rejects and never searches before it.
- rsel START_HASH END_HASH resolves both hashes uniquely, then selects the inclusive complete logical lines and their terminators. Use it when every selected line should be re-emitted.
- Missing hashes, duplicate-content hashes, and truncated-hash collisions reject without guessing or repair context.

Selection rules:
- Start tsel TEXT at stable non-whitespace content and copy its HASH from hread.
- Choose an anchor whose resolved line starts the intended search region. Do not use an earlier convenient hash when TEXT also occurs before the intended target.
- Because tsel cannot target only a later same-line match, expand TEXT to distinguish the intended occurrence or replace the complete line.
- Before using short TEXT that may also occur in prose, links, examples, or repeated code, verify all occurrences (for example with rg -nF) and include distinguishing text such as ## for a Markdown heading.
- Use rsel for multiline regions. When only part of a boundary line changes, select the complete lines and reproduce the boundary content that should remain.
- For insertion-only edits, type only the new content; never repeat unchanged selected text in the type payload. To insert before a selected fragment, copy the selection, replace it with only new content, then paste the preserved selection:

```
in handler.go
tsel 3316 "func handle(request Request) error {"
copy
type <<PATCH
// Audit requests before handling.
PATCH
paste
```

- For every selector, use a hashline from the current baseline inspection. Earlier edits do not shift baseline identity before commit.
- commit materializes edits as a new baseline; post-commit selectors address that new content. No report is available until the whole call finishes, so same-call selectors must use coordinates known before submission. If uncertain, end the call and inspect the resulting baseline before editing again.
- A blank line immediately before PATCH is part of the literal replacement.
- Use inline type when replacement text must not end with a newline.

Whole-function example using complete logical lines:

```
in service.go
rsel 2ff7 d10b
type <<PATCH
func calculateResult(input Input) (Result, error) {
	return computeFreshResult(input), nil
}
PATCH
```

One-line fragment example that preserves indentation and surrounding text:

```
in artifact.go
tsel 6403 "saveArtifactPayload(path, b)"
type "saveArtifactPayloadAtomically(path, b)"
```

Precision example after verifying the complete line is "return ready || ready":

```
in predicate.go
tsel 9645 "return ready || ready"
type "return ready || cached"
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
rsel ffb0 4b7b
cut
in destination.go
rsel 12d9 12d9
paste
commit
tsel ffb0 "sourceRegistry"
type "destinationRegistry"
```

State and safety:
- The first in captures an immutable file baseline. Hashline selectors resolve exactly one baseline logical line; missing and ambiguous hashes reject without guessing. All selectors in that generation use it; inserted text is not selectable.
- Returning with in resets cursor and selections but keeps pending edits. The clipboard survives file changes and commit.
- Disjoint edits may finalize together. Overlapping replacements, insertion inside a replacement, and multiple insertions at one offset reject atomically.
- Changed `.go` files are parsed and formatted with Go's standard library before finalization; other languages receive no language validation.
- Paths are workspace-relative and must remain inside the one routed root. Parents for new or moved files must already exist.
- A failed or corrected call is retried against the unchanged baseline.

After success, trust the reported edited ranges and hash-only preview rows; do not reread the file solely to verify placement.
