---
pjdoc:
  version: 1
  kind: spec
  scope: root
  status: draft
  revision: "35"
  files:
    - interface.md
    - comparison.md
    - benchmark.md
    - ctp.md
---
# hpatch specification

Revision 35 adds the opt-in spawned-subagent Mentor Handoff schedule and its native-versus-guided
paired benchmark. The router recognizes Codex's exact thread-spawn marker, temporarily selects a
higher-intelligence model, and returns the child to its configured model after bounded tool,
message, or provider-input usage. Reports separate mentor and configured-model provider usage while
comparing their combined treatment total.
`doc/spec/interface.md` owns the public report grammar, projection rule, continuation
semantics, and observable acceptance behavior.

## Inventory

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
- [`REQ-CTP-001`](ctp.md): lossless token-positive model-visible data-plane encoding
- [`REQ-GUIDE-001`](interface.md): concise agent guidance

All listed requirements are must-haves for this increment.
