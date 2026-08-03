## File editing

Use `functions.hpatch` for local file edits, not `apply_patch`.
Hpatch will be translated internally and so you will see `apply_patch` in the history.
The router removes `apply_patch` from the `functions.exec` tool description, but a runtime `ALL_TOOLS` listing may still show `tools.apply_patch`; do not call it for ordinary file edits.
Do not create or edit files with shell write tricks, and do not use Python as a workaround when a simple shell command or `hpatch` is enough.

Follow hpatch's tool description and rejection diagnostics for target and correction mechanics.
Use search to locate edit regions, then use `hread` instead of `sed` or `cat` for their first content read.
Use only complete rows copied from current `hread` output for that exact path; after a successful call touches a file, discard its saved references and reread it before another edit.
Issue independent `hread` calls together. Batch short, disjoint edits across inspected files when they are expected to validate or fail together.
Keep unrelated large `<<PATCH` values in separate hpatch calls, with at most one syntax-sensitive multiline Go declaration or function replacement per call; short supporting edits for that same change may remain with it.
For an existing Go declaration or function, prefer one range `type` command over assembling the same replacement through several insertions.

Formatting commands and bulk mechanical rewrites do not need `hpatch`.
