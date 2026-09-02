# Shell-routed structural file inspection

## REQ-INSPECT-001 — Shell-routed structural file inspection

The private `inspect_file PATH` command is available only through the model-visible shell tool.
It accepts exactly one shell-separated workspace-relative path and no options. The canonical
workspace is `process.cwd()`. Absolute paths, lexical escapes, `@shell` paths, and symlinks whose
canonical targets escape that workspace fail. In-workspace symlinks are allowed, and the final
target must be a regular file.

Extension matching is exact and case-sensitive. Code formats are `.go`, `.py`, `.pyi`, and every stable
TypeScript 7 source extension: `.ts`, `.tsx`, `.d.ts`, `.mts`, `.d.mts`, `.cts`, `.d.cts`, `.js`,
`.jsx`, `.mjs`, and `.cjs`. `.md` and `.json` remain structural formats. These formats use pinned
parsers; TypeScript and JSX select the corresponding JavaScript parser dialects, while Markdown
uses Lezer for headings and YAML for a closed initial frontmatter block. Every other extension returns
`kind: "none"`, the reported regular-file byte size, `line_count: null`, `parse_complete: true`,
and an empty outline without reading or decoding content. Supported files must be strict UTF-8.
Their logical line count matches `REQ-READ-001`, including CRLF, lone CR, empty files, and final
terminators.

Success is one LF-terminated JSON document with `ok`, `data`, `truncated`, and `truncation`.
`data` contains the normalized requested path, kind, language, exact inspected byte size, logical
line count, parser-completeness flag, and a flat source-ordered outline. Code entries include only
imports, top-level constants and variables, types, classes, functions, and direct methods.
Markdown includes only ATX headings outside fences and top-level scalar keys parsed from a closed
initial `---` YAML frontmatter block. JSON includes every recognized value as a depth-first RFC
6901 pointer and value type, including the empty root pointer. Each outline entry's `line` and
`line_end` are `REQ-READ-001` `LINE:HASH` identities for the inclusive span: the positive one-based
logical line and the lowercase four-digit hash of that complete logical line, excluding its
terminator. A single-line span repeats the same identity in both fields. Those identities are
copyable HPATCH row or `ROW..ROW` range targets. No result contains raw excerpts, bodies, fields,
comments, frontmatter values, JSON scalar values, or row `TEXT`.

The complete successful stdout, including its final LF, is at most 65,536 UTF-8 bytes. When
necessary, the worker retains the longest complete outline prefix and returns
`truncation: {"reason":"output_bytes","after_entries":N}`. Lezer parser recovery or YAML
frontmatter diagnostics set `parse_complete: false` independently of output truncation. There is
no input-size or entry-count limit. If an empty-outline success envelope cannot fit, the command
fails with `output_limit`.

Command failures write one closed LF-terminated JSON envelope to stdout, leave stderr empty, and
exit nonzero. Stable codes are `usage`, `not_found`, `not_regular`, `not_utf8`,
`outside_workspace`, `read`, `parse`, and `output_limit`. The centralized Codex guidance and
private call contract embed a concise success, failure, and outline-entry shape rather than the
normative specification schema. Shell replay keeps the original call and output; inspect_file is
not model-visible, directly routed, or included in hpatch recovery ancestry. Passthrough mode
installs and advertises none of these surfaces.

Acceptance:

1. Each supported language projection returns only its declared navigation identifiers and exact
   inclusive `LINE:HASH` span identities, while malformed recoverable input remains a successful
   partial result with `parse_complete: false`.
2. Markdown excludes fences, Setext headings, nested YAML frontmatter keys, and all frontmatter
   values while preserving source order for repeated top-level scalar keys; JSON escapes `~` and
   `/`, preserves duplicate pointers, and never returns scalar values.
3. Unsupported files are confined and checked as regular without content reads, UTF-8 validation,
   line counting, content detection, or command-level truncation.
4. Router startup validates `inspect_file` inside the immutable built-in snapshot without an
   executable frontend and exposes or routes only hpatch, shell, and configured model-visible
   contributions. Eligible request instructions use the central guidance while unrelated
   content remains unchanged; CTP/2 follows `REQ-CTP-001`.
