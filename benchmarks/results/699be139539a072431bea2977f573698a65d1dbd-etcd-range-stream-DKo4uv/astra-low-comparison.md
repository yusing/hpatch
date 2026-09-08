# Astra-low instruction experiment

One `hpatch-diagnostic` attempt on `etcd-range-stream`, using `gpt-6-astra`, `low`
reasoning, CTP/2, Codex 0.153.4, issue reporting disabled, and edit-loop enforcement enabled.
The new run passed the hidden grader, final-response check, edit-loop enforcement, and capture
validation. No additional attempt was launched.

## Historical comparison

| Metric | Earlier: 92ef3b68 | Previous: bdfe0658 | Current workspace |
|---|---:|---:|---:|
| Task passes | 1/1 | 1/1 | 1/1 |
| Agent time, seconds | 265.438 | 280.613 | 300.811 |
| Logical requests | 19 | 19 | 18 |
| Provider input tokens | 625,466 | 614,247 | 520,435 |
| Cached input tokens | 547,584 | 573,184 | 454,784 |
| Uncached input tokens | 77,882 | 41,063 | 65,651 |
| Cache rate | 87.55% | 93.31% | 87.39% |
| Output tokens | 3,447 | 3,816 | 3,682 |
| Reasoning tokens | 515 | 738 | 683 |
| Hpatch calls | 4 | 4 | 3 |
| Hpatch rejections / corrections | 0 / 0 | 0 / 0 | 0 / 0 |

Relative to the most recent previous run: input tokens fell 15.27%, output tokens fell 3.51%,
and Hpatch calls fell 25%. Agent time increased 7.20%, and uncached input increased 59.88%.
This is fewer total input tokens and edit calls, not demonstrated latency or cost improvement.

## Comparability and provenance

All three runs share the task-content fingerprint, stock base-instruction hash, model, reasoning
effort, protocol, and Codex release. They ran at different times and revisions, without a fresh
control arm. Cache behavior differs: current request 9 reports zero cached tokens despite stable
observed prefixes and preserved turn-state forwarding. That observation does not establish a
provider-side cause. One attempt cannot isolate the instruction change from run variability.

The current image was built from the uncommitted workspace, not the recorded Git commit alone.
It includes all six Astra workflow sections, shared technical references, request-local model
selection, and the hook-output timeout fix. The latter was validated separately by all 250
root-package tests; the instruction update passed 52 focused tests. No claim is made that the
hook fix affected this benchmark.

The exact instruction sources, including the two new workflow files absent from the tracked diff,
are retained under [experiment-source](instructions/experiment-source/). Other tracked changes
are retained in [workspace-changes.patch](workspace-changes.patch).

Sources: [current summary](summary.md),
[earlier summary](../92ef3b68c8faa902d243f5138c20c95bdb4e7c97-etcd-range-stream-inhN8d/summary.md),
[previous summary](../bdfe06580653b6b8cc6dc55fa5df6355ee9a8b64-etcd-range-stream-QsCKN9/summary.md).
