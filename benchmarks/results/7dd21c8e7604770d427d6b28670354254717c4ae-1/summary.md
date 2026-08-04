# Benchmark report — commit `7dd21c8e7604770d427d6b28670354254717c4ae`

Task: `etcd-range-stream`  
Configuration: `gpt-5.6-sol`, medium reasoning, 1 repetitions.

## Per repetition

| Rep | Order | Control result | Hpatch result | Control search errors | Hpatch search errors | Hread errors | Translation envelope errors | Wrapper errors |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | hpatch → control | FAIL — 525.431 s; 76112 uncached input; 14556 output; 6012 reasoning; grader 4.726 s | PASS — 473.789 s; 97383 uncached input; 13298 output; 7583 reasoning; grader 5.095 s | 2 | 0 | 0 | 0 | 0 |

## Per-repetition interactions

| Rep | Control requests | Hpatch requests | Excess requests | Control command execs | Hpatch command execs | Hread calls | Control file changes | Hpatch file changes | Hpatch translations | Hpatch rejections | Diagnostic tokens |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 31 | 47 | 16 | 14 | 30 | 15 | 7 | 10 | 10 | 0 | 0 |

## Aggregate outcome

| Measure | Control | Hpatch | Difference |
|---|---:|---:|---:|
| Task / grader pass rate | 0/1 | 1/1 |  |
| Agent wall time | 525.431s | 473.789s | -51.642s |
| Mean agent wall time | 525.431s | 473.789s | -51.642s |
| Grader time | 4.726s | 5.095s | 0.369s |
| Total input tokens | 1402448 | 2286183 | 883735 |
| Uncached input tokens | 76112 | 97383 | 21271 |
| Output tokens | 14556 | 13298 | -1258 |
| Reasoning tokens | 6012 | 7583 | 1571 |

## Agent interaction metrics

| Arm | Model requests | Command executions | Hread calls | Client file-change items | Routed Hpatch translations | Routed Hpatch rejections | Diagnostic tokens |
|---|---:|---:|---:|---:|---:|---:|---:|
| Control | 31 | 14 | 0 | 7 | — | — | — |
| Hpatch | 47 | 30 | 15 | 10 | 10 | 0 | 0 |

Hread calls are a subset of command executions. Client file-change items are completed Codex events; routed translations and rejections are server-side HPATCH outcomes.

## Hpatch attempt analysis

| Measure | Result |
|---|---:|
| Retained attempts | 10/10 routed calls |
| Call rejection rate | 0/10 (0%) |
| Indexed correction adoption | 0/0 rejected calls (n/a) |
| Value-row correction use | 0/0 indexed corrections (n/a); 0 row operations |
| Recovered rejection chains | 0/0 |
| Failed-payload share | 0/2312 tokens (0%) |
| Break-even failed-payload budget | 2153 tokens |
| Current failed payload | 0 tokens (2153 under budget) |

### Attempt sequence

| Rep | Sequence | Chain | Call | Attempt | Payload | Outcome | Value-row ops | Base body rows | Base command tokens | Evaluated commands | Hpatch tokens | Apply-patch baseline | Diagnostic tokens | Rejection evidence |
|---:|---:|---|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---|
| 1 | 1 | call_b6EaI9Hz0VdlRlPznegJMhsr | call_b6EaI9Hz0VdlRlPznegJMhsr | 1 | complete | successful | 0 | 0 | 0 | 6 | 265 | 658 | 0 | — |
| 1 | 2 | call_sEtuZDXmQfpdH8RoOYOlfley | call_sEtuZDXmQfpdH8RoOYOlfley | 1 | complete | successful | 0 | 0 | 0 | 3 | 846 | 1286 | 0 | — |
| 1 | 3 | call_CRlMVXJ5a6Vs8MH2S12xlk5A | call_CRlMVXJ5a6Vs8MH2S12xlk5A | 1 | complete | successful | 0 | 0 | 0 | 3 | 270 | 462 | 0 | — |
| 1 | 4 | call_zZ8oaaeUzOltXpl6jhG4w2ai | call_zZ8oaaeUzOltXpl6jhG4w2ai | 1 | complete | successful | 0 | 0 | 0 | 2 | 79 | 184 | 0 | — |
| 1 | 5 | call_mkiJIynl7vpdjD9QYk2v46Zi | call_mkiJIynl7vpdjD9QYk2v46Zi | 1 | complete | successful | 0 | 0 | 0 | 3 | 103 | 232 | 0 | — |
| 1 | 6 | call_CBlk9liO4vlWZ6C1zlBPN6dN | call_CBlk9liO4vlWZ6C1zlBPN6dN | 1 | complete | successful | 0 | 0 | 0 | 3 | 128 | 282 | 0 | — |
| 1 | 7 | call_666UlVrpIvRlXi1JqMKMdwjn | call_666UlVrpIvRlXi1JqMKMdwjn | 1 | complete | successful | 0 | 0 | 0 | 5 | 276 | 520 | 0 | — |
| 1 | 8 | call_VL3VWwNvdfdmOTY4WneCEaGf | call_VL3VWwNvdfdmOTY4WneCEaGf | 1 | complete | successful | 0 | 0 | 0 | 5 | 181 | 510 | 0 | — |
| 1 | 9 | call_HiqcaZJ7UlhYNOLEVGU98oUf | call_HiqcaZJ7UlhYNOLEVGU98oUf | 1 | complete | successful | 0 | 0 | 0 | 3 | 100 | 152 | 0 | — |
| 1 | 10 | call_wTjKWpAuKlp3RTVQQfSmYDU8 | call_wTjKWpAuKlp3RTVQQfSmYDU8 | 1 | complete | successful | 0 | 0 | 0 | 2 | 64 | 179 | 0 | — |

Attempt telemetry is bounded and contains no script, replacement text, diagnostic body, or repair context.

## Hpatch rejection evidence

No evaluator rejections.

Evidence contains evaluator-owned command identity, multiline value row, and generated Go position only; scripts, replacement text, diagnostics, and repair context are not retained.

## Editing efficiency

| Measure | Control-equivalent | Hpatch | Change |
|---|---:|---:|---:|
| Successful edit payload | 4465 | 2312 | 48.2% reduction |
| All edit payload | 4465 | 2312 | 48.2% reduction |
| End-to-end agent output | 14556 | 13298 | -8.6% |
| Estimated non-edit output | 10091 | 10986 | +8.9% |

Estimated non-edit output subtracts each edit payload estimate from that arm total. It is a semantic comparison, not direct attribution of the control arm emitted patch tokens.

## Router metrics

| Arm | Started | Completed | Failed | Canceled | Timed out | Usage observed | Usage missing | Total duration | Upstream duration |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Control | 31 | 31 | 0 | 0 | 0 | 31 | 0 | 424.679 s | 424.636 s |
| Hpatch | 47 | 47 | 0 | 0 | 0 | 47 | 0 | 381.299 s | 380.955 s |

## Hpatch gain and patch errors

```text
output token estimates:
calls       hpatch  apply_patch  reduction
-----       ------  -----------  ---------
successful  2312    4465         48.2%
failed      0       0            n/a
all         2312    4465         48.2%
failed apply_patch output uses the empty-patch semantic baseline.

input token estimates:
source                          tokens  description
------------------------------  ------  ----------------------------------------
state reports                   853     final state returned after successful
                                        calls
failure diagnostics             0       errors and repair context returned after
                                        failed calls
hpatch definition installed     2276    hpatch and hread tool definitions added
                                        by the router
apply_patch definition removed  -58     exact Code Mode section removed by the
                                        router
net added input                 3071    measured additions minus the removed
                                        definition
definition routing covers 47 accounted request(s) in 1 distinct session(s)
(installation and removal measured).

command metrics:
command  invocations  errors  error rate
-------  -----------  ------  ----------
in       10           0       0.0%
new      0            0       0.0%
mv       0            0       0.0%
rm       0            0       0.0%
type     10           0       0.0%
type-    3            0       0.0%
type+    12           0       0.0%
del      0            0       0.0%
total    35           0       0.0%

target metrics:
target         invocations  errors  error rate
-------        -----------  ------  ----------
line           20           0       0.0%
range          5            0       0.0%
text-single    0            0       0.0%
text-multiple  0            0       0.0%

failure reasons:
reason              errors
------              ------
script-syntax       0
row-missing         0
row-stale           0
occurrence-missing  0
invalid-count       0
target-order        0
edit-conflict       0
active-file         0
initialization      0
file-path           0
language-syntax     0
other               0
total               0

command failure reasons:
command  reason  errors
-------  ------  ------
none     none    0
```

The command-error and failure-reason totals above are collected by the Hpatch router. “Search errors” count failed Codex command executions containing rg, grep, find, fd, or search_code; “Hread errors” count failed executions of the routed private Hread wrapper. “Translation envelope errors” count client stderr envelopes and are not Hpatch command rejections; “Wrapper errors” are the `apply_patch verification failed` envelope entries in Hpatch agent stderr; they are reported separately because they are not equivalent to Hpatch command errors.
