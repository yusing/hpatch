# hpatch brief

## Problem

Agents describe edits with line-oriented diffs that repeat old content and unchanged
context. HPATCH/1 reduces that repetition, but its separate selector, cursor, clipboard,
and generation state makes simple mutations multi-command programs. A failed selector or
state precondition discards the complete atomic script and can consume more output and
model turns than the successful encoding saves.

## Outcome

Provide an atomic edit tool whose mutation commands carry compact, verified targets.
The agent emits replacement or inserted content once; hpatch resolves the target against
an immutable invocation baseline and constructs the ordinary `apply_patch` representation
internally. A routed reader emits copyable line-and-content references that disambiguate
repeated lines, detect stale inspection, and read several already-known files or ranges in
one ordered call without requiring old regions to be re-emitted. A routed ripgrep wrapper
emits those same verified references for complete matching and requested context lines so an
agent does not need to repeat an exact search result through the reader before editing it.

The historical benchmark remains the end-to-end authority: correctness must match the
native edit path, output tokens must be lower, and input, reasoning, request count, and
wall time must remain close to control.

## First-draft scope

- Multiple UTF-8 files opened in sequence by `in PATH` or created by `new PATH`.
- One routed hread call may contain an ordered newline-delimited batch of those existing
  single-file read specifications.
- Routed hgrep searches with familiar ripgrep matching, context, and file-selection arguments
  and emits complete UTF-8 result rows as copyable path-and-`LINE:HASH` results.
- Mutation-owned complete-line, inclusive line-range, and anchored literal targets.
- Replacement, insertion immediately before or after a target, and deletion without a
  separate selection, cursor, or clipboard protocol.
- Optional positive multiplicity for repeated anchored literal mutations.
- File creation, movement, and deletion.
- One immutable baseline per touched existing file for the complete script; overlapping
  mutations reject instead of rebasing or guessing.
- Normal mode that validates and stages the complete change set before filesystem commit.
- Translate mode that does not modify files and emits one OpenAI `apply_patch` envelope.
- Compact indexed corrections of rejected scripts without resending unaffected commands.
- Persistent encoding, diagnostic, command, target, and end-to-end benchmark metrics.
- Historical-commit benchmark tasks with hidden graders, paired randomized attempts, and
  structured artifacts.

## Public surface

- `hpatch`: evaluate one complete script and atomically update the workspace.
- `hpatch translate`: evaluate the same script and emit its `apply_patch` representation.
- `hpatch gain`: report persistent edit-encoding and failure metrics.
- `hpatch-router --mode hpatch|passthrough`: expose routed hpatch, hgrep, and single- or
  multi-item hread treatment, or the unchanged control path.
- `hpatch-bench validate --manifest TASK.json` and `hpatch-bench run`: validate and run
  paired historical-commit evaluations.
- `hpatch --help`, `hpatch --tool-help`, `hpatch translate --help`, and
  `hpatch --version`: informational output.
- Script commands: `in`, `new`, `mv`, `rm`, `type`, `type-`, and `type+`.
- `type` replaces its explicit target; an empty target-bearing value deletes the target,
  including an owned line or range terminator. `type-` and `type+` insert before and after
  their explicit target while preserving it.
- Immediately after `new`, targetless `type` may initialize the empty file once.
- A target is a copyable hread row, an inclusive pair of rows, or a row-anchored literal
  with optional multiplicity, as specified by `REQ-SCRIPT-001`.

## Non-goals

- Compatibility aliases or legacy support for `tsel`, `rsel`, `copy`, `cut`, `paste`, or
  script-level `commit`.
- A persistent selection, cursor, clipboard, mutable shadow buffer, undo history, or
  resume protocol.
- Content movement without re-emitting the moved content; `mv` moves complete files only.
- Selecting content introduced earlier in the same script. Dependent edits use a later
  inspected invocation.
- Interactive editor UI, plugins, binary files, non-UTF-8 files, or search semantics beyond
  the installed ripgrep executable.
- A new patch interchange format beyond the compact command script and translated
  `apply_patch` output.
- AST-specific mutation commands or language-specific editing frameworks.
- Remote dataset discovery, hosted benchmark orchestration, exact-reference-patch grading,
  or automatic cost conversion.

## Constraints

- The HPATCH/2 grammar replaces HPATCH/1; compatibility is not required.
- A routed row reference combines a positive one-based logical line with a lowercase
  four-digit content hash. The line disambiguates repeated content; the hash verifies the
  complete logical-line bytes, including indentation and excluding its terminator.
- Routed hgrep accepts familiar ripgrep search arguments, invokes the installed `rg` with
  internal `--json --no-config` transport, and exposes only complete current logical lines
  with the same routed row identity. Shell syntax in an argument is data, never execution.
- A target must resolve against the active file's immutable invocation baseline. Missing,
  stale, reversed, incomplete, and overlapping targets reject the complete script.
- Inline strings use compact JSON-compatible quoting with literal horizontal tabs;
  multiline values use the grammar-constrained `<<PATCH` frame.
- Parsing, target resolution, validation, and in-memory evaluation failures must not
  modify files or emit a partial patch or successful final-state report.
- Normal success writes the final-state report to stderr. Translate success writes the
  patch to stdout and the pending final-state report to stderr.
- Changed Go files are parsed and formatted with Go's standard library before success.
- Correctness is determined by required graders and path-scope checks, not reference-patch
  similarity. End-to-end Responses usage is authoritative for task-level token results.
- Diagnostics use stderr and a nonzero exit status for standalone CLI failures.
