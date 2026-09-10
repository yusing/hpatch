## File editing

Use `functions.hpatch` for local file edits, not `apply_patch`.
Use shell formatting commands for bulk mechanical rewrites, but do not create or edit files
with shell write tricks or Python when hpatch is sufficient.

## Commentary

Attach progress commentary only to a supported tool call, using that tool's `commentary` field or
documented runtime commentary mechanism. When no available tool supports commentary, continue
without a commentary message. Never emit a standalone assistant message with
`phase: "commentary"`; standalone commentary messages are router-owned.

## Shell execution

Use `functions.shell` for command execution. Before submitting a program, follow the shared
Shell reference below for interpreter selection, input format, execution options, and continuation.

## Edit planning

When inspected files are ready, submit every known related edit
in one atomic script, including related multiline declarations and repeated `in PATH`
sections. Split only when a later edit depends on validation or information unavailable
before the current call. Keep unrelated large `<<PATCH` values in separate failure-domain
calls. When a formatter
owns formatting, alignment, or indentation, do not replace surrounding lines merely to
reproduce its output; let the formatter apply those changes. For example, add one struct
field with one insertion rather than replacing the declaration.

## Target reuse

When an exact current literal is already known, an unanchored literal can target it against the
complete immutable baseline. Use a row anchor when repeated text makes the intended position
significant.

On later calls, target previously changed content with a returned final-state row, a
confirmed mapping, or exact unanchored current text; never reconstruct a row or range endpoint.
For exact content you just authored in a new file, use an unanchored literal target in the later
invocation instead of inventing a row hash or rereading the file. Use focused hread or hgrep only
when none of those forms identifies the target.

Use HPATCH's `<<PATCH` value form for regular expressions and other escape-heavy edit values
so HPATCH quoted-string escaping stays separate from source escaping. Acquire only an exact missing
target.

## Target acquisition

Acquire target-bearing context for existing-file edits. Reuse known literals, verified rows,
and confirmed mappings instead of rereading solely to obtain an already available target.
When those forms no longer identify the intended current span, acquire a focused hread or hgrep
result for that target.

When a known identifier or literal is likely to become a target, use hgrep first; use `-F` with repeated `-e` literals
so punctuation cannot create a regex error, and add `-A` or `-B` when a small amount of surrounding code is needed.
Every emitted match or context row is target-bearing. When the owner is known but the location
is not, use inspect_file for structure or hgrep for a symbol. Copy inspect_file `LINE:HASH` spans
directly as HPATCH targets. Use bounded hread for source text not supplied by the target-bearing
search or outline. Ordinary reads do not supply verified row identities; use the mekugi reading
tools when those identities are needed for the edit.
