# Benchmark report — commit `e7100e5fc5b76b326fc97a930e2ebacac1e5ae30`

Task: `etcd-range-stream`  
Configuration: `gpt-5.6-sol`, medium reasoning, 2 repetitions.

## Per repetition

| Rep | Order | Control result | Hpatch result | Control search errors | Hpatch search errors | Hread errors | Translation envelope errors | Wrapper errors |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | hpatch → control | PASS — 312.262 s; 79424 uncached input; 9921 output; 2199 reasoning; grader 5.150 s | PASS — 281.697 s; 84143 uncached input; 6583 output; 1680 reasoning; grader 5.533 s | 1 | 1 | 0 | 0 | 0 |
| 2 | control → hpatch | PASS — 318.594 s; 78189 uncached input; 9673 output; 1809 reasoning; grader 4.676 s | PASS — 345.070 s; 89777 uncached input; 7706 output; 1811 reasoning; grader 4.539 s | 0 | 6 | 0 | 0 | 0 |

## Per-repetition interactions

| Rep | Control requests | Hpatch requests | Excess requests | Control command execs | Hpatch command execs | Hread calls | Control file changes | Hpatch file changes | Hpatch translations | Hpatch rejections | Diagnostic tokens |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 25 | 31 | 6 | 16 | 20 | 10 | 4 | 5 | 5 | 0 | 0 |
| 2 | 23 | 37 | 14 | 15 | 36 | 10 | 2 | 6 | 6 | 0 | 0 |

## Aggregate outcome

| Measure | Control | Hpatch | Difference |
|---|---:|---:|---:|
| Task / grader pass rate | 2/2 | 2/2 | Equal |
| Agent wall time | 630.856s | 626.767s | -4.089s |
| Mean agent wall time | 315.428s | 313.3835s | -2.0445s |
| Grader time | 9.826s | 10.072s | 0.246s |
| Total input tokens | 2647981 | 3306848 | 658867 |
| Uncached input tokens | 157613 | 173920 | 16307 |
| Output tokens | 19594 | 14289 | -5305 |
| Reasoning tokens | 4008 | 3491 | -517 |

## Agent interaction metrics

| Arm | Model requests | Command executions | Hread calls | Client file-change items | Routed Hpatch translations | Routed Hpatch rejections | Diagnostic tokens |
|---|---:|---:|---:|---:|---:|---:|---:|
| Control | 48 | 31 | 0 | 6 | — | — | — |
| Hpatch | 68 | 56 | 20 | 11 | 11 | 0 | 0 |

Hread calls are a subset of command executions. Client file-change items are completed Codex events; routed translations and rejections are server-side HPATCH outcomes.

## Hpatch attempt analysis

| Measure | Result |
|---|---:|
| Retained attempts | 11/11 routed calls |
| Call rejection rate | 0/11 (0%) |
| Indexed correction adoption | 0/0 rejected calls (n/a) |
| Value-row correction use | 0/0 indexed corrections (n/a); 0 row operations |
| Recovered rejection chains | 0/0 |
| Failed-payload share | 0/4887 tokens (0%) |
| Break-even failed-payload budget | 3689 tokens |
| Current failed payload | 0 tokens (3689 under budget) |

### Attempt sequence

| Rep | Sequence | Chain | Call | Attempt | Payload | Outcome | Value-row ops | Base body rows | Base command tokens | Evaluated commands | Hpatch tokens | Apply-patch baseline | Diagnostic tokens | Rejection evidence |
|---:|---:|---|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---|
| 1 | 1 | call_sqFFRsnRTDzHdbUhDdgL6dMj | call_sqFFRsnRTDzHdbUhDdgL6dMj | 1 | complete | successful | 0 | 0 | 0 | 6 | 268 | 699 | 0 | — |
| 1 | 2 | call_0CNuFKncOjfX3A5rpZPB95oT | call_0CNuFKncOjfX3A5rpZPB95oT | 1 | complete | successful | 0 | 0 | 0 | 2 | 180 | 271 | 0 | — |
| 1 | 3 | call_9DYRBS47XiE4kV49kkc70CAv | call_9DYRBS47XiE4kV49kkc70CAv | 1 | complete | successful | 0 | 0 | 0 | 5 | 446 | 721 | 0 | — |
| 1 | 4 | call_2B99d544OPCANkVPDIG4N5sm | call_2B99d544OPCANkVPDIG4N5sm | 1 | complete | successful | 0 | 0 | 0 | 4 | 1035 | 1562 | 0 | — |
| 1 | 5 | call_Agm91nrrcji8S2sJxEHRSfwQ | call_Agm91nrrcji8S2sJxEHRSfwQ | 1 | complete | successful | 0 | 0 | 0 | 11 | 408 | 815 | 0 | — |
| 2 | 1 | call_AXndjTCbabmeKnvD3hdHSdZM | call_AXndjTCbabmeKnvD3hdHSdZM | 1 | complete | successful | 0 | 0 | 0 | 6 | 268 | 699 | 0 | — |
| 2 | 2 | call_LST1UDWOmacznRZJhPxI1uYD | call_LST1UDWOmacznRZJhPxI1uYD | 1 | complete | successful | 0 | 0 | 0 | 13 | 582 | 1133 | 0 | — |
| 2 | 3 | call_NVWI2EXK55GChxP2CeIwG501 | call_NVWI2EXK55GChxP2CeIwG501 | 1 | complete | successful | 0 | 0 | 0 | 5 | 446 | 721 | 0 | — |
| 2 | 4 | call_HM60MnRjAw3W17P1XLeDSP3L | call_HM60MnRjAw3W17P1XLeDSP3L | 1 | complete | successful | 0 | 0 | 0 | 4 | 385 | 678 | 0 | — |
| 2 | 5 | call_Q4k0fQ3z9mHJ1tdoEjwQQD3Q | call_Q4k0fQ3z9mHJ1tdoEjwQQD3Q | 1 | complete | successful | 0 | 0 | 0 | 2 | 673 | 947 | 0 | — |
| 2 | 6 | call_cwe4m3F4r8rtXEcHvoKtztVe | call_cwe4m3F4r8rtXEcHvoKtztVe | 1 | complete | successful | 0 | 0 | 0 | 2 | 196 | 330 | 0 | — |

Attempt telemetry is bounded and contains no script, replacement text, diagnostic body, or repair context.

## Hpatch rejection evidence

No evaluator rejections.

Evidence contains evaluator-owned command identity, multiline value row, and generated Go position only; scripts, replacement text, diagnostics, and repair context are not retained.

## Editing efficiency

| Measure | Control-equivalent | Hpatch | Change |
|---|---:|---:|---:|
| Successful edit payload | 8576 | 4887 | 43.0% reduction |
| All edit payload | 8576 | 4887 | 43.0% reduction |
| End-to-end agent output | 19594 | 14289 | -27.1% |
| Estimated non-edit output | 11018 | 9402 | -14.7% |

Estimated non-edit output subtracts each edit payload estimate from that arm total. It is a semantic comparison, not direct attribution of the control arm emitted patch tokens.

## Router metrics

| Arm | Started | Completed | Failed | Canceled | Timed out | Usage observed | Usage missing | Total duration | Upstream duration |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Control | 48 | 48 | 0 | 0 | 0 | 48 | 0 | 556.861 s | 556.786 s |
| Hpatch | 68 | 68 | 0 | 0 | 0 | 68 | 0 | 554.272 s | 553.749 s |

## Hpatch gain and patch errors

```text
output token estimates:
calls       hpatch  apply_patch  reduction
-----       ------  -----------  ---------
successful  4887    8576         43.0%
failed      0       0            n/a
all         4887    8576         43.0%
failed apply_patch output uses the empty-patch semantic baseline.

input token estimates:
source                          tokens  description
------------------------------  ------  ----------------------------------------
state reports                   932     final state returned after successful
                                        calls
failure diagnostics             0       errors and repair context returned after
                                        failed calls
hpatch definition installed     4552    hpatch and hread tool definitions added
                                        by the router
apply_patch definition removed  -116    exact Code Mode section removed by the
                                        router
net added input                 5368    measured additions minus the removed
                                        definition
definition routing covers 68 accounted request(s) in 2 distinct session(s)
(installation and removal measured).

command metrics:
command  invocations  errors  error rate
-------  -----------  ------  ----------
in       18           0       0.0%
new      0            0       0.0%
mv       0            0       0.0%
rm       0            0       0.0%
type     10           0       0.0%
type-    2            0       0.0%
type+    30           0       0.0%
del      0            0       0.0%
total    60           0       0.0%

target metrics:
target         invocations  errors  error rate
-------        -----------  ------  ----------
line           40           0       0.0%
range          2            0       0.0%
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
