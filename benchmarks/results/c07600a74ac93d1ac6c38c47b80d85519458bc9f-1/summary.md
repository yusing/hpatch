# Benchmark report — `c07600a74ac93d1ac6c38c47b80d85519458bc9f`

Task: `etcd-range-stream`  
Configuration: `gpt-5.6-sol`, medium reasoning, 1 measured Hpatch run(s).

## Outcome

| Measure | Control | Hpatch | Difference |
|---|---:|---:|---:|
| Task pass rate | 1/1 | 1/1 | equal |
| Agent wall time (s) | 392.352 | 405.199 | 12.847 |
| Total input tokens | 1018357 | 1401592 | 383235 |
| Uncached input tokens | 136693 | 82680 | -54013 |
| Output tokens | 12765 | 11469 | -1296 |
| Model requests | 22 | 29 | 7 |

## Command behavior

Counts are parsed command invocations, not compound shell event rows.

| Category | Control | Hpatch | Control after edit | Hpatch after edit |
|---|---:|---:|---:|---:|
| File reads | 23 | 8 | 3 | 4 |
| Search / grep | 13 | 14 | 5 | 3 |
| Discovery / find | 0 | 1 | 0 | 0 |
| git diff content | 1 | 1 | 1 | 1 |
| git diff --check | 2 | 3 | 2 | 3 |
| git diff metadata | 1 | 1 | 1 | 1 |
| git status | 2 | 2 | 1 | 1 |
| Tests / builds | 2 | 4 | 2 | 3 |
| Formatters | 2 | 0 | 2 | 0 |
| Upstream fetches | 0 | 0 | 0 | 0 |
| Other | 10 | 8 | 4 | 2 |

A same-path loop is a read, search, or content git diff whose concrete path operand appears in completed file changes both before and after that command. Pattern-only text matches and terminal validation reads do not count.

| Post-edit path behavior | Control | Hpatch |
|---|---:|---:|
| File reads on an exact prior-changed path | 0 | 3 |
| File reads in a same-path edit → command → edit loop | 0 | 3 |
| File reads on a prior-changed path with no later change | 0 | 0 |
| Search / grep on an exact prior-changed path | 0 | 0 |
| Search / grep in a same-path edit → command → edit loop | 0 | 0 |
| Search / grep on a prior-changed path with no later change | 0 | 0 |
| git diff content on an exact prior-changed path | 1 | 1 |
| git diff content in a same-path edit → command → edit loop | 1 | 1 |
| git diff content on a prior-changed path with no later change | 0 | 0 |
| Workspace-wide bare git diff after an edit | 0 | 0 |

| Structural measure | Control | Hpatch |
|---|---:|---:|
| command execution items | 12 | 19 |
| parsed command invocations | 56 | 42 |
| failed command execution items | 0 | 1 |
| file change events | 5 | 4 |
| changed path entries | 13 | 12 |
| unique changed paths | 8 | 8 |
| repeated changed paths | 4 | 3 |

## Hpatch reliability

| Measure | Result |
|---|---:|
| Successful calls | 4 |
| Rejected calls | 1 |
| Correction calls | 0 |
| Chains using correction | 0 |
| Recovered rejected chains | 0 |
| Repeated rejection signature in a later attempt | 0 |
| Later rejected attempt on the same command, operation, target kind, and path | 0 |
| Maximum attempts in one chain | 1 |
| Diagnostic input tokens | 541 |
| Retained attempt telemetry | 5/5 calls |

| Rejection reason | Operation | Target | Prior confirmed target relation | Count |
|---|---|---|---|---:|
| row-stale | type | range | unknown | 1 |

## Automated findings

- Task success is at parity or better: Hpatch passed 1/1 versus control 1/1.
- Same-path structural loops remain: 3 file-read, 0 search, and 1 content-diff invocation(s), versus control at 0, 0, and 1.
- Recovery remains: 1 rejected call(s) caused 0 correction call(s); maximum chain depth was 1.
- Most frequent retained rejection: row-stale `type` with range target and unknown prior-target relation, 1 location(s).
- No later rejected attempt repeated an earlier rejection signature in the same chain.
- No later rejected attempt reused an earlier command, operation, target kind, and path in the same chain.
- Requests changed by +7, total input by +383235 tokens, and output by -1296 tokens relative to control.

## Edit payload

| Measure | Apply-patch equivalent | Hpatch | Reduction |
|---|---:|---:|---:|
| Successful edits | 4127 | 2138 | 48.2% |
| All edits | 4149 | 2483 | 40.2% |

The machine-readable evidence remains in `results.jsonl`, `control-metrics.json`, and `hpatch-metrics.json`, plus detailed `artifacts/`. The summary intentionally omits session, thread, tool-call, and correlation identifiers.
