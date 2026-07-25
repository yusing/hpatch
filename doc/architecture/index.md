# hpatch architecture contract

Status: accepted draft  
Revision: 2

## AC-CORE-001: Virtual workspace and editor state

One engine owns script parsing, logical path resolution, first-touch order, per-file
original and pending contents, cursor or selection state, and net file actions for
`HP-SCRIPT-001`, `HP-FILE-001`, `HP-SELECT-001`, and `HP-EDIT-001`. It evaluates every
command against one in-memory virtual workspace. Normal and translate modes consume
the same completed change set; neither mode reimplements command semantics.

The engine obtains an original file only through a filesystem-loading function owned
by the CLI boundary. It never writes files or output. A logical file retains a stable
identity across moves so original-to-final actions can be derived without alias state.

## AC-BOUNDARY-001: Filesystem and output boundary

The CLI boundary owns arguments, stdin, filesystem reads, staging, commit, rollback,
diagnostics, stdout, and exit status for `HP-CLI-001` and `HP-OUTPUT-001`. Paths are
normalized with the host OS rules; relative paths resolve from the process working
directory and absolute paths remain absolute. Initial inputs cross into the engine
only after a regular-file check and strict UTF-8 decoding.

Normal mode stages the complete engine result before commit. Its transaction
coordinator owns backups, ordered path operations, rollback attempts, and honest
reporting of any commit or rollback failure. It must not expose a stronger atomicity
claim than the filesystem can provide.

## AC-TRANSLATE-001: Patch rendering

One translation renderer owns all OpenAI `apply_patch` syntax. It receives the engine's
ordered net change set and emits one envelope containing the required `Add File`,
`Update File`, `Move to`, and `Delete File` actions. It finishes the complete string
before stdout is written so evaluation or rendering failures cannot expose a partial
patch. It owns the minimal nonempty verification hunk required by OpenAI `apply_patch`
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
