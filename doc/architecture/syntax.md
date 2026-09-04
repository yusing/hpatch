# Shared compact-script framing

## CTR-SYNTAX-001 — Shared compact-script framing

One internal lexical owner implements the inline quoted-operand and fixed `<<PATCH`
heredoc framing required by `REQ-SCRIPT-001`. The root parser consumes decoded operands
and command frames for ordinary HPATCH/2 and owns a comparable opaque identity for each parsed
target. Recovery retains that identity while first separating a rejected mutation's target from
its decoded value, then uses the same root parser for replacement targets. The root `EditText`
primitive owns immutable text-target mutation. The
lexical owner performs no filesystem access, target resolution, command evaluation,
rejected-script ancestry, or output rendering.
