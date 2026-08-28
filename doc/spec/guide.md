# Agent guidance

## REQ-GUIDE-001 — Agent guidance

`contrib/codex/file-editing-instructions.md` is the single Codex source for CTP/2 representation
rules and all durable edit, shell, read, search, and inspection guidance.
Each requirement file listed from `doc/spec/index.md` owns one normative engine or router contract. Model-visible tool descriptions contain only concise
call-local contracts and request-specific schemas. The router does not use private tool
descriptions as prompt text. Native model protocol injects the central source without its leading
CTP/2 section and stops after the ordinary guidance rewrite; CTP/2 injects the complete source and
then transforms only eligible model-visible strings under `REQ-CTP-001`.

For each eligible turn carrying a non-null Responses `instructions` string, the router refreshes
one current marked hpatch section or replaces the pinned stock Codex file-editing section and its
displaced rg and exec-command lines. It preserves all unrelated instruction content. At startup,
the router reads `$CODEX_HOME/config.toml`, falling back to `~/.codex/config.toml`, only to
snapshot whether the top-level `model_instructions_file` key is set. A configured custom prompt
without either recognized section receives the central guidance by append; without that setting,
the request fails before upstream forwarding as an unsupported upstream instruction change.
Missing and null `instructions` values remain unchanged. This request-local behavior covers
session start, post-compaction, subagent start, and subagent post-compaction instruction delivery;
an inherited side conversation refreshes the marked section already in its prompt. Neither
`make install`, `make uninstall`, nor the router creates, changes, or removes an instruction file.

The recovery template adjacent to the central source owns dynamic recovery prose. After each
wholly row-stale evaluator rejection, the router supplies only the current handles and summaries
for rejected target-bearing commands. Other evaluator rejections direct the model to one complete
ordinary script. A re-rejected recovery states that prior handles are stale and refreshes the
listed commands from the latest evaluated script.

Persistent guidance teaches this workflow:

1. Submit a shell call as one free-form script without an outer wrapper. Use Bash by default or
   select another interpreter with a direct compact shebang. Keep program input on standard input,
   use exactly one `{.}` in `#!cmd=`, place request-specific outer arguments in `#!params=`, and
   use native session facilities for PTY-backed or long-running executions.
2. Inspect, edit, or rerun a retained shell script through its `@shell/` reference, and never mix
   retained and workspace paths in one hpatch script.
3. Acquire target-bearing context once before editing. When a known identifier or literal is
   likely to become a target, use hgrep first with
   repeated fixed-string patterns, adding bounded context options when surrounding code is needed.
   Every emitted match or context row is target-bearing. When the owner is known but the location
   is not, use inspect_file for structure or hgrep for a symbol, then hread only the smallest range
   needed to obtain the target. Use hsymbol refs for exact Go references and hsymbol def for an
   editable Go declaration after obtaining a verified selector row. Avoid whole-file hread unless
   the complete file is necessary.
4. Run one hread command per file and batch only already-known reads in one shell script. Copy
   only current emitted references. Do not follow target-bearing hgrep output with hread unless
   nonmatching context outside the requested bounds is needed.
5. Choose a line, inclusive range, or anchored literal target inside the mutation command.
6. Submit every known related edit in one atomic script. Split only when a later edit depends on
   validation or information unavailable before the current call. Keep unrelated large values
   in separate failure-domain calls.
7. Prefer the smallest mutation and let hpatch formatting own formatting. After success, do not
   hread, hgrep, hsymbol, or run `git diff` on a changed file or a directory containing one merely to
   inspect, verify, or locate a follow-up target. Reuse the exact authored value, unchanged rows,
   and any
   exact pre-edit row or range covered by a confirmed routed `reuse` mapping. Use a returned
   final-state row or exact unanchored current text for other changed content; acquire only a
   target that none of these forms identifies. Use a fixed heredoc for regular expressions and
   other escape-heavy source.
8. Use nonempty `type` to replace and empty target-bearing `type` to delete. Use `add` to
   insert before a line or text destination and `add EOF` to append. Use inline values for
   short text and `<<PATCH` for multiline or escape-heavy values.
9. After a wholly row-stale routed rejection, use `functions.hpatch_recover` with one current
   `C... TARGET` line per listed command. Submit every listed target correction in one atomic
   payload. Use one complete ordinary script for non-target or mixed corrections. After
   re-rejection, discard all prior handles. Ordinary `functions.hpatch` and root APIs have no
   recovery mode.
10. Let hpatch format changed Go files and syntax-check supported changed Python, JavaScript, and
    TypeScript files.

Acceptance:

1. A model can choose and encode every HPATCH/2 operation from the persistent guidance.
2. The forwarded prompt contains the selected central guidance exactly once and omits the pinned
   stock apply_patch, rg, and exec_command instructions. Native omits the CTP/2 section; CTP/2 retains
   it.
3. A marked prompt retains content before and after the owned section and refreshes idempotently;
   a configured custom prompt without a recognized section retains its content before the append.
4. Missing and null request instructions remain byte-equivalent. An unconfigured, unrecognized
   non-null instruction string fails before forwarding. CTP/2 never creates or encodes its selected
   instruction carrier, and `ctp1` fails before router startup.
5. Dynamic rejected-script references and recovery prose appear only with actionable context.
6. A wholly row-stale evaluator rejection lists only the rejected target-bearing command handles
   and exact guidance for one atomic target-correction payload. Other failures direct one complete
   ordinary script; re-rejection explicitly invalidates prior handles.
7. A routed success can be followed by another hpatch call using an exact row from its report
   without an intervening hread; a saved pre-edit row still rejects as stale.
