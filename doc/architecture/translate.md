# Patch rendering

## CTR-TRANSLATE-001 — Patch rendering

One translation renderer owns all OpenAI `apply_patch` syntax. It receives the engine's ordered net change set and emits one envelope containing the required `Add File`, `Update File`, `Move to`, and `Delete File` actions. It finishes the complete string before returning so evaluation or rendering failures cannot expose a partial patch.

The public `RenderFileWritePatch` adapter also uses this renderer for literal shell heredoc
writes. It renders one unconditional `Add File` action, which the host can use to overwrite an
existing file without a sampled baseline. It accepts only paths and empty or LF-terminated UTF-8
content representable byte-exactly by the host format. This rendering-only API performs no
filesystem reads, source validation, formatting, hooks, or application. The router owns shell
eligibility, execution ordering, and runtime target guards; Codex owns authorization and writes.

For root-scoped engine translation, every emitted path is relative to the workspace root, independent of cwd. The router's normal host adapter uses `TranslateForHostAt`; it evaluates against an optional canonical metadata directory without confinement, rejects relative operands when no directory is selected, never falls back to router cwd, and preserves cleaned host path identities for Codex's carrier. The renderer owns the minimal nonempty verification hunk required by OpenAI `apply_patch` when a move has no content change, and the renderer-only LF normalization required by the line-oriented output format. For changed content it expands context until every bare hunk's old-side sequence is unique, failing instead of emitting an ambiguous patch. The engine's evaluated contents remain unchanged.
