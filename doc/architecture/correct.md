# Router-only rejected-script recovery

## CTR-CORRECT-001 — Router-only rejected-script recovery

The router owns recovery grammar, command handles, rejected-script ancestry,
worktree isolation, correlation, replay, diagnostics, and complete-script
reevaluation. Target shortcuts replace the target of a handled command; ordinary
script-text mutations can repair any selected retained text. Both operate against
the same latest visible evaluated rejection. The router uses the generic root
`EditTextBounded` primitive to rebuild the complete script, then the same dispatch and result
projection as ordinary scripts. This shared boundary owns workspace translation versus private
retained-shell application, attempt metadata, limits, and applied confirmation; recovery owns
only rebuilding, ancestry, and correction guidance. The core evaluator, root public APIs, root
grammar, and ordinary `functions.hpatch` have no edit-only recovery mode. Generic text editing
and pre-render byte accounting belong to the root; selecting the rejected baseline
and enforcing its configured bound belong to the router. No mutable shell artifact
is another source of rejected-script state. Malformed, stale, conflicting, or incomplete recovery
changes neither the workspace nor the retained evaluated baseline.
Durable translation facts belong to the replay store under `CTR-BOUNDARY-001`; the recovery
owner receives an ordered request-local view of visible calls, followed by the current response's
evaluated calls. It never selects a baseline by scanning all stored calls or a parent's latest state.
Restart and forks do not change this selection rule. Confirmation and alias selection remain
isolated to the request even when routing or thread identities are shared.
Command-handle hashes include the complete retained baseline as well as the
individual frame, so a script-text correction that changes file context cannot
leave an unchanged mutation's earlier handle usable.

For the target-only shortcut, while separating a retained `type` or `add` command's
target from its value, the router obtains the
target's opaque identity from the root target parser and retains it with the command. Before
rebuilding, it obtains the replacement identity from that same parser and rejects an unchanged
identity without root reevaluation. Equivalent quoted escapes and implicit versus explicit default
occurrence counts share one identity; a single-row range shares its row's identity. That proxy
rejection retains the same baseline and handles for a later different target.

Mixed-script continuation is a separate router interface under
[CTR-PLUGIN-001](plugin.md). Edit-only recovery routes mixed and resume calls to
that interface without classifying successful translation as successful execution.
