## File editing

Use `functions.hpatch` for local file edits, not `apply_patch`.
Hpatch will be translated internally and so you will see `apply_patch` in the history.
The router removes `apply_patch` from the `functions.exec` tool description, but a runtime `ALL_TOOLS` listing may still show `tools.apply_patch`; do not call it for ordinary file edits.
Do not create or edit files with shell write tricks, and do not use Python as a workaround when a simple shell command or `hpatch` is enough.

Follow hpatch's tool description and rejection diagnostics for selection and correction mechanics.

Formatting commands and bulk mechanical rewrites do not need `hpatch`.
