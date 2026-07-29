package hpatch

const toolDescription = `HPATCH/1 edits workspace files atomically. Submit one complete grammar-constrained script; rejection or cancellation changes nothing.
Do not call this tool in parallel with other tools.

Minimize the complete selector-plus-replacement output; a likely retry costs more than a few saved tokens:
- tsel FROM_LINE "TEXT" [N] selects the first N exact one-line matches from FROM_LINE; use it for a fragment or one replacement at multiple sites.
- bsel "START" "END" selects from distinct START through END inclusively; START must occur exactly once file-wide and END exactly once after it. It matches literals only; it does not parse syntax or pair braces. Use bsel for a multiline partial region whose outer text should remain, such as a body beneath a long function declaration.
- rsel START:END selects complete logical lines and their terminators; use it when every selected line should be re-emitted.
- sel LINE START:END selects inclusive one-based rune columns; use it only when verified columns are safer than content anchors.

Illustrative complete-call GPT-5 token estimates: preserving a long signature and braces costs bsel 71 versus rsel 84; a complete short block costs rsel 32 versus bsel 35; one expression costs tsel or sel 25 versus rsel 26; the same replacement at two sites costs tsel 25 versus rsel 30. Counts vary with paths and text. Prefer a stable anchor over a cheaper ambiguous selector.

Selection rules:
- Start tsel TEXT and bsel anchors at stable non-whitespace content. Omit leading spaces and tabs unless indentation is intentionally part of the edit or needed to disambiguate otherwise identical matches; copy any included text exactly from the baseline.
- bsel consumes both anchors. Re-emit any anchor that should remain. Never use a bare } or another duplicated fragment as an anchor. For a complete brace-delimited block, use fresh nl -ba output and rsel unless both anchors are distinctive and file-unique. Whole interior lines alone do not require rsel: bsel can avoid re-emitting substantial unchanged boundary text.
- For insertion-only edits, type only the new content; never repeat unchanged selected text in the type payload. To insert before a selected fragment, copy the selection, replace it with only new content, then paste the preserved selection:
  in handler.go
  tsel 40 "func handle(request Request) error {"
  copy
  type <<PATCH
  // Audit requests before handling.
  PATCH
  paste
- Use fresh nl -ba output for rsel or sel coordinates. Earlier edits do not shift baseline coordinates.
- Use type <<PATCH for multiline text and put PATCH immediately after the final content line; an extra blank body line changes the output.
- Use inline type when replacement text must not end with a newline.

Function-body example that preserves the declaration, parameters, opening brace, indentation before the first anchor, and closing brace:
  in service.go
  bsel "oldResult := compute(input)" "return oldResult, nil"
  type <<PATCH
  newResult := computeFresh(input)
      return newResult, nil
  PATCH

One-line fragment example that preserves indentation and surrounding text:
  in artifact.go
  tsel 90 "saveArtifactPayload(path, b)"
  type "saveArtifactPayloadAtomically(path, b)"

Commands:
- in PATH selects an existing UTF-8 file; new PATH selects a pending empty file.
- mv DESTINATION moves the active file; rm removes it.
- type "TEXT" replaces selections or inserts at the cursor. type <<PATCH supplies literal multiline text.
- del deletes selections; copy preserves and stores them; cut stores and deletes them; paste inserts the clipboard after selections or at the cursor.
- commit advances all live files to a new immutable baseline without writing the workspace.

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
TSEL_QUOTED: /"(?:\\(?:["\\\/t]|u(?:0009|00[2-9A-Fa-f][0-9A-Fa-f]|0[1-9A-Fa-f][0-9A-Fa-f]{2}|[1-9A-Fa-f][0-9A-Fa-f]{3}))|[^\x00-\x1F"\\]|\t)+"/
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
