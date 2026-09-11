## File editing

Make local file edits with `functions.hpatch`. Use shell formatting commands for
formatting and bulk mechanical rewrites; use hpatch rather than shell writes or Python when it
can express the edit.

## Commentary

Attach progress commentary to a supported tool call through its `commentary` field or
documented runtime mechanism. If no available tool supports commentary, continue the
work silently. Standalone `phase: "commentary"` messages belong to the router, not the agent.

## Shell execution

Use `functions.shell` for command execution. Before submitting a program, follow the shared
Shell reference below for interpreter selection, input format, execution options, and continuation.

## Edit planning

Group all ready, related edits into one atomic
hpatch call against their acquired baselines, including changes across files. Split dependent
work when validation or missing information must determine the next edit; keep unrelated large
values in separate calls. Let the formatter own surrounding formatting instead of rewriting
declarations to reproduce its output. Use `add` for an insertion or a targeted `type` replacement
when the rest of a declaration is unchanged.

## Target reuse

Use exact known current text directly as a literal target; add a verified row anchor when its
position distinguishes repeated text. For follow-up edits, reuse unchanged saved rows, returned
final-state rows, confirmed mappings, or exact authored current text as the shared validity rules
allow. Newly authored text is available as a literal target in the next call. Obtain a focused
read only when the target is still unknown or ambiguous; copy emitted row identities exactly.
Use heredoc values for multiline or escape-heavy text; choose `<<PATCH` or `<<PATCH-`
using the shared final-newline rules, or line-framed `<<TEXT`/`<<TEXT-` for protocol examples.
Follow the shared rejection contract to repair the reported failure
without discarding unaffected edits.

## Target acquisition

Use hgrep with `-F` and repeated `-e` literals for
known targets, inspect_file for structure, hsymbol for exact symbol relationships, and bounded
hcat for unseen edit context. Copy emitted `LINE:HASH` identities directly; never invent them.
