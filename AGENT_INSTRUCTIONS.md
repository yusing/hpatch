# Editing with hpatch

Inspect the relevant source before selecting it. Run `hpatch translate` from the
workspace root with paths relative to that root, then pass its stdout directly to the
native `apply_patch` tool inside one model tool call or equivalent host orchestration
boundary:

```text
translated = exec("hpatch translate", stdin=SCRIPT, cwd=WORKSPACE_ROOT)
if translated failed:
    return its diagnostic

applied = native_apply_patch(translated.stdout)
if applied failed:
    return its diagnostic

reread the intended workspace paths
run focused validation
```

The boundary requirement means the translated patch stays internal: do not invoke a
shell executable named `apply_patch`, return the patch to the model, or ask the model
to repeat it. Use a non-PTY stdin facility when the wrapper provides one. Keep stdout
patch-only and propagate diagnostics from either stage.

The available native `apply_patch` resolves relative paths from its fixed workspace
root and has no working-directory override. The working directory used by
`hpatch translate` is not embedded in its textual patch or transferred to the native
tool. Therefore:

- use workspace-relative paths and normally run translation from the workspace root;
- do not expect an absolute path or translator working directory to patch outside it;
- for an artifact ultimately required in a system temporary directory, edit and
  validate it in an ignored workspace-local staging directory, then relocate the
  completed artifact;
- treat an opaque successful native result such as `{}` as no operation summary:
  reread the intended files and validate behavior;
- treat an absent, malformed, unrelated, or unknown/future native result as
  unconfirmed rather than inventing hpatch semantics.

```text
in PATH                     select an existing file
new PATH                    select a new empty file at cursor 0:0
mv PATH                     move the selected file
rm                          remove the selected file and clear active state
sel LINE START:END          select inclusive one-based Unicode columns
tsel LINE OCCURRENCE "TEXT" select a nonempty one-line literal; -1 is last
rsel START:END              select inclusive complete logical lines
type "TEXT"                 replace the selection or insert at the cursor
del                         delete the selection
dup                         duplicate the selection
```

Commands run sequentially against the current in-memory contents and may switch files
with multiple `in` commands. Returning to a file resets its cursor to `0:0` but
retains pending edits. Every selection replaces the prior cursor or selection.
`type` and `del` leave the cursor immediately after the inserted text or at the
deleted selection's start. `dup` selects the new copy.

`rsel` owns each selected line's terminator when present. Replacing complete lines
with `type` therefore requires the replacement string to include any desired final
newline. Earlier edits immediately affect later line numbers:

```text
in example.go
rsel 2:3
type "replacement line\n"
tsel 3 1 "later"
type "updated"
```

Here the original lines 2–3, including line 3's terminator, are replaced. The later
`tsel` observes the resulting current line 3, not the original file.

`type` and `tsel` operands are JSON strings. Generate operands with a JSON encoder
instead of hand-escaping multiline text, quotes, backslashes, template literals, or
Unicode. A `type` value may contain line terminators; a `tsel` value may not.

Additional examples:

```text
new note.txt
type "foo"
type " "
type "bar\n"

in config.txt
tsel 4 -1 "old"
type "new"

in logs.txt
tsel 8 1 "debug"
del

in block.txt
rsel 2:4
dup

in old.txt
mv current.txt

in obsolete.txt
rm
```
