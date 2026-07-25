# Editing with hpatch

Inspect the relevant source, then send a compact script to `hpatch translate`. Pass its
stdout directly to the native `apply_patch` tool inside the same wrapper operation:

```text
translated = exec("hpatch translate", stdin=SCRIPT)
if translated failed: return its error
apply_patch(translated.stdout)
```

Keep the translated patch internal. Do not invoke a shell executable named
`apply_patch`, return the patch to the model, or ask the model to repeat it. The native
tool call is what applies the change and lets the harness display its diff.

```text
in PATH                     select an existing file
new PATH                    select a new empty file at cursor 0:0
mv PATH                     move the selected file
rm                          remove the selected file
sel LINE START:END          select inclusive one-based columns
tsel LINE OCCURRENCE "TEXT" select a literal; -1 means the last occurrence
rsel START:END              select inclusive complete lines
type "TEXT"                 replace the selection or insert at the cursor
del                         delete the selection
dup                         duplicate the selection
```

Commands run sequentially and may switch files with multiple `in` commands. `type` and
`tsel` operands are JSON strings.
