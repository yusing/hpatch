package hpatch

const toolDescription = `Edit workspace files atomically with one free-form script. Submit the complete
script in one call. A rejected script changes nothing.

Script format:
  Write one command per nonblank physical line; commands cannot continue.
  A literal newline always ends the current command. Quoted operands use JSON
  syntax. Encode allowed controls as \t, \n, or \r; never insert literal controls
  or newlines.

Commands:
  in PATH                              select an existing file baseline
  new PATH                             select a pending empty file
  mv PATH                              move the active pending file
  rm                                   remove the active file
  sel LINE START:END                   select inclusive one-based rune columns
  tsel LINE OCCURRENCE "TEXT" [N]      select TEXT on exactly LINE; single-line TEXT only
  bsel "START" "END"                   select a whole-file block; each anchor must be unique
  bsel_next "START" "END"              select a block in scope; each anchor must be unique
  rsel START:END                       select inclusive complete logical lines
  type "TEXT"                          replace the selection or insert at the cursor
  del                                  delete the selection
  copy                                 store the selected baseline text
  cut                                  store and delete the selected baseline text
  paste                                insert clipboard text after the selection or at the cursor

State and selectors:
  The first in for an existing file captures an immutable baseline. Every
  selector and edit for that file uses baseline coordinates even after earlier
  commands. Returning with in resets its cursor and selection but keeps recorded
  edits. mv preserves baseline identity. Text introduced by an edit is not selectable.
  The clipboard is local to one script, survives file changes, and may be pasted repeatedly.

  sel columns count Unicode code points, including tabs, and both endpoints are
  inclusive. tsel checks TEXT only on LINE; copy both from the same current numbered
  baseline read instead of guessing or reusing a stale line. Occurrences are nonzero:
  positive from the start and negative from the end. Optional N spans consecutive
  nonoverlapping matches. Prefer tsel or rsel when possible because a valid sel range
  may still target unintended text.

  bsel searches the whole baseline. bsel_next searches the active selection, or
  from the cursor to end of file without wrapping. START and END are independent:
  bsel does not choose the nearest END; each anchor must be independently unique
  in scope. Avoid short or punctuation-only whole-file anchors such as "}"; lengthen
  them, or select a unique outer block and then use bsel_next. Anchors must be nonempty and
  different. ASCII space and tab runs match interchangeably. A block includes both
  anchors.

Edits:
  type, bsel, and bsel_next JSON operands may encode line terminators with
  escapes such as \n. The escape stays on the command's physical line and decodes
  inside the operand. tsel TEXT must decode to exactly one logical line and must
  not contain \n or \r; use bsel for a multiline substring or rsel for complete
  lines. rsel owns line terminators: replacing a terminated selection without an
  explicit terminator preserves the existing LF, CRLF, or CR.
 del and cut remove selected logical lines completely.
  copy preserves the selection; cut leaves the cursor at its start. paste inserts
  after an active selection, or at the cursor otherwise, and then clears the selection.
  A linewise paste adds only missing destination boundary terminators.

  Disjoint edits commit together after the whole script validates. Overlapping
  replacements or deletions, insertions inside replacements, and multiple insertions
  at one baseline position are conflicts. Boundary insertions are allowed. A new
  file accepts one effective type; rm conflicts with recorded edits to an existing
  file.

Final-state report:
  Use workspace-relative paths within the workspace. Parent directories for new
  files must already exist. Success reports the active path and up to three nearby
  post-edit lines; use that report plus a parser or formatter to verify placement.
  A rejection reports repair context when available; retry against the unchanged
  baseline.
`

// ToolDescription returns the authoritative free-form tool instructions.
func ToolDescription() string {
	return toolDescription
}
