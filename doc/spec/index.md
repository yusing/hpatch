---
pjdoc:
  version: 1
  kind: spec
  scope: root
  status: approved
  revision: "18"
  files:
    - interface.md
    - comparison.md
    - benchmark.md
---
# hpatch specification

This revision specifies the incompatible HPATCH/2 increment described by
[`doc/brief.md`](../brief.md). `doc/spec/interface.md` owns the public grammar and
observable command semantics. The existing executable HPATCH/1 grammar remains the
implementation baseline until a separately authorized implementation slice replaces it.

## Inventory

- [`REQ-CLI-001`](interface.md): invocation modes and final-state reporting
- [`REQ-READ-001`](interface.md): routed verified-row reading and bounded ranges
- [`REQ-GREP-001`](interface.md): routed ripgrep search with directly editable verified rows
- [`REQ-PLUGIN-001`](interface.md): router-local custom tools and Code Mode carrier translation
- [`REQ-SHELL-001`](interface.md): installable free-form script tool and interpreter selection
- [`REQ-METRICS-001`](interface.md): persistent encoding, command, target, and failure metrics
- [`REQ-SCRIPT-001`](interface.md): HPATCH/2 grammar and target forms
- [`REQ-CORRECT-001`](interface.md): compact rejected-script corrections
- [`REQ-FILE-001`](interface.md): file scope and lifecycle
- [`REQ-SELECT-001`](interface.md): immutable-baseline target resolution
- [`REQ-EDIT-001`](interface.md): target-bearing mutation behavior
- [`REQ-OUTPUT-001`](interface.md): output, validation, and failure behavior
- [`REQ-COMPARE-001`](comparison.md): token comparison scenarios
- [`REQ-BENCH-001`](benchmark.md): historical-commit correctness and paired model evaluation
- [`REQ-GUIDE-001`](interface.md): concise agent guidance

All listed requirements are must-haves for this increment.
