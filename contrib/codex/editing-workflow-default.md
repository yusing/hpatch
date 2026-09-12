## File editing

Use `functions.hpatch` for local file edits, not `apply_patch`.
Use shell formatting commands for bulk mechanical rewrites, but do not create or edit files
with shell write tricks or Python when hpatch is sufficient.

## Shell execution

Use `functions.shell` for command execution. Before submitting a program, follow the shared
Shell reference below for interpreter selection, input format, execution options, and continuation.

For ready commands sharing an interpreter and execution options, use one multiline shell
script. Put commands on successive lines, without batch headers or separator programs:

```bash
hcat first.go 1:40
hcat second.go 1:40
```

This is one execution. Do not create separate executions merely because reads are independent.
Keep short reads and searches together; use a later call when earlier output must determine it.
Reserve explicit batches for separate interpreters, execution options, or isolated shell state.

For slower independent commands that may safely share output, run background jobs with `&`
inside one shell script and wait for every job. Preserve each failure rather than allowing a
successful last job to hide it:

```bash
check_one &
first_check_pid=$!
check_two &
second_check_pid=$!
checks_status=0
wait "$first_check_pid" || checks_status=$?
wait "$second_check_pid" || checks_status=$?
exit "$checks_status"
```

Do not overlap dependent commands, edits, or jobs that share mutable state. This is shell-level
concurrency, not permission to call tools in parallel contrary to their contracts.

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
Copy both range endpoints as complete `LINE:HASH` pairs from the same acquired span.
A nearby brace's hash is not interchangeable with the endpoint's hash: indentation is part
of the row. A stale-target correction must still select the complete intended replacement
span; changing the hash alone does not fix an incomplete declaration boundary.
For exact content you just authored in a new file, use an unanchored literal target in the later
invocation instead of inventing a row hash or rereading the file. Use focused hcat or hgrep only
when none of those forms identifies the target.

Use the shared HPATCH/2 reference for value framing, newline ownership, and rejected-script recovery.

## Target acquisition

Acquire target-bearing context for existing-file edits. Reuse known literals, verified rows,
and confirmed mappings instead of rereading solely to obtain an already available target.
When those forms no longer identify the intended current span, acquire a focused hcat or hgrep
result for that target.

When a known identifier or literal is likely to become a target, use hgrep first; use `-F` with repeated `-e` literals
so punctuation cannot create a regex error, and add `-A` or `-B` when a small amount of surrounding code is needed.
Every emitted match or context row is target-bearing. When the owner is known but the location
is not, use inspect_file for structure or hgrep for a symbol. Copy inspect_file `LINE:HASH` spans
directly as HPATCH targets. Use bounded hcat for source text not supplied by the target-bearing
search or outline. Ordinary reads do not supply verified row identities; use the mekugi reading
tools when those identities are needed for the edit.
