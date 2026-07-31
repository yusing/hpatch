# hpatch brief

## Problem

Agents currently describe small edits with line-oriented diffs, repeating unchanged
context and existing text. That is token-heavy and makes repeated or duplicated edits
awkward compared with selecting text in an editor and acting on the selection. Numeric
line references can also drift after another edit, causing a syntactically valid selector
to target different content.

## Outcome

Provide a small command-line tool that reads a compact, selection-oriented edit script
from standard input. The same script can update, create, move, or remove files, or
translate those changes into OpenAI `apply_patch` format. A router-exposed read tool emits
stable hashline references for constructing selectors. Include measured GPT-5 token
comparisons, a concise instruction sheet suitable for agents, and a reproducible
historical-commit benchmark that measures hpatch against the native edit path by
executable correctness.

## First-draft scope

- Multiple UTF-8 files selected in sequence by `in PATH` or created by `new PATH`.
- Forward literal-match selection sets and inclusive whole-line range selection.
- Cursor insertion and atomic selection-set replacement, deletion, and invocation-local
  clipboard copy, cut, and paste.
- File creation, movement, and deletion.
- Immutable per-generation baselines with hash-only line identities, disjoint edit
  collection within a generation, rejection instead of guessing when identity is missing
  or ambiguous, and an explicit `commit` barrier that advances the in-memory baseline.
- Normal mode that validates and stages the complete change set before committing it.
- Translate mode that does not modify files and emits one `apply_patch` envelope.
- Historical-commit benchmark tasks that prove an evaluator fails on the parent revision,
  passes on the oracle revision, then run paired randomized Codex attempts against
  history-free snapshots with hidden graders and structured artifacts.
- A router pass-through control that shares the hpatch router's provider and usage path
  without rewriting the model's edit tool.

- Automated scenarios comparing hpatch scripts with equivalent handwritten
  `apply_patch` inputs using the tokenizer returned for the OpenAI `gpt-5` model.

## Public surface

- `hpatch`: normal mode; read the script from standard input and edit files.
- `hpatch translate`: read the script from standard input and emit `apply_patch` text.
- `hpatch gain`: report persistent comparative token estimates.
- `hpatch-bench validate --manifest TASK.json`: prove the task's base/oracle grader
  discrimination before model execution.
- `hpatch-bench run`: validate and run paired control/hpatch attempts through separately
  labeled router endpoints, writing JSONL results and diagnostic artifacts.
- `hpatch-router --mode hpatch|passthrough`: select edit-tool rewriting or the unchanged
  control path; hpatch mode exposes the `hread` hashline reader beside the editor, while
  omitted mode retains hpatch behavior.

- `hpatch --help`, `hpatch --tool-help`, `hpatch translate --help`, and
  `hpatch --version`: informational output without reading stdin or accessing the
  workspace.
- Script commands: `in`, `new`, `mv`, `rm`, `tsel`, `rsel`, `type`,
  `del`, `copy`, `cut`, `paste`, and `commit`; `type` also accepts a framed heredoc body.
- Routed rejected-script corrections can replace, accept displayed safe corrections for,
  delete, or insert commands by command index without resending the complete script.

## Non-goals

- Interactive editor UI, undo history, file discovery, configuration, or plugins.
- Binary or non-UTF-8 files.
- A new diff or patch interchange format beyond the command script and translated
  `apply_patch` output.
- Compatibility aliases for commands or invocation modes.
- Remote repository cloning, benchmark dataset discovery, hosted orchestration, parallel
  model execution, exact-reference-patch grading, or automatic cost conversion.

- Implicit selector rebasing after every edit; only explicit `commit` advances a baseline.
- Verbose object-per-command encoding on the agent-facing edit path.

## Constraints

- Lines, columns, and inclusive endpoints are one-based.
- A hashline is `HHHH: TEXT`; `HHHH` is the first two SHA-256 bytes of exact
  logical-line content, excluding its terminator, rendered as lowercase hexadecimal.

- Columns count Unicode code points; a tab counts as one code point.
- Inline string operands use compact JSON-compatible quoting that also accepts literal tabs;
  multiline `type` content uses an explicit heredoc frame.
- Parsing, validation, and in-memory evaluation failures must not modify files or emit
  a partial patch.
- Normal-mode success writes only the final-state report to stderr. Translate-mode
  success writes only the patch to stdout and the pending final-state report to stderr.
- A benchmark manifest names a local source repository, a base and oracle commit, a prompt,
  hidden files, grader commands, allowed path prefixes, and finite agent/grader timeouts.
- The agent receives neither source history nor hidden graders. Correctness is determined
  after execution from required graders and scope checks, not reference-patch similarity.
- Control and treatment use distinct router endpoints that report `passthrough` and
  `hpatch` mode respectively.

- Diagnostics go to standard error with a nonzero exit status.
