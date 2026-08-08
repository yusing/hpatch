# Specification review

## Basis

- Specification: `doc/spec/index.md` revision 21
- Requirements reviewed: `REQ-OUTPUT-001` and `REQ-GUIDE-001`
- Product context: `doc/brief.md`
- Recovery requirements: `todos.md` sections `HP-CONTINUE`, `REPORT-REFS`, and
  `S2-FINAL-REFERENCES`
- Independent inspections: Git-agent review `Q6QDP6VO5OLAGHX3VMINA4G2RY` and correction review `WIB3IY4SCI354BVHVRCOAOKVJE`

## Result

PASS

Revision 21 assigns the report grammar, projection, continuation behavior, and acceptance to
one interface contract. It defines authored-command and row ordering, per-command bounds, the
conditional active-file fallback, formatting-aware final coordinates, deduplication, moved and
empty-file behavior, and current-row reuse in testable terms.

The accepted correction is present: reported rows support exact boundary-local continuation,
but the projection does not promise to contain a distant or interior target needed later.
Focused hread remains required for an absent target, saved pre-edit references remain stale,
and caller-selected report ranges remain outside this increment.

No contradictory behavior, duplicate semantic owner, invented public surface, lost constraint,
or untestable must-have blocks architecture review. The existing implementation and architecture
revision are intentionally not treated as evidence that revision 21 has already been delivered;
the required revision-22 contract decision and implementation remain separate gates.
