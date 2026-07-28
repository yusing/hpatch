package hpatch

const toolDescription = `Edit workspace files atomically with one free-form script. Submit the complete
script in one call. A rejected script changes nothing.

Script format:
  Outside a type heredoc, write one command per nonblank physical line;
  commands cannot continue. A literal newline always ends the current inline command.
  Only type <<TAG consumes following physical lines as one command.
  Inline quoted operands use JSON-compatible escapes and accept literal horizontal tabs.
  Escape quotes, backslashes, newlines, carriage returns, NUL, and other C0 controls.

Commands:
  in PATH                              select an existing file baseline
  new PATH                             select a pending empty file
  mv PATH                              move the active pending file
  rm                                   remove the active file
  sel LINE START:END                   select inclusive one-based rune columns
  tsel FROM_LINE "TEXT" [N]           select the first N separate TEXT matches from FROM_LINE
  bsel "START" "END"                   select a whole-file block; each anchor must be unique
  rsel START:END                       select inclusive complete logical lines
  type "TEXT"                          replace the selection or insert at the cursor
  type <<TAG                           insert or replace with a literal multiline body
  del                                  delete the selection
  copy                                 store the selected baseline text
  cut                                  store and delete the selected baseline text
  paste                                insert clipboard text after the selection or at the cursor
  commit                               advance to the next immutable baseline

State and selectors:
  The first in for an existing file captures an immutable baseline. Every
  selector and edit for that file uses baseline coordinates even after earlier
  commands. Returning with in resets its cursor and selection set but keeps recorded
  edits. mv preserves baseline identity. Text introduced by an edit is not selectable.
  The script-local clipboard survives file changes and may be pasted repeatedly.
  commit makes pending state the next immutable baseline without filesystem or output.
  It clears edits and resets an active cursor to 0:0; the clipboard survives. Script
  end finalizes edits without that reset.

  sel columns count Unicode code points, including tabs, and both endpoints are
  inclusive. tsel starts at column 1 of FROM_LINE and scans forward through EOF.
  N defaults to 1. tsel selects the first N exact matches; all N must exist. If
  fewer than N exist in the suffix but exactly N exist file-wide,
  tsel repairs FROM_LINE to the first; extras reject it.
  Prefer a broader TEXT over occurrence arithmetic. Prefer tsel or rsel when possible
  because a valid sel range may still target unintended text.

  bsel searches the whole baseline; it does not choose the nearest END. START and END
  are independent, and each anchor must be independently unique. Avoid short
  or punctuation-only anchors such as "}"; lengthen them. Anchors must be nonempty
  and different. ASCII space and tab runs match interchangeably. A block includes
  both anchors.

Edits:
  Inline type, tsel, and bsel operands use the quoted syntax above.
  type and bsel may encode line terminators with escapes such as \n.
  A heredoc TAG matches [A-Z][A-Z0-9_]{2,31}; its body is literal until a line exactly equal to TAG.
  Do not quote, indent, or suffix the closing TAG. Never put a physical newline
  inside a quoted operand. tsel TEXT must stay on one logical line; use a type heredoc
  only for multiline replacement text.
  rsel owns line terminators: replacing a terminated selection without an
  explicit terminator preserves the existing LF, CRLF, or CR.
  del and cut remove selected logical lines completely. type, del, cut, and paste
  apply to every active selection atomically. copy stores their shared TEXT once
  and preserves the selection set. paste inserts after every active selection, or
  at the cursor otherwise, and then clears the selection set. A linewise paste adds
  only missing destination boundary terminators.

  Within one generation, disjoint edits finalize together. Overlapping
  replacements or deletions, insertions inside replacements, and multiple insertions
  at one baseline position are conflicts. Boundary insertions are allowed. A new
  file accepts one effective type; rm conflicts with recorded edits to an existing
  file.

Final-state report:
  Use workspace-relative paths within the workspace. Parent directories for new
  files must already exist. Success reports the active path and up to three nearby
  post-edit lines. Multiple selections report their bounded envelope. Repairs add
  requested/resolved lines and up to three post-edit context lines. Use the report
  to verify placement.
  A rejection reports repair context when available; retry against the unchanged baseline.
`

// ToolDescription returns the authoritative free-form tool instructions.
func ToolDescription() string {
	return toolDescription
}
