# Bounded final-state projection

## CTR-STATE-001 — Bounded final-state projection

One projector owned beside the engine consumes only the completed result. It derives the
active final path, last effective mutation operation and surviving path, affected target
count, at most three immutable-baseline Unicode ranges, remaining-range count, aggregate
net file actions, and final-reference blocks for `REQ-OUTPUT-001`. For each surviving
effective content command, it uses the editor's existing splices and rendered content to
form one aggregate rendered extent, maps the whole extent through the existing language-format
offset map, and selects the endpoint rows plus their immediate surviving neighbors. It
retains authored command order, orders each block by final line, and deduplicates rows
within that block. It does not consult the committed filesystem or reconstruct editor state.

The engine's formatting map owns pre-format to final-source correspondence. It retains
physical source spans independently of lexical identity, uses `go/format` for canonical
numeric spelling, follows `go/ast.SortImports`' surviving spec identities and order, and
preserves ordered code anchors elsewhere. Rewritten comment and punctuation gaps project
between surviving anchors. Import-local maps include leading inline comments and complete-row
terminators, follow reordered imports, and map deduplicated imports to their surviving
equivalents. Only parenthesized import specs form movable regions, bounded by the declaration
body, prior specs, and the formatter's consecutive import runs. Declaration comments and doc
comments before a run remain outside those regions; standalone comments retain their own row
terminators. Extent projection covers every overlapping anchor, including interior imports
that move outside the mapped endpoint pair. Report, alias, and whitespace-cleanup consumers
share this projection and retain their own endpoint policies. The complete formatting and
whitespace-deletion offset machinery lives in `format_offsets.go`.

One pure formatter renders the projection through the shared verified-row owner. It emits
at most four rows per effective command and retains the existing three-row active-file
fallback whenever that active file has no reference block. It truncates displayed row text
to 64 Unicode code points while hashing complete final line content. It preserves active
and moved paths, last-edit summaries, net file counts, empty-file rows, and control escaping
before any external effect. Apply and translation paths share this projection.

The projection borrows completed editor content during rendering and retains no additional
original or final content copy. Its rows describe only successful completed state. The router
consumes the rendered report and does not compute coordinates, hashes, command extents, or
formatting adjustments.
