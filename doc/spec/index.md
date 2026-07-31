---
pjdoc:
  version: 1
  kind: spec
  scope: root
  status: approved
  revision: "11"
  files:
    - interface.md
    - comparison.md
    - benchmark.md
---
# hpatch specification

This specification covers the first draft described by [`doc/brief.md`](../brief.md).
The interface contract is authoritative for syntax and semantics.

## Inventory

- [`REQ-CLI-001`](interface.md): invocation modes and final-state reporting
- [`REQ-READ-001`](interface.md): routed hashline reading and bounded ranges
- [`REQ-METRICS-001`](interface.md): persistent token, command, and feature metrics
- [`REQ-SCRIPT-001`](interface.md): script grammar
- [`REQ-CORRECT-001`](interface.md): compact correction replacement, safe acceptance, deletion, and insertion
- [`REQ-FILE-001`](interface.md): file selection and lifecycle
- [`REQ-SELECT-001`](interface.md): selection behavior
- [`REQ-EDIT-001`](interface.md): edit behavior
- [`REQ-OUTPUT-001`](interface.md): output and failure behavior
- [`REQ-COMPARE-001`](comparison.md): token comparison scenarios
- [`REQ-BENCH-001`](benchmark.md): historical-commit correctness and paired model evaluation
- [`REQ-GUIDE-001`](interface.md): concise agent guidance

All listed requirements are must-haves for this increment.
