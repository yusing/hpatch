package hpatch

const toolDescription = `HPATCH/1 edits workspace files atomically. Submit one complete grammar-constrained script; rejection or cancellation changes nothing.

Choose the first matching selector:
1. Complete logical lines or any indentation change: rsel START:END.
2. Exact existing non-whitespace content: tsel FROM_LINE "TEXT" [N].
3. A block whose boundary must start or end inside a line: bsel "START" "END".
4. Exact rune columns only when content selectors cannot express the target: sel LINE START:END.

Hard rules:
- Never include leading spaces or tabs in tsel TEXT or bsel anchors. Start at stable non-whitespace content.
- Never infer, normalize, or count indentation. Copy exact baseline content.
- Never use bsel when rsel can own the complete lines.
- Never place a physical newline inside a quoted operand. Encode inline line breaks as \n or \r.
- tsel TEXT and both bsel anchors must be nonempty. tsel cannot contain encoded line breaks.
- Use fresh nl -ba output for rsel or sel coordinates. Earlier edits do not shift baseline coordinates.
- Use type <<PATCH for multiline replacement text. PATCH must be the exact unindented closing line.
- Put PATCH immediately after the final content line. Do not add a blank body line unless the output should contain that blank line.
- Use inline type when replacement text must not end with a newline.

Selection examples:
Replace complete lines:
  in parser.go
  rsel 50:53
  type <<PATCH
  func parse() error {
  	return nil
  }
  PATCH

Replace content inside an indented line without selecting indentation:
  in artifact.go
  tsel 90 "return saveArtifactPayload(path, b)"
  type "return saveArtifactPayloadAtomically(path, b)"
Do not write tsel 90 "\t\treturn saveArtifactPayload(path, b)".

Replace a partial-line block only when complete-line selection does not fit:
  in expression.go
  bsel "oldCall(" "finalArgument)"
  type "newCall(firstArgument, finalArgument)"

Commands:
- in PATH selects an existing UTF-8 file; new PATH selects a pending empty file.
- mv DESTINATION moves the active file; rm removes it.
- type "TEXT" replaces selections or inserts at the cursor. type <<PATCH supplies literal multiline text.
- del deletes selections; copy preserves and stores them; cut stores and deletes them; paste inserts the clipboard after selections or at the cursor.
- commit advances all live files to a new immutable baseline without writing the workspace.

Selector semantics:
- tsel scans from FROM_LINE column 1 through EOF and selects the first N exact nonoverlapping matches; N defaults to 1 and all N must exist.
- bsel requires distinct START and END anchors. START must occur exactly once file-wide, and END exactly once after START; the selection includes both anchors.
- rsel selects inclusive complete logical lines and owns their terminators.
- sel uses inclusive one-based Unicode-rune columns; a tab counts as one rune.
- Prefer a longer exact content anchor over occurrence arithmetic.

State and safety:
- The first in captures an immutable file baseline. All selectors in that generation use it; inserted text is not selectable.
- Returning with in resets cursor and selections but keeps pending edits. The clipboard survives file changes and commit.
- Disjoint edits may finalize together. Overlapping replacements, insertion inside a replacement, and multiple insertions at one offset reject atomically.
- Paths are workspace-relative and must remain inside the one routed root. Parents for new or moved files must already exist.
- A failed or corrected call is retried against the unchanged baseline.

After success, inspect the reported edited ranges, run the formatter or parser, relevant tests, whitespace checks, and git diff --check.
`

const toolGrammar = `start: _blank_line* (script | corrections) _blank_line*

script: command (_separator command)*
corrections: correction (_separator correction)*

?command: path_command
        | bare_command
        | sel_command
        | tsel_command
        | bsel_command
        | rsel_command
        | type_command
        | heredoc_command

path_command: PATH_OP SP PATH
bare_command: "rm" | "del" | "copy" | "cut" | "paste" | "commit"
sel_command: "sel" SP POSINT SP POSINT ":" POSINT
tsel_command: "tsel" SP POSINT SP TSEL_QUOTED (SP POSINT)?
bsel_command: "bsel" SP NONEMPTY_QUOTED SP NONEMPTY_QUOTED
rsel_command: "rsel" SP POSINT ":" POSINT
type_command: "type" SP QUOTED
heredoc_command: "type" SP "<<PATCH" NL _patch_body "PATCH"

?correction: replacement
           | deletion
           | insertion_before
           | insertion_after

replacement: POSINT ":" SP? command
deletion: "-" POSINT
insertion_before: "+" POSINT ":" SP? command
insertion_after: POSINT "+:" SP? command

_separator: NL _blank_line*
_blank_line: HSPACE? NL
_patch_body: (PATCH_BODY_LINE | NL)*

PATH_OP: "in" | "new" | "mv"
POSINT: /[1-9][0-9]*/
SP: " "
HSPACE: /[ \t]+/
PATH: /[^\r\n]+/
NL: /\r?\n/
QUOTED: /"(?:\\(?:["\\\/bfnrt]|u[0-9A-Fa-f]{4})|[^\x00-\x1F"\\]|\t)*"/
NONEMPTY_QUOTED: /"(?:\\(?:["\\\/bfnrt]|u[0-9A-Fa-f]{4})|[^\x00-\x1F"\\]|\t)+"/
TSEL_QUOTED: /"(?:\\(?:["\\\/bft]|u(?:[1-9A-Fa-f][0-9A-Fa-f]{3}|0[1-9A-Fa-f][0-9A-Fa-f]{2}|00[1-9A-Fa-f][0-9A-Fa-f]|000[0-9BCEFbcef]))|[^\x00-\x1F"\\]|\t)+"/
PATCH_BODY_LINE: /(?:PATCH[^\r\n]+|PATC[^H\r\n][^\r\n]*|PAT[^C\r\n][^\r\n]*|PA[^T\r\n][^\r\n]*|P[^A\r\n][^\r\n]*|PATC|PAT|PA|P|[^P\r\n][^\r\n]*)(?:\r\n|\n)/
`

// ToolDescription returns the authoritative model guidance and examples.
func ToolDescription() string {
	return toolDescription
}

// ToolGrammar returns the authoritative Lark grammar for model-generated calls.
func ToolGrammar() string {
	return toolGrammar
}
