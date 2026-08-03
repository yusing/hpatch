## File editing

Use `functions.hpatch` for local file edits, not `apply_patch`.
Hpatch will be translated internally and so you will see `apply_patch` in the history.
The router removes `apply_patch` from the `functions.exec` tool description, but a runtime `ALL_TOOLS` listing may still show `tools.apply_patch`; do not call it for ordinary file edits.
Do not create or edit files with shell write tricks, and do not use Python as a workaround when a simple shell command or `hpatch` is enough.

Follow hpatch's tool description and rejection diagnostics for target and correction mechanics.
Use search to locate edit regions, then use `hread` instead of `sed` or `cat` for their first content read.
Issue independent `hread` calls together, and batch disjoint edits across files into one hpatch call with repeated `in PATH` sections.
Reread only when a later edit depends on content introduced by a successful call.

Formatting commands and bulk mechanical rewrites do not need `hpatch`.
