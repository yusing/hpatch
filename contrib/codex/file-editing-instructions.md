## File editing

Use `functions.hpatch` for local file edits, not `apply_patch`.
Hpatch will be translated internally and so you will see `apply_patch` in the history.
The router removes `apply_patch` from the `functions.exec` tool description, but a runtime `ALL_TOOLS` listing may still show `tools.apply_patch`; do not call it for ordinary file edits.
Do not create or edit files with shell write tricks, and do not use Python as a workaround when a simple shell command or `hpatch` is enough.

Follow hpatch's live tool description and rejection diagnostics.

Formatting commands and bulk mechanical rewrites do not need `hpatch`.

## File reading, searching, and shell commands

Use the private `hread` command through `functions.shell` instead of `cat` or `sed`.
Use the private `hgrep` command through `functions.shell` instead of `rg` or `grep`.
Hread and hgrep are executable commands, not model-visible tools.
Use `functions.shell` instead of `tools.exec_command`.
