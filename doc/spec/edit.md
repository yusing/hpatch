# Target-bearing mutations

## REQ-EDIT-001 — Target-bearing mutations

`type TARGET VALUE` replaces every target span with the decoded value. An empty target-bearing
value deletes every target span, including a terminator owned by a complete-line or range
target. `add DESTINATION VALUE` inserts the value immediately before every line or text
destination span and preserves the destination. `add EOF VALUE` inserts once at the immutable
baseline EOF. A command with multiple text matches is atomic: resolution or conflict at any
match records none of its mutations.

Replacements and deletions must have disjoint baseline interiors. An insertion strictly
inside a replacement or deletion conflicts. Insertions exactly at either boundary are
permitted. Multiple insertions at the same baseline boundary are permitted and render in
script command order. Conflicts identify the prior command and affected baseline range;
they reject the complete script before filesystem mutation or patch output.

For a complete-line or range replacement whose target owns a final LF, CRLF, or
standalone-CR terminator, nonempty `type` preserves that exact final terminator when the
replacement does not end in a terminator. A replacement-supplied final terminator is
authoritative and is not doubled. No terminator is synthesized for an unterminated selected
final line. An empty target-bearing `type` value removes owned terminators. Inserted values
are otherwise byte-exact decoded UTF-8. Existing line endings outside explicit inserted or
replaced text remain unchanged.

The engine orders registered immutable-baseline edits once and renders one final content
value per file. It never reads pending mutated content while resolving a later target.
Content movement requires emitting the destination content; `mv` moves whole files only.

Acceptance:

1. Replacement, deletion, insertion before a line or text destination, and EOF append
   produce the specified result directly from their targets or destination.
2. Multi-match text mutation applies the same action to every requested match or none.
3. Disjoint edits are script-order independent except for deliberate insertions at the
   same boundary, which retain script order.
4. Overlapping destructive spans and insertions strictly inside them reject atomically;
   boundary insertions remain valid.
5. LF, CRLF, and standalone-CR complete-line replacement preserve the owned terminator
   for a nonempty value unless the value supplies one; an unterminated final line stays
   unterminated, while an empty value deletes any owned terminator.
