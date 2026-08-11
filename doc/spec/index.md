---
pjdoc:
  version: 1
  kind: spec
  scope: root
  status: draft
  revision: "21"
  files:
    - interface.md
    - comparison.md
    - benchmark.md
---
# hpatch specification

Revision 21 specifies bounded current final references for successful HPATCH/2 edits.
`doc/spec/interface.md` owns the public report grammar, projection rule, continuation
semantics, and observable acceptance behavior.

## Inventory

- [`REQ-CLI-001`](interface.md): invocation modes and final-state reporting
- [`REQ-READ-001`](interface.md): routed verified-row reading and bounded ranges
- [`REQ-GREP-001`](interface.md): routed ripgrep search with directly editable verified rows
- [`REQ-PLUGIN-001`](interface.md): router-local custom tools and Code Mode carrier translation
- [`REQ-SHELL-001`](interface.md): installable free-form script tool and interpreter selection
- [`REQ-METRICS-001`](interface.md): persistent encoding, command, target, and failure metrics
- [`REQ-SCRIPT-001`](interface.md): HPATCH/2 grammar and target forms
- [`REQ-CORRECT-001`](interface.md): rejected-script recovery with ordinary verified-row edits
- [`REQ-FILE-001`](interface.md): file scope and lifecycle
- [`REQ-SELECT-001`](interface.md): immutable-baseline target resolution
- [`REQ-EDIT-001`](interface.md): target-bearing mutation behavior
- [`REQ-OUTPUT-001`](interface.md): output, validation, and failure behavior
- [`REQ-COMPARE-001`](comparison.md): token comparison scenarios
- [`REQ-BENCH-001`](benchmark.md): historical-commit correctness and paired model evaluation
- [`REQ-GUIDE-001`](interface.md): concise agent guidance

All listed requirements are must-haves for this increment.
