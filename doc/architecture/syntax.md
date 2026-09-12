# Shared compact-script framing

## CTR-SYNTAX-001 — Shared compact-script framing

One internal lexical owner implements inline quoted operands and both fixed-delimiter heredoc
modes (`<<PATCH` and `<<PATCH-`) required by `REQ-SCRIPT-001`. The root parser consumes decoded operands
and command frames for ordinary HPATCH/2 and owns a comparable opaque identity for each parsed
target. Recovery retains that identity while first separating a rejected mutation's target from
its decoded value, then uses the same root parser for replacement targets. The root `EditText`
primitive owns immutable text-target mutation. The
lexical owner performs no filesystem access, target resolution, command evaluation,
rejected-script ancestry, or output rendering.

The same lexical owner splits routed `shell COMMAND` lines and `shell <<SHELL`
frames into ordered shell programs and contiguous edit segments. The exact block
opener is reserved and cannot fall back to a single-line command. Other inline
`<<` occurrences reject, keeping the single-line lexical rule small and unambiguous. It uses existing command framing to skip
edit values, preserving body bytes and original physical-line positions. It
performs no shell parsing or execution. Root engine parsing remains edit-only.
