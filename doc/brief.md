# hpatch brief

## Problem

Agents currently describe small edits with line-oriented diffs, repeating unchanged
context and existing text. That is token-heavy and makes repeated or duplicated edits
awkward compared with selecting text in an editor and acting on the selection.

## Outcome

Provide a small command-line tool that reads a compact, selection-oriented edit script
from standard input. The same script can update, create, move, or remove files, or
translate those changes into OpenAI `apply_patch` format. Include measured GPT-5 token
comparisons and a concise instruction sheet suitable for agents.

## First-draft scope

- Multiple UTF-8 files selected in sequence by `in PATH` or created by `new PATH`.
- Single-line column selection, literal text-occurrence selection, unique literal-anchored
  block selection, and inclusive whole-line range selection.
- Cursor insertion, selection replacement, selection deletion, and invocation-local clipboard
  copy, cut, and paste.
- File creation, movement, and deletion.
- Immutable per-generation baselines with stable selectors, disjoint edit collection within
  a generation, and an explicit `commit` barrier that advances the in-memory baseline.
- Normal mode that validates and stages the complete change set before committing it.
- Translate mode that does not modify files and emits one `apply_patch` envelope.
- Automated scenarios comparing hpatch scripts with equivalent handwritten
  `apply_patch` inputs using the tokenizer returned for the OpenAI `gpt-5` model.

## Public surface

- `hpatch`: normal mode; read the script from standard input and edit files.
- `hpatch translate`: read the script from standard input and emit `apply_patch` text.
- `hpatch gain`: report persistent comparative token estimates.
- `hpatch --help`, `hpatch --tool-help`, `hpatch translate --help`, and
  `hpatch --version`: informational output without reading stdin or accessing the
  workspace.
- Script commands: `in`, `new`, `mv`, `rm`, `sel`, `tsel`, `bsel`, `bsel_next`,
  `rsel`, `type`, `del`, `copy`, `cut`, `paste`, and `commit`; `type` also accepts a
  framed heredoc body.
- Routed rejected-script corrections can replace, delete, or insert commands by command
  index without resending the complete script.

## Non-goals

- Interactive editor UI, undo history, file discovery, configuration, or plugins.
- Binary or non-UTF-8 files.
- A new diff or patch interchange format beyond the command script and translated
  `apply_patch` output.
- Compatibility aliases for commands or invocation modes.
- Implicit selector rebasing after every edit; only explicit `commit` advances a baseline.
- Verbose object-per-command encoding on the agent-facing edit path.

## Constraints

- Lines, columns, and inclusive endpoints are one-based.
- Columns count Unicode code points; a tab counts as one code point.
- Inline string operands use compact JSON-compatible quoting that also accepts literal tabs;
  multiline `type` content uses an explicit heredoc frame.
- Parsing, validation, and in-memory evaluation failures must not modify files or emit
  a partial patch.
- Normal-mode success writes only the final-state report to stderr. Translate-mode
  success writes only the patch to stdout and the pending final-state report to stderr.
- Diagnostics go to standard error with a nonzero exit status.
