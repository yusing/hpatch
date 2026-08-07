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

A router-local tool plugin system loads TypeScript-authored, compiled JavaScript declarations
from the user configuration directory, exposes their OpenAI custom-tool specifications, and
translates model calls into ordinary Code Mode tool-call carriers. Common exec translations
use a router-owned wrapper, while tool implementations run only when Codex executes the
translated carrier under its normal sandbox and permissions. Gain reporting attributes installed
definitions, emitted-versus-translated output shapes, and current-versus-stock executor-result input
shapes to each contributed tool.

An installable `shell` plugin accepts one free-form script and exposes its normalized interpreter
and exact body in the translated Codex exec carrier. The executor supplies the body through an
anonymous script descriptor and preserves standard input for program data. It stores no
intermediate script file. A compact shebang selects the interpreter, while a missing shebang
selects `bash`. An optional `#!cmd=` directive wraps that canonical shell frontend command in
one user-supplied command template.

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
- Router-local tool plugins discovered from `hpatch/plugins` beneath the platform user
  configuration directory, with complete-registry startup validation.
- Model-visible custom-tool declarations using unconstrained string input or OpenAI-supported
  Lark and regex grammars, typed translation into Code Mode carriers, and executor-side tool
  implementations.
- Per-plugin and per-tool installed-definition, emitted-call, translated-carrier, current-result,
  and optional stock-result token estimates in `hpatch gain` and the router gain page.
- Built-in hread and hgrep stock results preserve returned content while omitting their verified
  `LINE:HASH` row identity, so gain reports the input cost of editable row references.
- A repository `plugins/shell.mjs` example with optional interpreter and command-template
  directives, plus a `make install` path that installs both binaries and configured plugins.

## Public surface

- `hpatch`: evaluate one complete script and atomically update the workspace.
- `hpatch translate`: evaluate the same script and emit its `apply_patch` representation.
- `hpatch gain`: report persistent edit-encoding and failure metrics.
- `hpatch-router --mode hpatch|passthrough`: expose routed hpatch, hgrep, and single- or
  multi-item hread treatment, or the unchanged control path.
- `hpatch/plugins` beneath the platform user configuration directory: the only tool-plugin
  discovery surface; the router has no plugin command-line flags.
- `shell`: an installable unconstrained custom tool whose translated exec carrier shows the
  interpreter and exact script body, optionally inside a `#!cmd=` command template.
- `make install`: install `hpatch`, `hpatch-router`, and repository plugin declarations.
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
- Interactive editor UI, binary files, non-UTF-8 files, remote plugin discovery, runtime
  TypeScript transpilation, hot plugin reload, or search semantics beyond the installed
  ripgrep executable.
- Bundling configured example plugins into the router, parsing the script body as shell syntax,
  storing the body in an intermediate file, overriding the executor working directory or
  environment, or printing script source into program output.
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
- In hpatch mode the router validates the complete discovered plugin registry before opening
  its listener or installing tool wrappers; any schema, identity, implementation, or wrapper
  mismatch reports diagnostics and stops startup without exposing a partial registry.
- Each executor-backed contributed tool uses a stable basename symlink beside `hpatch-router`.
  The stable symlink targets an authenticated snapshot wrapper with the same basename, and the
  snapshot wrapper targets `hpatch-router`. A translated exec carrier invokes only the basename;
  private child dispatch validates both links and selects the pinned implementation from the
  snapshot, so Codex remains the owner of working directory, sandbox, and permissions.
  One process-lifetime lock owns the frontend set. A restart can reclaim authenticated links
  after a crash, while a concurrent router fails startup.
- A plugin translator returns a normal Code Mode tool-call carrier rather than an exec-specific
  envelope. The plugin API may provide an exec wrapper that alone owns the repeated outer exec
  shape.

- A plugin executor returns its current result once and may include a stock result produced during
  that same execution. The stock result is metric evidence only and cannot change the current
  stdout, stderr, or exit status.
