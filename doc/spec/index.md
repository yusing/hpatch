---
pjdoc:
  version: 1
  kind: spec
  scope: root
  status: approved
  revision: "3"
  files:
    - interface.md
    - comparison.md
---
# hpatch specification

This specification covers the first draft described by [`doc/brief.md`](../brief.md).
The interface contract is authoritative for syntax and semantics.

## Inventory

- [`HP-CLI-001`](interface.md): invocation modes and final-state reporting
- [`HP-METRICS-001`](interface.md): persistent token, command, and feature metrics
- [`HP-SCRIPT-001`](interface.md): script grammar
- [`HP-FILE-001`](interface.md): file selection and lifecycle
- [`HP-SELECT-001`](interface.md): selection behavior
- [`HP-EDIT-001`](interface.md): edit behavior
- [`HP-OUTPUT-001`](interface.md): output and failure behavior
- [`HP-COMPARE-001`](comparison.md): token comparison scenarios
- [`HP-GUIDE-001`](interface.md): concise agent guidance

All listed requirements are must-haves for this increment.
