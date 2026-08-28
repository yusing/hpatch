# Rejected-script recovery

## REQ-CORRECT-001 — Rejected-script recovery

The router exposes a separate model-visible `functions.hpatch_recover` custom tool with an
independent embedded Lark grammar. Recovery is unavailable from root public APIs, root
`tool_grammar.lark`, and ordinary `functions.hpatch`. A payload beginning with `type` or
`add` is therefore always an ordinary complete HPATCH/2 script.

Each rejected-script command has a `C<number>:<hash>` handle covering its complete attributable
command frame. Recovery has exactly one form per line: `C<number>:<hash> TARGET`. `TARGET` uses
the ordinary HPATCH/2 row, range, anchored-literal, or unanchored-literal target syntax and must
denote a different target from the retained command. Recovery
has no operation keyword and cannot change operations, values, heredoc bodies, command count or
order, or file context. Target parsing and script rebuilding preserve the public target literal's
exact decoded bytes, including escaped LF, and enforce the same empty, CR, and control
exclusions.

The router owns recovery grammar, parsing, handle resolution, ancestry, worktree isolation,
dispatch, replay, diagnostics, and reevaluation. Every command handle resolves against the latest
visible evaluated rejected script as one immutable baseline. Each handled command may appear at
most once in a payload. The router changes only its target, rebuilds the complete script through
the root `EditText` primitive, then evaluates that script normally.

A malformed, stale, unchanged, conflicting, incomplete, cross-worktree, or otherwise invalid recovery
changes neither workspace state nor retained rejected ancestry. Proxy-rejected attempts keep
the last evaluated script as the next baseline. A re-rejected recovery becomes the next
baseline, and replay restores the exact `functions.hpatch_recover` payload while retaining its
rebuilt script for later recovery. Non-hpatch plugin and shell failures never enter this
ancestry. Input truncation removes calls the conversation no longer shows.

When every structured rejection is `row-stale`, the routed diagnostic lists only the rejected
target-bearing commands and their current `C...` handles. Recovery guidance directs the model to
submit one `C... TARGET` line per listed command in one atomic payload. Every non-target or mixed
failure instead directs the model to submit one complete corrected ordinary HPATCH/2 script. A
handle from an older baseline is stale. A re-rejection explicitly states that no workspace file
changed, target corrections survive only in the new rejected-script baseline, and every earlier
handle is invalid. Correlation IDs remain stable and attempt
numbers increase across evaluated and proxy-rejected calls. The transport capturer observes each
provider-emitted `hpatch` or `hpatch_recover` call and its delivered carrier without changing
recovery ancestry.

Outcome hooks report one routed attempt once. Their structured event includes tool identity,
chain and call identity, attempt number, correction marker, lifecycle stage, outcome, and
emitted, evaluated, and translated-patch byte counts. `unevaluated/rejected` means the router
rejected the request before engine evaluation. `evaluated/rejected` means engine evaluation
failed without host mutation; `translated/succeeded` means a host patch was produced but does
not claim Codex applied it; `applied/succeeded` means root-owned application completed, while
`applied/failed` means root-owned commit or cleanup failed. Recovery hook Markdown treats the
exact short recovery payload as model-emitted, renders its compact resolved-operation delta,
and identifies any larger complete script as router-rebuilt. Routed evaluator rejection invokes
the outcome hook, not a second per-command error hook. Root error hooks remain separate from routed outcome hooks.

Acceptance:

1. `functions.hpatch_recover` has a dedicated grammar, is router-only, and accepts only `C... TARGET` lines.
2. Every command handle resolves against one immutable latest evaluated rejected script, and a command appears at most once per payload.
3. A successful rebuild is reevaluated as one complete ordinary HPATCH/2 script.
4. Re-rejection advances the baseline, emits refreshed target-command handles, and invalidates every prior handle; proxy rejection leaves the baseline unchanged.
5. Recovery cannot cross sessions or selected worktrees, and unrelated tools cannot become bases.
6. Replay restores `hpatch_recover` identity and the exact emitted short payload.
7. Ordinary mutation-leading hpatch scripts are never detected as recovery.
8. Captured provider calls remain individual and correlate to their actual delivered carriers.
9. One payload can correct multiple distinct command targets atomically without changing any other command field.
10. A target correction can retarget an anchored or unanchored mutation to exact multiline
    text with escaped LF; rebuilding preserves the target bytes and public control exclusions.
11. A target correction that denotes the retained target rejects before root reevaluation,
    including an explicit default occurrence count, an equivalent quoted escape spelling, or a
    range whose two endpoints are the retained single row; the retained baseline and handles remain usable.
