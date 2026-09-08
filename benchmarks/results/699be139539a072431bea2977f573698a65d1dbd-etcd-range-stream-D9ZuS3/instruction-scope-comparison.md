# Tool-focused instruction rebenchmark

One `hpatch-diagnostic` attempt on `etcd-range-stream`, using `gpt-6-astra` at `low`
effort with CTP/2 and Codex 0.153.4. Issue reporting and Mentor Handoff were disabled;
edit-loop enforcement remained enabled. The attempt, hidden grader, expected final response,
edit-loop enforcement, and capture validation passed. No additional attempt was launched.

## Instruction boundary

Retained guidance explains the tools and their effective use: batch all ready related edits
atomically, reuse verified targets and confirmed mappings, choose insertion/replacement forms,
leave formatting to the engine, use target-bearing reading tools, and submit shell scripts and
commentary through their supported interfaces.

Removed guidance prescribed task-wide autonomy or approval checkpoints, commentary length,
general correctness checks and validation stopping rules, or prohibited inspecting changed files.
Target reuse still avoids reads made solely to reacquire an already available target. Both
workflow variants omit the explanation about translated apply_patch execution history.

The same host/task instructions remain responsible for general workflow policy. The tool-specific
wording is not a claim that the benchmark isolates the engine from all usage guidance.

## Results

| Metric | Before split: bdfe0658 | Previous split: DKo4uv | Revised: D9ZuS3 |
|---|---:|---:|---:|
| Task passes | 1/1 | 1/1 | 1/1 |
| Agent time, seconds | 280.613 | 300.811 | 241.510 |
| Requests / provider attempts | 19 / 19 | 18 / 18 | 15 / 15 |
| Input tokens | 614,247 | 520,435 | 494,516 |
| Cached input tokens | 573,184 | 454,784 | 435,200 |
| Uncached input tokens | 41,063 | 65,651 | 59,316 |
| Cache rate | 93.31% | 87.39% | 88.01% |
| Output tokens | 3,816 | 3,682 | 3,687 |
| Reasoning tokens | 738 | 683 | 630 |
| Hpatch calls | 4 | 3 | 2 |
| Rejections / corrections | 0 / 0 | 0 / 0 | 0 / 0 |

Compared with the previous split: agent time fell 19.71%, input fell 4.98%, uncached input fell
9.65%, and requests fell 16.67%. Output was essentially unchanged (+0.14%). The revised run used
two successful Hpatch calls instead of three.

All runs share the task-content fingerprint, stock base-instruction hash, model, effort, protocol,
and Codex release. They are single historical diagnostic runs, not a fresh paired A/B experiment.
They do not isolate causation or establish a repeatable performance gain. In the revised run,
request 3 reported zero cached tokens on 28,139 input tokens despite an append-only observed
prefix. Cache variability remains material; no provider-side cause is established here.

## Provenance and validation

The image was built from the uncommitted workspace. [commit.txt](commit.txt) identifies its base;
[workspace-changes.patch](workspace-changes.patch) records the working-tree changes before launch.
[Instruction source snapshots](instructions/experiment-source/) were captured before the image
build and verified unchanged at completion. They include both workflow files and the renderer.
The hook-timeout fix was already present in the preceding split run and was not changed here.

All 52 focused instruction/router tests passed before launch. Whitespace checks passed.

Sources: [current summary](summary.md), [current configuration](benchmark-config.json),
[previous split summary](../699be139539a072431bea2977f573698a65d1dbd-etcd-range-stream-DKo4uv/summary.md),
[before-split summary](../bdfe06580653b6b8cc6dc55fa5df6355ee9a8b64-etcd-range-stream-QsCKN9/summary.md).
