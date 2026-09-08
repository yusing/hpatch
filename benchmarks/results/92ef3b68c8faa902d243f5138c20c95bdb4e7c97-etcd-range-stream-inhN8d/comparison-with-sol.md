# Astra-low versus previous Sol-medium runs

One new Astra-low Hpatch + CTP/2 attempt completed. The task grader, capture validation,
and enabled no-edit-loop acceptance passed; runner exit status was 0. All four Hpatch
deliveries succeeded, with zero rejections or corrections.

Historical reference: `2d848429192378afcab5766c8b91857fb0e87c58-etcd-range-stream-ttPygV`.
Both runs used the same task-content fingerprint, Codex 0.153.4, CTP/2 treatment protocol,
disabled issue reporting, and enabled no-edit-loop checks. The model/effort, model-specific
base instructions, router revision, and execution time differ. This is a descriptive
comparison, not a controlled measurement of the routing fix. Older Sol captures lack the
current metric schema or task fingerprint and are excluded from numeric comparison.

| Measure | Astra low Hpatch + CTP/2 | Sol medium Hpatch + CTP/2 | Sol medium stock |
|---|---:|---:|---:|
| Task grader | Pass | Pass | Pass |
| Overall run | Pass | Failed no-edit-loop acceptance | Part of previous pair |
| Agent seconds | 265.438 | 705.397 | 902.957 |
| Requests | 19 | 28 | 34 |
| Input tokens | 625,466 | 1,367,060 | 1,365,579 |
| Cached input | 547,584 | 1,287,552 | 1,306,112 |
| Uncached input | 77,882 | 79,508 | 59,467 |
| Output tokens | 3,447 | 11,948 | 15,240 |
| Reasoning tokens | 515 | 5,630 | 6,413 |
| Provider cache rate | 87.55% | 94.18% | 95.65% |

Versus the Sol Hpatch treatment, Astra used 62.37% less agent time, 54.25% less total input,
71.15% less output, but only 2.05% less uncached input.

## Routing fix and remaining cache miss

The first request had no turn-state token, as expected. All 18 continuation requests
preserved the client-issued header unchanged at the provider boundary. All comparable
prefixes were append-stable; session and body cache keys were stable.

Request 2 reused 15,616 cached tokens after request 1 had 15,725 input, unlike the cold
second request in the previous Sol treatment. However, request 6 had 33,084 input and
zero cached tokens after request 5 had 32,909 input. It preserved turn state and had
stable observed prefixes and keys. Thus the forwarding defect is fixed in live traffic,
but full cache misses persist. The retained evidence does not establish the remaining
provider-side cause or guarantee cache readiness, residency, or internal routing.

The reported repeated-prefix shortfall estimate is 39,479 tokens, of which request 6
contributes 32,909. This estimate uses previous input counts, not a measured hidden
model-token prefix. It must not be labeled as proven router-induced waste.

The initial native-mode preparation exited 137 during dependency setup before any model
attempt; it produced no measured result. Only the corrected CTP/2 attempt invoked a model.
