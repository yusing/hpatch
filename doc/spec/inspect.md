# Shell-routed structural file inspection

## REQ-INSPECT-001 — Shell-routed structural file inspection

The private `inspect_file [--source NAME] [--source-bytes N] PATH` command is
available only through the model-visible shell tool. It accepts one shell-separated
path, relative to the process working directory or absolute, like hcat. Parent
paths and symlinks are allowed; the target must be a host-readable regular file.
Codex owns filesystem permissions. Thread-private `@shell` references are read
through hcat rather than treated as workspace files by this command.

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
Declaration-owned names MUST exclude initializer-local declarations, type parameters, and fields,
including when the enclosing top-level declaration spans multiple lines.
JavaScript and TypeScript side-effect imports use their decoded module string as the name,
including single- and double-quoted literals and ECMAScript escapes and line continuations.
Recovered invalid module strings MUST NOT contribute fabricated names.
Markdown includes only ATX headings outside fences and top-level scalar keys parsed from a closed
initial `---` YAML frontmatter block. JSON includes every recognized value as a depth-first RFC
6901 pointer and value type, including the empty root pointer. Each outline entry's `line` and
`line_end` are `REQ-READ-001` `LINE:HASH` identities for the inclusive span: the positive one-based
logical line and the lowercase four-digit hash of that complete logical line, excluding its
terminator. A single-line span repeats the same identity in both fields. Repeated boundaries within an
inspection MUST reuse the verified identity of that immutable source line rather than rehashing
the complete line for every entry. Those identities are
copyable HPATCH row or `ROW..ROW` range targets. By default, results contain no raw
excerpts, bodies, fields, comments, frontmatter values, JSON scalar values, or row `TEXT`.

The optional leading `--source NAME` selects all outline entries with that exact
name or JSON pointer, in source order. An empty name selects the JSON root pointer;
duplicate names or pointers remain separate entries. No match is a successful empty
outline. `data.selection` records the selector. There is no regex or fuzzy matching.
Selection and extraction use the same parsed UTF-8 snapshot as the row identities,
so a caller can obtain a known declaration or JSON value without a preceding outline
lookup or a second read.

Selected entries add `source: {text, source_bytes, omitted_bytes}`. The text is the
exact parser-located declaration, heading, JSON value syntax, or YAML key/value
source, up to 8,192 UTF-8 bytes by default. It is not a decoded JSON/YAML value.
`--source-bytes N` accepts 1 through 8,192, requires `--source`, and can precede it.
Each option may appear once, before PATH. Prefixes never split a Unicode character.
Omitted bytes are counted explicitly; the caller must not treat a partial prefix as
complete source. The source's inclusive whole-line identities cover its full span,
including multiline selected YAML values. A heading selects its own source, not
the section that follows it. Default outline-only spans remain unchanged.

The existing total stdout ceiling also applies to selected results. It can omit
complete selected entries independently of their per-entry source-prefix bounds;
`truncated` and `truncation.after_entries` report that omission. A byte-prefix
omission alone does not mean outline entries were omitted.

The complete successful stdout, including its final LF, is at most 65,536 UTF-8 bytes. When
necessary, the worker retains the longest complete outline prefix and returns
`truncation: {"reason":"output_bytes","after_entries":N}`. Lezer parser recovery or YAML
frontmatter diagnostics set `parse_complete: false` independently of output truncation. There is
no input-size or entry-count limit. If an empty-outline success envelope cannot fit, the command
fails with `output_limit`.

Command failures write one closed LF-terminated JSON envelope to stdout, leave stderr empty, and
exit nonzero. Stable codes are `usage`, `not_found`, `not_regular`, `not_utf8`,
`read`, `parse`, and `output_limit`. The centralized Codex guidance and
private call contract embed a concise success, failure, and outline-entry shape rather than the
normative specification schema. Shell replay keeps the original call and output; inspect_file is
not model-visible, directly routed, or included in mekugi recovery ancestry. Passthrough mode
installs and advertises none of these surfaces.

Acceptance:

1. Each default language projection returns only its declared navigation identifiers and exact
   inclusive `LINE:HASH` span identities, while malformed recoverable input remains a successful
   partial result with `parse_complete: false`.
2. Default Markdown inspection excludes fences, Setext headings, nested YAML frontmatter keys, and all frontmatter
   values while preserving source order for repeated top-level scalar keys; JSON escapes `~` and
   `/`, preserves duplicate pointers, and never returns scalar values.
3. Unsupported files are checked as regular without content reads, UTF-8 validation,
   line counting, content detection, or command-level truncation.
4. Router startup validates `inspect_file` inside the immutable built-in snapshot without an
   executable frontend and exposes or routes only mekugi, shell, and configured model-visible
   contributions. Eligible request instructions use the central guidance while unrelated
   content remains unchanged; CTP/2 follows `REQ-CTP-001`.

5. Absolute, parent-relative, and symlink paths outside the current directory work
   when host permissions allow; non-regular files still fail.
6. Exact source selection returns matching code, minified JSON values, and YAML
   key/value source from the same snapshot as its full-span identities. Duplicate
   selectors, no matches, Unicode prefix boundaries, and independent total-output
   truncation remain explicit and deterministic.
