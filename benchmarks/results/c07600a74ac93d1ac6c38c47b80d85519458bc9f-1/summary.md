# Benchmark report — commit `c07600a74ac93d1ac6c38c47b80d85519458bc9f`

Task: `etcd-range-stream`  
Configuration: `gpt-5.6-sol`, medium reasoning, 1 repetitions.

## Per repetition

| Rep | Order | Control result | Hpatch result | Control search errors | Hpatch search errors | Hread errors | Translation envelope errors | Wrapper errors |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | hpatch → control | PASS — 392.352 s; 136693 uncached input; 12765 output; 5906 reasoning; grader 26.818 s | PASS — 405.199 s; 82680 uncached input; 11469 output; 5991 reasoning; grader 4.595 s | 0 | 1 | 0 | 0 | 0 |

## Per-repetition interactions

| Rep | Control requests | Hpatch requests | Excess requests | Control command execs | Hpatch command execs | Hread calls | Control file changes | Hpatch file changes | Hpatch translations | Hpatch rejections | Diagnostic tokens |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 22 | 29 | 7 | 12 | 19 | 8 | 5 | 4 | 4 | 1 | 541 |

## Aggregate outcome

| Measure | Control | Hpatch | Difference |
|---|---:|---:|---:|
| Task / grader pass rate | 1/1 | 1/1 | Equal |
| Agent wall time | 392.352s | 405.199s | 12.847s |
| Mean agent wall time | 392.352s | 405.199s | 12.847s |
| Grader time | 26.818s | 4.595s | -22.223s |
| Total input tokens | 1018357 | 1401592 | 383235 |
| Uncached input tokens | 136693 | 82680 | -54013 |
| Output tokens | 12765 | 11469 | -1296 |
| Reasoning tokens | 5906 | 5991 | 85 |

## Agent interaction metrics

| Arm | Model requests | Command executions | Hread calls | Client file-change items | Routed Hpatch translations | Routed Hpatch rejections | Diagnostic tokens |
|---|---:|---:|---:|---:|---:|---:|---:|
| Control | 22 | 12 | 0 | 5 | — | — | — |
| Hpatch | 29 | 19 | 8 | 4 | 4 | 1 | 541 |

Hread calls are a subset of command executions. Client file-change items are completed Codex events; routed translations and rejections are server-side HPATCH outcomes.

## Hpatch attempt analysis

| Measure | Result |
|---|---:|
| Retained attempts | 5/5 routed calls |
| Call rejection rate | 1/5 (20%) |
| Indexed correction adoption | 0/1 rejected calls (0%) |
| Value-row correction use | 0/0 indexed corrections (n/a); 0 row operations |
| Recovered rejection chains | 0/1 |
| Failed-payload share | 345/2483 tokens (13.9%) |
| Break-even failed-payload budget | 2011 tokens |
| Current failed payload | 345 tokens (1666 under budget) |

### Attempt sequence

| Rep | Sequence | Chain | Call | Attempt | Payload | Outcome | Value-row ops | Base body rows | Base command tokens | Evaluated commands | Hpatch tokens | Apply-patch baseline | Diagnostic tokens | Rejection evidence |
|---:|---:|---|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---|
| 1 | 1 | call_HjYI81rIHaLLLWgONQ0p0WUX | call_HjYI81rIHaLLLWgONQ0p0WUX | 1 | complete | successful | 0 | 0 | 0 | 9 | 1066 | 1945 | 0 | — |
| 1 | 2 | call_xfJeul46pYTbgX8NCgd2k3Bb | call_xfJeul46pYTbgX8NCgd2k3Bb | 1 | complete | successful | 0 | 0 | 0 | 14 | 685 | 1336 | 0 | — |
| 1 | 3 | call_EMcMVlTXsH9P0dT0xdO1y4Fx | call_EMcMVlTXsH9P0dT0xdO1y4Fx | 1 | complete | rejected | 0 | 0 | 0 | 3 | 345 | 22 | 541 | command 3 · script line 10 · type/range · row-stale · server/etcdserver/api/v3rpc/key.go |
| 1 | 4 | call_jcjG0UTVwmThxFeIsAzragHB | call_jcjG0UTVwmThxFeIsAzragHB | 1 | complete | successful | 0 | 0 | 0 | 11 | 343 | 708 | 0 | — |
| 1 | 5 | call_xzzdEZ7wihu8oNtLjfyWj0rK | call_xzzdEZ7wihu8oNtLjfyWj0rK | 1 | complete | successful | 0 | 0 | 0 | 2 | 44 | 138 | 0 | — |

Attempt telemetry is bounded and contains no script, replacement text, diagnostic body, or repair context.

## Hpatch rejection evidence

| Rep | Command | Source line | Operation | Target | Value row | Reason | Path | Generated line | Generated column |
|---:|---:|---:|---|---|---:|---|---|---:|---:|
| 1 | 3 | 10 | type | range | — | row-stale | server/etcdserver/api/v3rpc/key.go | — | — |

Evidence contains evaluator-owned command identity, multiline value row, and generated Go position only; scripts, replacement text, diagnostics, and repair context are not retained.

## Editing efficiency

| Measure | Control-equivalent | Hpatch | Change |
|---|---:|---:|---:|
| Successful edit payload | 4127 | 2138 | 48.2% reduction |
| All edit payload | 4149 | 2483 | 40.2% reduction |
| End-to-end agent output | 12765 | 11469 | -10.2% |
| Estimated non-edit output | 8616 | 8986 | +4.3% |

Estimated non-edit output subtracts each edit payload estimate from that arm total. It is a semantic comparison, not direct attribution of the control arm emitted patch tokens.

## Router metrics

| Arm | Started | Completed | Failed | Canceled | Timed out | Usage observed | Usage missing | Total duration | Upstream duration |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Control | 22 | 22 | 0 | 0 | 0 | 22 | 0 | 318.564 s | 318.530 s |
| Hpatch | 29 | 29 | 0 | 0 | 0 | 29 | 0 | 337.419 s | 337.199 s |

## Hpatch gain and patch errors

```text
output token estimates:
calls       hpatch  apply_patch  reduction
-----       ------  -----------  ---------
successful  2138    4127         48.2%
failed      345     22           n/a
all         2483    4149         40.2%
failed apply_patch output uses the empty-patch semantic baseline.

input token estimates:
source                          tokens  description
------------------------------  ------  ----------------------------------------
state reports                   322     final state returned after successful
                                        calls
failure diagnostics             541     errors and repair context returned after
                                        failed calls
hpatch definition installed     2118    hpatch and hread tool definitions added
                                        by the router
apply_patch definition removed  -58     exact Code Mode section removed by the
                                        router
net added input                 2923    measured additions minus the removed
                                        definition
definition routing covers 29 accounted request(s) in 1 distinct session(s)
(installation and removal measured).

command metrics:
command  invocations  errors  error rate
-------  -----------  ------  ----------
in       13           0       0.0%
new      0            0       0.0%
mv       0            0       0.0%
rm       0            0       0.0%
type     13           1       7.7%
type-    0            0       0.0%
type+    13           0       0.0%
total    39           1       2.6%

target metrics:
target         invocations  errors  error rate
-------        -----------  ------  ----------
line           21           0       0.0%
range          5            1       20.0%
text-single    0            0       0.0%
text-multiple  0            0       0.0%

failure reasons:
reason              errors
------              ------
script-syntax       0
row-missing         0
row-stale           1
occurrence-missing  0
invalid-count       0
target-order        0
edit-conflict       0
active-file         0
initialization      0
file-path           0
language-syntax     0
other               0
total               1

command failure reasons:
command  reason     errors
-------  ------     ------
type     row-stale  1
```

The command-error and failure-reason totals above are collected by the Hpatch router. “Search errors” count failed Codex command executions containing rg, grep, find, fd, or search_code; “Hread errors” count failed Codex command executions identified as Hread invocations. “Translation envelope errors” count client stderr envelopes and are not Hpatch command rejections; “Wrapper errors” are the `apply_patch verification failed` envelope entries in Hpatch agent stderr; they are reported separately because they are not equivalent to Hpatch command errors.
