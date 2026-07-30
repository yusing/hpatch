# etcd fast keys range benchmark

- Run date: 2026-07-30
- Hpatch revision: `291804ffe24f1b56e8bc5f9742420a4fa4c83d98`
- Model: `gpt-5.6-luna`
- Repetitions: 3 per arm
- Worktree state: dirty; this run includes the uncommitted container Git `safe.directory` fix in `benchmarks/codex-compose.sh`
- Aggregate records: `results.jsonl`

## Outcome

| Repetition | First arm | Control | Hpatch |
| ---: | --- | --- | --- |
| 1 | hpatch | pass, 170.943 s | pass, 256.332 s |
| 2 | control | fail, 181.321 s | pass, 286.406 s |
| 3 | hpatch | pass, 230.085 s | pass, 251.910 s |
| Overall | | 2/3 (66.7%) | 3/3 (100%) |
| Mean agent duration | | 194.116 s | 264.883 s |

All six agents completed a model turn. No router log or agent artifact contains the original `hpatch rewrite requires exactly one usable workspace`, `dubious ownership`, or `Failed before model turn` diagnostic.

Control repetition 2 exited the agent successfully but failed the hidden grader because it introduced `KeysOnly` instead of the required `FastKeysOnly`. All six runs changed only the four allowed paths and reported no unauthorized paths. The benchmark command therefore exited 1 for a genuine control-arm grader failure.

## Usage

Codex turn-completion records:

| Arm | Input | Cached input | Output | Reasoning |
| --- | ---: | ---: | ---: | ---: |
| Control | 1,696,287 | 1,552,896 | 18,635 | 6,028 |
| Hpatch | 3,812,526 | 3,604,224 | 24,800 | 6,613 |

Router provider metrics:

| Arm | Input | Uncached input | Output | Reasoning |
| --- | ---: | ---: | ---: | ---: |
| Control | 1,395,633 | 108,977 | 13,768 | 4,572 |
| Hpatch | 848,744 | 36,456 | 5,842 | 657 |

The Codex and router measurements use materially different accounting boundaries. Do not combine them into a single token-efficiency conclusion without reconciling those semantics.

## Hpatch gain

- Successful output estimate: 6,524 hpatch tokens versus 9,339 `apply_patch` tokens, a 30.1% reduction.
- Output estimate including failed calls: 8,347 versus 9,405 tokens, an 11.2% reduction.
- Net added input: 5,538 tokens.
- Commands: 133 invocations, 3 rejections (2.3%); all three hpatch arms recovered and passed.
- Failure reasons: two coordinate-bound selections and one missing occurrence; no path failures.

See `gain.txt`, `hpatch-metrics.json`, and `control-metrics.json` for the complete measurements. Per-run model events, grader output, patches, and result records are under `artifacts/`.

