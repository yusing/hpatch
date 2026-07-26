---
pjdoc:
  version: 1
  kind: architecture
  scope: root
  status: approved
  revision: "4"
  files:
    []
---
# hpatch architecture contract

## AC-CORE-001: Virtual workspace and baseline edit state

One engine owns script parsing, logical path resolution, first-touch order, per-file
immutable baselines, baseline cursor or selection state, recorded edits, conflict
validation, and net file actions for `HP-SCRIPT-001`, `HP-FILE-001`,
`HP-SELECT-001`, and `HP-EDIT-001`. It evaluates every command against one in-memory
virtual workspace. Normal and translate modes consume the same completed change set;
neither mode reimplements command semantics.

The engine obtains an original file only through a filesystem-loading function owned
by the workspace boundary. Before evaluation, that boundary converts every command
path to a clean root-relative identity. First load establishes the file's immutable
baseline. All selectors resolve against that baseline, and edit actions record baseline
spans rather than mutating selector state. The editor owner rejects selectors over
already modified spans, intersecting edit spans, insertions inside spans, and duplicate
insertion positions before it materializes ordered disjoint edits into final content.

The engine never writes files or output. A logical file retains its baseline identity
across moves so original-to-final actions can be derived without alias state.

## AC-BOUNDARY-001: Filesystem and output boundary

The CLI boundary owns arguments, stdin, workspace selection, diagnostics, stdout, and
exit status for `HP-CLI-001` and `HP-OUTPUT-001`. The workspace boundary owns a pinned
`*os.Root`, a root-relative cwd, root-scoped filesystem reads, staging, commit, and
rollback. Relative script paths resolve from cwd; absolute script paths are converted
to root-relative identities only when they are within root. Lexical and symlink escapes
fail. Initial inputs cross into the engine only after a regular-file check and strict
UTF-8 decoding. Informational argument forms are resolved at the outer process boundary
before stdin, working-directory, configuration-directory, metrics, or filesystem access.

The standalone CLI canonicalizes an absolute root, canonicalizes cwd beneath it, opens
the root once, and keeps that capability open for the invocation. Library callers pass
the already-authorized root capability, opened using its canonical absolute name, and a
root-relative cwd. Absolute script operands are matched against that canonical name;
equivalent alias spellings are not resolved outside the capability. Translation and
normal commit consume the same root-relative identities, so changing cwd changes which
file a relative script path denotes without changing the workspace boundary.

Normal mode stages the complete engine result before commit. Its transaction
coordinator owns backups, ordered path operations, rollback attempts, and honest
reporting of any commit or rollback failure. It must not expose a stronger atomicity
claim than the filesystem can provide.

## AC-TRANSLATE-001: Patch rendering

One translation renderer owns all OpenAI `apply_patch` syntax. It receives the engine's
ordered net change set and emits one envelope containing the required `Add File`,
`Update File`, `Move to`, and `Delete File` actions. It finishes the complete string
before stdout is written so evaluation or rendering failures cannot expose a partial
patch. Every emitted path is relative to the workspace root, independent of cwd. It
owns the minimal nonempty verification hunk required by OpenAI `apply_patch`
when a move has no content change, and the renderer-only LF normalization required by
the line-oriented output format. For changed content it expands context until every
bare hunk's old-side sequence is unique, failing instead of emitting an ambiguous
patch. The engine's normal-mode contents remain unchanged.

## AC-COMPARE-001: Independent comparison cases

The comparison artifact may call the engine as test support, but every equivalent
`apply_patch` input is independently authored scenario data. A clearly test-only patch
applier verifies both representations reach the same final path-to-content map before
token counts are reported. Neither the applier nor the comparison is part of the
installed `hpatch` CLI.
