# Shared compact-script framing

## CTR-SYNTAX-001 — Shared compact-script framing

One internal lexical owner implements the inline quoted-operand and fixed `<<PATCH`
heredoc framing required by `REQ-SCRIPT-001`. The root parser consumes decoded operands
and command frames for ordinary HPATCH/2. The router reuses the lexical owner only to decode
ordinary quoted targets while rebuilding a target-corrected rejected script. The root
`EditText` primitive owns immutable text-target mutation. The
lexical owner performs no filesystem access, target resolution, command evaluation,
rejected-script ancestry, or output rendering.
