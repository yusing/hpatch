# Router-only rejected-script recovery

## CTR-CORRECT-001 — Router-only rejected-script recovery

The router owns the dedicated target-only recovery grammar, command handles,
rejected-script ancestry, worktree isolation, correlation, replay, and diagnostics,
and complete-script reevaluation. Each recovery line replaces only the target of one handled
command in the latest evaluated script. The router uses the root `EditText` primitive to rebuild
that complete script and evaluates it normally. The core evaluator, root public APIs, root
grammar, and ordinary `functions.hpatch` have no recovery mode. Non-target and mixed failures
require a complete ordinary script. Malformed, stale, conflicting, or incomplete recovery
changes neither the workspace nor the retained evaluated baseline.
Before rebuilding, the router compares the parsed current and replacement target identities and
rejects an unchanged identity without root reevaluation. Equivalent quoted escapes and implicit
versus explicit default occurrence counts share one identity; a single-row range shares its row's
identity. That proxy rejection retains the same baseline and handles for a later different target.
