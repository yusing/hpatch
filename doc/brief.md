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
A successful edit report projects compact current rows for each effective content command.
Those reported references can be used directly in the next invocation after line shifts and
language formatting. A focused routed read remains necessary when the report does not contain
the exact next target.

A router-local tool plugin system loads TypeScript-authored, compiled JavaScript declarations
from the user configuration directory, exposes their OpenAI custom-tool specifications, and
translates model calls into ordinary Code Mode tool-call carriers. Common exec translations
use a router-owned wrapper, while tool implementations run only when Codex executes the
translated carrier under its normal sandbox and permissions. Structured router metrics attribute
installed definitions, emitted-versus-translated output shapes, and current-versus-stock executor-result input
shapes to each contributed tool.

The mandatory built-in `shell` plugin accepts one free-form script and exposes its normalized interpreter
and exact body in the translated Codex exec carrier. The executor preserves standard input for
program data. A compact shebang selects the interpreter, while a missing shebang selects `bash`.
Optional directives use one `#!key=value` syntax: `#!cmd=` wraps that canonical shell frontend
command in one user-supplied command template, while `#!params=` forwards a JSON object, except
`cmd`, through the typed exec carrier. A present `login` value must be `false`. The router always
rewrites the custom `exec` tool either directly inside an `additional_tools` item for app-server
traffic or inside that item's `functions` namespace for CLI traffic. It removes that owner's
`exec_command` section and introductory `tools.exec_command` example, derives the request-specific
parameter shape, omits `cmd`, and appends only that sanitized shape to `shell`.
Direct `functions.exec` and top-level exec carriers remain unsupported.

The exec carrier preserves the native executor result when a command yields instead of treating
the initial call as terminal. Codex remains the sole owner of the yielded session and its native
continuation operation; the router and plugins do not poll, resume, cancel, retry, replace, or
persist that session.

The historical benchmark remains the end-to-end authority: correctness must match the
native edit path, output tokens must be lower, and input, reasoning, request count, and
wall time must remain close to control.

## First-draft scope

- Multiple UTF-8 files opened in sequence by `in PATH` or created by `new PATH`.
- Private hread commands accept one existing file and an optional range; shell scripts batch reads
  as separate hread commands.
- Private hgrep commands accept familiar ripgrep matching, context, and file-selection arguments
  and emit complete UTF-8 result rows as copyable path-and-`LINE:HASH` results.
- Mutation-owned complete-line, inclusive line-range, and anchored literal targets.
- Replacement, insertion immediately before or after a target, and deletion without a
  separate selection, cursor, or clipboard protocol.
- Optional positive multiplicity for repeated anchored literal mutations.
- File creation, movement, and deletion.
- One immutable baseline per touched existing file for the complete script; overlapping
  mutations reject instead of rebasing or guessing.
- Basic `Apply` validates, stages, and commits a complete change set, returning only an error.
- Basic `Translate` does not modify files and returns only the OpenAI `apply_patch` bytes or an error.
- `ApplyForHost`, `ApplyForHostRoot`, `TranslateForHost`, and `TranslateForHostAt` return
  `HostTranslation` with the rendered report, final state, diagnostics, and evaluator metrics.
- Router-only target correction through `functions.hpatch_recover` with hashed command handles; an unchanged target rejects before reevaluation, and ordinary `functions.hpatch` and root APIs have no recovery mode.
- Persistent encoding, diagnostic, command, target, and end-to-end benchmark metrics.
- Historical-commit benchmark tasks with hidden graders, paired randomized attempts, and
  structured artifacts.
- Router-local tool plugins discovered from `hpatch/plugins` beneath the platform user
  configuration directory, with complete-registry startup validation.
- Model-visible custom-tool declarations using unconstrained string input or OpenAI-supported
  Lark and regex grammars, typed translation into Code Mode carriers, and executor-side tool
  implementations.
- Per-plugin and per-tool installed-definition, emitted-call, validated stock-carrier, current-result,
  and optional stock-result token estimates in the router dashboard and structured metrics endpoint.
- Built-in hread and hgrep stock results preserve returned content while omitting their verified
  `LINE:HASH` row identity, so structured router metrics report the input cost of editable row references.
- The repository `plugins/shell.mjs` source is embedded as the mandatory built-in shell, with
  optional interpreter, command-template, JSON parameter directives, and an interpreter-specific
  native exec baseline for output metrics.

## Public surface

- Basic root Go APIs: `Apply` atomically updates an authorized workspace and returns an error;
  `Translate` returns `apply_patch` bytes and an error.
- Host root APIs: `ApplyForHost`, `ApplyForHostRoot`, `TranslateForHost`, and
  `TranslateForHostAt` return `HostTranslation` for report, state, diagnostics, and metrics.
- `hpatch-router --mode hpatch|passthrough`: expose model-visible hpatch and shell tools with
  private single-file hread and hgrep commands, or the unchanged control path.
- `hpatch/plugins` beneath the platform user configuration directory: the configured tool-plugin
  discovery surface; the router has no plugin command-line flags.
- `shell`: a mandatory built-in unconstrained custom tool whose translated exec carrier shows the
  interpreter and exact script body, optionally with `#!cmd=` and request-specific `#!params=`
  assignments.
- `make install`: regenerate the embedded plugin bundle, install `hpatch-router`, and install
  Codex instructions.
- `hpatch-bench validate --manifest TASK.json` and `hpatch-bench run`: validate and run
  paired historical-commit evaluations.
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
- A guarantee that successful report references cover every possible later target, or retention
  of prior routed-read rows so the engine can predict a later edit.
- Word diffs, translated-patch retention in model-visible history, and caller-selected final
  report ranges.
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
- A routed row reference combines a positive one-based logical-line hint with a lowercase
  four-digit content hash. The hash verifies complete logical-line bytes, including
  indentation and excluding its terminator. A shifted row resolves only when the hash is unique.
- Routed hgrep accepts familiar ripgrep search arguments, invokes the installed `rg` with
  internal `--json --no-config` transport, and exposes only complete current logical lines
  with the same routed row identity. Shell syntax in an argument is data, never execution.
- A target must resolve against the active file's immutable invocation baseline. Missing,
  stale, reversed, incomplete, and overlapping targets reject the complete script.
- Inline strings use compact JSON-compatible quoting with literal horizontal tabs;
  multiline values use the grammar-constrained `<<PATCH` frame.
- Parsing, target resolution, validation, and in-memory evaluation failures must not
  modify files or emit a partial patch or successful final-state report.
- Basic `Apply` returns only an error and basic `Translate` returns only patch bytes and an
  error. Host variants return completed state, the rendered report, diagnostics, and evaluator
  metrics through `HostTranslation`.
- A successful report row is current for its named final path and may target the next
  invocation directly. Saved pre-edit rows remain stale; when the exact next target is absent,
  the caller performs a focused read rather than guessing or reconstructing it.
- Final-reference projection derives from completed editor state and formatting offsets without
  retaining another copy of original or final file content.
- Changed Go files are parsed and formatted with Go's standard library before success.
- Correctness is determined by required graders and path-scope checks, not reference-patch
  similarity. End-to-end Responses usage is authoritative for task-level token results.
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
