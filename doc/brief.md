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
- Cursor insertion, selection replacement, selection deletion, and duplication.
- File creation, movement, and deletion.
- Immutable per-file baselines with stable selectors, disjoint edit collection, and
  explicit conflict rejection.
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
  `rsel`, `type`, `del`, and `dup`.

## Non-goals

- Interactive editor UI, undo history, file discovery, configuration, or plugins.
- Binary or non-UTF-8 files.
- A new diff or patch interchange format beyond the command script and translated
  `apply_patch` output.
- Compatibility aliases for commands or invocation modes.

## Constraints

- Lines, columns, and inclusive endpoints are one-based.
- Columns count Unicode code points; a tab counts as one code point.
- String operands use JSON string syntax.
- Parsing, validation, and in-memory evaluation failures must not modify files or emit
  a partial patch.
- Normal-mode success writes only the final-state report to stderr. Translate-mode
  success writes only the patch to stdout and the pending final-state report to stderr.
- Diagnostics go to standard error with a nonzero exit status.
