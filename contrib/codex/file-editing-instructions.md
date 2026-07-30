## File editing

Use `functions.hpatch` for local file edits, not `apply_patch`.
Hpatch will be translated internally and so you will see `apply_patch` in the history.
The router removes `apply_patch` from the `functions.exec` tool description, but a runtime `ALL_TOOLS` listing may still show `tools.apply_patch`; do not call it for ordinary file edits.
Do not create or edit files with shell write tricks, and do not use Python as a workaround when a simple shell command or `hpatch` is enough.

Line numbers in a selector are baseline coordinates. Since the baseline never shifts, never
adjust one to account for an edit earlier in the same script — that arithmetic is what lands an
edit on the wrong line. Read numbers from a fresh `nl -ba`.

If a script is rejected, correct it from the diagnostic rather than retrying with an empty or speculative variant. When the tool result offers indexed repair operations (`INDEX: COMMAND`, `-INDEX`, `+INDEX: COMMAND`, `INDEX+: COMMAND`), prefer those over resending the full script; indices are the rejected evaluation's command numbers, not source-line numbers.

Formatting commands and bulk mechanical rewrites do not need `hpatch`.
