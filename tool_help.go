package hpatch

const toolDescription = `Edit workspace files atomically with one free-form script. Submit the complete
script in one call. A rejected script changes nothing.

Commands:
  in PATH                              select an existing file baseline
  new PATH                             select a pending empty file
  mv PATH                              move the active pending file
  rm                                   remove the active file
  sel LINE START:END                   select inclusive one-based rune columns
  tsel LINE OCCURRENCE "TEXT" [N]      select matching text; N defaults to 1
  bsel "START" "END"                   select one unique whole-file block
  bsel_next "START" "END"              select one unique block in the current scope
  rsel START:END                       select inclusive complete logical lines
  type "TEXT"                          replace the selection or insert at the cursor
  del                                  delete the selection
  dup                                  duplicate the selected baseline text

State and selectors:
  The first in for an existing file captures an immutable baseline. Every
  selector and edit for that file uses baseline coordinates even after earlier
  commands. Returning with in resets its cursor and selection but keeps recorded
  edits. mv preserves baseline identity. Text introduced by an edit is not selectable.

  sel columns count Unicode code points, including tabs, and both endpoints are
  inclusive. tsel occurrences are nonzero: positive from the start, negative
  from the end. Its optional N is positive and spans consecutive nonoverlapping
  matches. Prefer tsel or rsel when possible because a valid sel range may still
  target unintended text.

  bsel searches the whole baseline. bsel_next searches the active selection, or
  from the cursor to end of file when there is no selection, and never wraps.
  Anchors must be nonempty and different. Each anchor must resolve uniquely in
  its scope; if exact matching fails, nonempty ASCII space and tab runs are
  interchangeable. A block includes both anchors.

Edits:
  type, bsel, and bsel_next operands are JSON strings and may encode line
  terminators; tsel text may not. rsel owns line terminators: replacing a
  terminated selection without an explicit terminator preserves the existing
  LF, CRLF, or CR. del removes selected logical lines completely.

  Disjoint edits commit together after the whole script validates. Overlapping
  replacements or deletions, insertions inside replacements, and multiple insertions
  at one baseline position are conflicts. Boundary insertions are allowed. A new
  file accepts one effective type; rm conflicts with recorded edits to an existing
  file.

Final-state report:
  Use workspace-relative paths within the workspace. Parent directories for new
  files must already exist. Success reports the active path and up to three
  nearby post-edit lines; use that report plus a parser or formatter to verify
  placement. A rejection reports repair context when available; retry against
  the unchanged baseline.
`

// ToolDescription returns the authoritative free-form tool instructions.
func ToolDescription() string {
	return toolDescription
}
