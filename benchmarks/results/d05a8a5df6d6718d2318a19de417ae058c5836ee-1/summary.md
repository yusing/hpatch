# Benchmark report — commit `d05a8a5df6d6718d2318a19de417ae058c5836ee`

Task: `etcd-range-stream`  
Configuration: `gpt-5.6-sol`, medium reasoning, 4 repetitions.

## Per repetition

| Rep | Order | Control result | Hpatch result | Control search errors | Hpatch search errors | Hread errors | Hpatch rejections | Wrapper errors |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | hpatch → control | PASS — 257.055 s; 67542 uncached input; 8093 output; 1673 reasoning; grader 4.862 s | PASS — 523.377 s; 119691 uncached input; 11425 output; 2619 reasoning; grader 4.399 s | 2 | 1 | 0 | 0 | 0 |
| 2 | control → hpatch | PASS — 316.621 s; 80483 uncached input; 9333 output; 2088 reasoning; grader 13.941 s | PASS — 301.011 s; 100368 uncached input; 9176 output; 1448 reasoning; grader 4.748 s | 1 | 0 | 0 | 0 | 0 |
| 3 | hpatch → control | PASS — 306.642 s; 86312 uncached input; 10123 output; 2509 reasoning; grader 4.669 s | PASS — 418.921 s; 119259 uncached input; 11321 output; 2771 reasoning; grader 4.917 s | 2 | 6 | 0 | 0 | 0 |
| 4 | control → hpatch | PASS — 351.656 s; 89737 uncached input; 11061 output; 3585 reasoning; grader 5.702 s | PASS — 421.602 s; 102941 uncached input; 10597 output; 3234 reasoning; grader 5.545 s | 1 | 7 | 0 | 0 | 0 |

## Aggregate outcome

| Measure | Control | Hpatch | Difference |
|---|---:|---:|---:|
| Task / grader pass rate | 4/4 | 4/4 | Equal |
| Agent wall time | 1231.974s | 1664.911s | 432.937s |
| Mean agent wall time | 307.9935s | 416.22775s | 108.23425s |
| Grader time | 29.174s | 19.609s | -9.565s |
| Total input tokens | 4155626 | 10048403 | 5892777 |
| Uncached input tokens | 324074 | 442259 | 118185 |
| Output tokens | 38610 | 42519 | 3909 |
| Reasoning tokens | 9855 | 10072 | 217 |

## Router metrics

| Arm | Started | Completed | Failed | Canceled | Timed out | Usage observed | Usage missing | Total duration | Upstream duration |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Control | 85 | 85 | 0 | 0 | 0 | 85 | 0 | 1068.498 s | 1068.302 s |
| Hpatch | 169 | 169 | 0 | 0 | 0 | 169 | 0 | 1423.920 s | 1422.337 s |

## Hpatch gain and patch errors

```text
output token estimates:
calls       hpatch  apply_patch  reduction
-----       ------  -----------  ---------
successful  7212    17686        59.2%
failed      9334    330          n/a
all         16546   18016        8.2%
failed apply_patch output uses the empty-patch semantic baseline.

input token estimates:
source                          tokens  description
------------------------------  ------  ----------------------------------------
state reports                   1687    final state returned after successful
                                        calls
failure diagnostics             1813    errors and repair context returned after
                                        failed calls
hpatch definition installed     10212   hpatch and hread tool definitions added
                                        by the router
apply_patch definition removed  -232    exact Code Mode section removed by the
                                        router
net added input                 13480   measured additions minus the removed
                                        definition
definition routing covers 169 accounted request(s) in 4 distinct session(s)
(installation and removal measured).

command metrics:
command  invocations  errors  error rate
-------  -----------  ------  ----------
in       61           0       0.0%
new      0            0       0.0%
mv       0            0       0.0%
rm       0            0       0.0%
tsel     142          2       1.4%
rsel     18           7       38.9%
type     148          4       2.7%
del      4            0       0.0%
copy     72           1       1.4%
cut      0            0       0.0%
paste    67           1       1.5%
commit   0            0       0.0%
total    512          15      2.9%

tsel selection metrics:
selection  invocations  errors  error rate
-------    -----------  ------  ----------
single     142          2       1.4%
multiple   0            0       0.0%

failure reasons:
reason              errors
------              ------
syntax              3
coordinate-bounds   6
occurrence-missing  0
invalid-count       0
order-or-overlap    0
edit-conflict       4
active-file         1
selection-required  1
clipboard-empty     0
file-missing        0
file-conflict       0
path                0
other               0
total               15

command failure reasons:
command  reason              errors
-------  ------              ------
tsel     edit-conflict       1
tsel     active-file         1
rsel     coordinate-bounds   6
rsel     edit-conflict       1
type     syntax              2
type     edit-conflict       2
copy     selection-required  1
paste    syntax              1
```

The command-error and failure-reason totals above are collected by the Hpatch router. “Search errors” count failed Codex command executions containing rg, grep, find, fd, or search_code; “Hread errors” count failed executions of the routed private Hread wrapper. “Wrapper errors” are the `apply_patch verification failed` envelope entries in Hpatch agent stderr; they are reported separately because they are not equivalent to Hpatch command errors.
