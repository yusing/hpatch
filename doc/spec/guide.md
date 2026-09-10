# Agent guidance

## REQ-GUIDE-001 — Agent guidance

`contrib/codex/file-editing-instructions.md` owns shared CTP/2 representation rules and durable
tool contracts. Its editing-workflow slot is rendered from adjacent `editing-workflow-astra.md`
for request model `gpt-6-astra` or IDs beginning `gpt-6-astra-`, and from
`editing-workflow-default.md` for every other or missing model ID. Selection happens on each
eligible request, including model switches and inherited marked prompts, independently of
native versus CTP/2 transport. Only the selected workflow is injected; shared tool syntax,
atomicity, recovery, and commentary-routing rules are unchanged. Both workflow files cover file
editing, commentary routing, shell submission, edit planning, target reuse, and target acquisition.
Model-specific phrasing does not alter the shared reference contracts. Guidance includes effective
use of tool capabilities: batching related edits against immutable baselines, reusing verified
targets, selecting suitable mutation forms, and leaving formatting to the engine. It does not
prescribe general task autonomy, approval checkpoints, prose length, validation scope, or a ban
on inspecting changed files. Those policies remain with the host and task instructions.
Each requirement file listed from `doc/spec/index.md` owns one normative engine or router contract. Model-visible tool descriptions contain only concise
call-local contracts and request-specific schemas. The router does not use private tool
descriptions as prompt text. Native model protocol injects the central source without its leading
CTP/2 section and stops after the ordinary guidance rewrite; CTP/2 injects the complete source and
then transforms only eligible model-visible strings under `REQ-CTP-001`.

For each eligible turn carrying a non-null Responses `instructions` string, the router refreshes
one current marked mekugi section or replaces the pinned stock Codex file-editing section and its
displaced rg and exec-command lines. The GPT-6 Astra stock template has no file-editing section:
the router recognizes its pinned introduction and work-rules heading, replaces the pinned rg line
immediately after the heading and blank separator with central guidance, and removes the pinned
exec-command line. The active Astra prompt may instead have no legacy exec-command line and one
pinned transport-independent shell-safety line after the search line; that safety line is preserved.
The search and execution anchors must be unique, and an old file-editing section must be absent.
For stock, marked, and configured custom prompts, the router also rewrites pinned conflicting
progress-channel, initial-update, skill-announcement, approval-rejection delivery, 60-second commentary/wait, Code Mode batching,
and unrestricted parallelization fragments outside the owned section. Progress uses supported
tool commentary, known reads and searches batch in a shell script, and parallelism respects tool
contracts with hpatch running alone. Unrelated instructions, including authorization, validation,
and shell-safety rules, are preserved. These rewrites cover the GPT-6 Astra and shared GPT-5.6
Sol/Terra/Luna templates and the active Codex prompt. At startup,
the router reads `$CODEX_HOME/config.toml`, falling back to `~/.codex/config.toml`, only to
snapshot whether the top-level `model_instructions_file` key is set. A configured custom prompt
without recognized stock or marked guidance receives the central guidance by append; without that setting,
the request fails before upstream forwarding as an unsupported upstream instruction change.
Missing and null `instructions` values remain unchanged. This request-local behavior covers
session start, post-compaction, subagent start, and subagent post-compaction instruction delivery;
an inherited side conversation refreshes the marked section already in its prompt. Neither
`make install`, `make uninstall`, nor the router creates, changes, or removes an instruction file.

The capture evidence in `REQ-METRICS-001` records the actual instruction carrier, matched rewrite
strategy, selected model workflow, and whether a custom instruction file was configured. Prompt
shape matching tries both stock shapes independently of workflow selection, including an
Astra-shaped override sent to a non-Astra model. Evidence describes the rewrite decision, not
proof that the model followed the guidance or that a later forwarding step succeeded.
The WebSocket transport must not elide rewritten inherited instructions against a provider
prefix containing their old values; its cache replacement contract is in `REQ-ROUTER-001`.

The recovery template adjacent to the central source owns dynamic recovery prose. After each
wholly row-stale evaluator rejection, the router supplies only the current handles and summaries
for rejected target-bearing commands. Other evaluator rejections direct the model to one complete
ordinary script. A re-rejected recovery states that prior handles are stale and refreshes the
listed commands from the latest evaluated script.

Both model variants use the shared Shell reference for execution rules; model-specific sections
point to it rather than repeat submission syntax. Shell working-directory guidance applies with
and without CTP. The variants teach the following tool workflow:

1. Submit a shell call as one free-form script without an outer wrapper. Use Bash by default or
   select another interpreter with a direct compact shebang. Keep program input on standard input,
   use exactly one `{.}` in `#!cmd=`, place request-specific outer arguments in `#!params=`, and
   use native session facilities for PTY-backed or long-running executions.
   Teach direct interpreter selection with `#!COMMAND [ARGS...]`: its body contains only the
   selected interpreter's program, without a closing heredoc delimiter. Examples do not limit
   interpreter selection. Subsequent shell checks belong in a separate default-Bash call
   after success. Runtime failure may leave earlier statements' effects in place; inspect affected
   state before retrying. Distinguish HPATCH's `<<PATCH` edit-value syntax and shell data
   heredocs from interpreter-program submission. Put the optional interpreter selector first,
   followed by at most one `#!cmd=` and one `#!params=` in either order, then the source body.
2. Inspect, edit, or rerun a retained shell script through its `@shell/` reference, and never mix
   retained and workspace paths in one HPATCH script.
3. Acquire target-bearing context for existing-file edits. When a known identifier or literal is
   likely to become a target, use hgrep first with
   repeated fixed-string patterns, adding bounded context options when surrounding code is needed.
   Every emitted match or context row is target-bearing. When the owner is known but the location
   is not, use inspect_file for structure or hgrep for a symbol. Copy inspect_file `LINE:HASH`
   spans directly as HPATCH targets. Use bounded hcat for source text not supplied by the search
   or outline.
   Use hsymbol refs for exact Go references and hsymbol def for an
   editable Go declaration after obtaining a verified selector row.
4. Run one hcat command per file and batch only already-known reads in one shell script. Copy
   only current emitted references. Do not follow target-bearing hgrep output with hcat unless
   nonmatching context outside the requested bounds is needed.
5. Choose a line, inclusive range, or anchored literal target inside the mutation command.
6. Submit every known related edit in one atomic script. Split only when a later edit depends on
   validation or information unavailable before the current call. Keep unrelated large values
   in separate failure-domain calls.
7. Use an insertion or targeted replacement rather than rewriting surrounding declarations for
   formatting that the engine already owns. Reuse exact authored text, unchanged rows, and exact
   pre-edit rows or ranges covered by confirmed routed mappings. Use returned final-state rows
   or exact unanchored current text for other changed content. Acquire a focused read when these
   forms do not identify the intended current target; rereading solely to recover an available
   target is unnecessary. Use HPATCH's `<<PATCH` value form for regular expressions and other
   escape-heavy edit values.
8. Use nonempty `type` to replace and empty target-bearing `type` to delete. Use `add` to
   insert before a line or text destination and `add EOF` to append. Use inline values for
   short text and `<<PATCH` for multiline or escape-heavy values.
9. After a wholly row-stale routed rejection, use `functions.hpatch_recover` with one current
   `C... TARGET` line per listed command. Submit every listed target correction in one atomic
   payload. Use one complete ordinary script for non-target or mixed corrections. After
   re-rejection, discard all prior handles. Ordinary `functions.hpatch` and root APIs have no
   recovery mode.
10. Let mekugi format changed Go files and syntax-check supported changed Python, JavaScript, and
    TypeScript files.

Acceptance:

1. A model can choose and encode every HPATCH/2 operation from the persistent guidance.
2. The forwarded prompt contains the selected central guidance exactly once and omits the pinned
   stock apply_patch, rg, and exec_command instructions. Native omits the CTP/2 section; CTP/2 retains
   it. Both the GPT-5 editing-section template and GPT-6 Astra work-rules template are supported.
   The workflow follows the request model, not the stock prompt shape or the proxy's first model.
   Switching models refreshes the existing marked section without retaining the other workflow.
3. A marked prompt retains unrelated content before and after the owned section and refreshes
   idempotently; a configured custom prompt without a recognized section retains unrelated content
   before the append. Pinned conflicting tool and progress fragments are rewritten in both paths,
   including fragments inherited from earlier rewrites. Fixtures cover all four cached model IDs,
   both instruction carriers, and both model protocols.
4. Missing and null request instructions remain byte-equivalent. An unconfigured, unrecognized
   non-null instruction string fails before forwarding. CTP/2 never creates or encodes its selected
   instruction carrier, and `ctp1` fails before router startup.
5. Dynamic rejected-script references and recovery prose appear only with actionable context.
6. A wholly row-stale evaluator rejection lists only the rejected target-bearing command handles
   and exact guidance for one atomic target-correction payload. Other failures direct one complete
   ordinary script; re-rejection explicitly invalidates prior handles.
7. A routed success can be followed by another hpatch call using an exact row from its report
   without an intervening hcat; a saved pre-edit row still rejects as stale.
