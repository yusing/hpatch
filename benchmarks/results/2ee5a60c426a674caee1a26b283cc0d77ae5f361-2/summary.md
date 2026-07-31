# Benchmark report — commit `2ee5a60c426a674caee1a26b283cc0d77ae5f361`

Task: `etcd-range-stream`  
Configuration: `gpt-5.6-sol`, medium reasoning, 4 repetitions.

## Per repetition

| Rep | Order | Control result | Hpatch result | Hpatch rejections | Wrapper errors |
|---:|---|---|---|---:|---:|
| 1 | hpatch → control | PASS — 416.822 s; 85567 uncached input; 10267 output; 2145 reasoning; grader 4.331 s | PASS — 427.986 s; 102004 uncached input; 12108 output; 4732 reasoning; grader 4.808 s | 0 | 10 |
| 2 | control → hpatch | PASS — 351.588 s; 109912 uncached input; 10000 output; 2903 reasoning; grader 4.756 s | PASS — 444.156 s; 121845 uncached input; 12427 output; 4462 reasoning; grader 4.171 s | 1 | 19 |
| 3 | hpatch → control | PASS — 329.111 s; 92315 uncached input; 10112 output; 2514 reasoning; grader 3.919 s | PASS — 400.397 s; 113114 uncached input; 12417 output; 3808 reasoning; grader 7.499 s | 3 | 14 |
| 4 | control → hpatch | PASS — 325.795 s; 84858 uncached input; 10670 output; 3407 reasoning; grader 9.582 s | PASS — 524.843 s; 126139 uncached input; 13386 output; 5137 reasoning; grader 4.630 s | 4 | 20 |

## Aggregate outcome

| Measure | Control | Hpatch | Difference |
|---|---:|---:|---:|
| Task / grader pass rate | 4/4 | 4/4 | Equal |
| Agent wall time | 1423.316s | 1797.382s | 374.066s |
| Mean agent wall time | 355.829s | 449.3455s | 93.5165s |
| Grader time | 22.588s | 21.108s | -1.48s |
| Total input tokens | 6320556 | 10635262 | 4314706 |
| Uncached input tokens | 372652 | 463102 | 90450 |
| Output tokens | 41049 | 50338 | 9289 |
| Reasoning tokens | 10969 | 18139 | 7170 |

## Router metrics

| Arm | Started | Completed | Failed | Canceled | Timed out | Usage observed | Usage missing | Total duration | Upstream duration |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Control | 120 | 119 | 1 | 0 | 0 | 119 | 1 | 1247.664 s | 1247.480 s |
| Hpatch | 170 | 170 | 0 | 0 | 0 | 170 | 0 | 1624.777 s | 1622.722 s |

## Hpatch gain and patch errors

```text
output token estimates:
calls       hpatch  apply_patch  reduction
-----       ------  -----------  ---------
successful  5288    19355        72.7%
failed      11577   1276         n/a
all         16865   20631        18.3%
failed apply_patch output uses the empty-patch semantic baseline.

input token estimates:
source                          tokens  description
------------------------------  ------  ----------------------------------------
state reports                   1100    final state returned after successful
                                        calls
failure diagnostics             1032    errors and repair context returned after
                                        failed calls
hread results                   61440   hashline-formatted file content returned
                                        by hread
equivalent cat results          -40061  raw file content displaced by hread
hpatch definition installed     10076   hpatch and hread tool definitions added
                                        by the router
apply_patch definition removed  -1172   exact Code Mode section removed by the
                                        router
net added input                 32415   measured additions minus equivalent cat
                                        results and the removed definition
definition routing covers 170 accounted request(s) in 4 distinct session(s)
(installation and removal measured).

command metrics:
command  invocations  errors  error rate
-------  -----------  ------  ----------
in       83           0       0.0%
new      1            0       0.0%
mv       0            0       0.0%
rm       1            0       0.0%
tsel     154          1       0.6%
rsel     31           3       9.7%
type     178          2       1.1%
del      5            0       0.0%
copy     66           0       0.0%
cut      0            0       0.0%
paste    65           2       3.1%
commit   1            0       0.0%
total    585          8       1.4%

tsel selection metrics:
selection  invocations  errors  error rate
-------    -----------  ------  ----------
single     154          1       0.6%
multiple   0            0       0.0%

failure reasons:
reason              errors
------              ------
syntax              2
coordinate-bounds   3
occurrence-missing  1
invalid-count       0
order-or-overlap    0
edit-conflict       2
active-file         0
selection-required  0
clipboard-empty     0
file-missing        0
file-conflict       0
path                0
other               0
total               8

command failure reasons:
command  reason              errors
-------  ------              ------
tsel     occurrence-missing  1
rsel     coordinate-bounds   3
type     edit-conflict       2
paste    syntax              2
```

The command-error and failure-reason totals above are collected by the Hpatch router. “Wrapper errors” are the `apply_patch verification failed` envelope entries in Hpatch agent stderr; they are reported separately because they are not equivalent to Hpatch command errors.
