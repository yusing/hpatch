# Historical benchmark contract

## REQ-BENCH-001 — Containerized paired correctness evaluation

`benchmarks/bench.sh` owns the executable benchmark workflow. Dockerfile and
Docker Compose own the agent and router topology; the shell directly invokes
Docker, Codex, Git, the Go test grader, metrics endpoints, and `hpatch gain`.
No Go or Python benchmark runner or process-execution abstraction is part of the
benchmark.

Repetitions 1, 2, and 3 run concurrently. Within each repetition, one control
and one hpatch attempt run sequentially in randomized order. Both attempts use
the same pinned Codex image, model, prompt, historical etcd base, full-access
Codex sandbox setting, approval policy, timeout, authentication mount,
Codex-owned workspace metadata, and disposable-container setup.

The control uses the pinned CLI's stock bundled base instructions and the
passthrough router. The treatment replaces only the stock file-editing guidance
with `contrib/codex/file-editing-instructions.md` and uses the hpatch router.
The complete control and treatment instructions, their hashes, and their unified
diff are retained in the run directory and referenced by each result.

The active etcd task exports base revision
`27d5ef1b4da1d76ca8b9421d667e87d2fd2aba9d` and oracle revision
`dd57ad39fa4eb1afcd9abdadf75354bca0600d9f`. Before model execution, the shell
must prove that the hidden MVCC and transaction tests fail on the base with the
expected discriminator and pass on the oracle.

Every attempt receives a fresh history-free base repository without hidden
tests, oracle objects, or a remote. After Codex exits, the shell records changed
and untracked paths and writes the literal binary-capable diff. It then injects
the hidden tests and grades the attempt. Only the four declared MVCC and
transaction production paths may change.

An attempt passes only when Codex exits successfully without timeout, all
changed paths are allowed, Codex JSONL can be recorded, and the hidden grader
passes. Any failed hpatch attempt is fatal to the benchmark: after retaining its
result, the worker signals the parent, which terminates every active arm, starts
no further arm, waits for partial evidence capture, merges retained results,
collects available router evidence, and tears down Compose. User cancellation
uses the same evidence-preserving path. Other failures retain the available
diff, Codex diagnostics, grader diagnostics, and router evidence.

Each concurrent attempt writes one private JSON object containing arm and pair
order, selected base instruction artifact and digest, elapsed time, token usage,
Codex item counts and terminal error, changed and unauthorized paths, literal
diff and diff path, grader outcome, and the final pass decision. The parent
deterministically merges all six objects after a complete run or all retained
objects after fail-fast cancellation into `results.jsonl`.

After the paired attempts, the shell retains control and treatment router
metrics and an isolated treatment `hpatch gain` report. A gain of zero is not an
editing-performance result when no treatment request reached hpatch.
The generated summary joins each attempt's thread ID to router session metrics and reports
model requests, command executions, hread calls, client-visible file-change items, routed
hpatch translations and rejections, and rejected-call diagnostic tokens. It separately shows
semantic edit-payload reduction, end-to-end agent output change, and estimated non-edit
output so payload savings cannot be mistaken for whole-agent savings. A client stderr
translation envelope is labeled separately from an hpatch command rejection. When the router
artifact supports it, the summary lists bounded attempt sequences and evaluator rejection
evidence by repetition, command, physical source line, operation, target kind, stable reason,
path, and generated Go line and column when applicable. An older artifact without either
bounded collection is labeled unavailable rather than reported as zero activity. When lifetime
routed-call counters exceed retained attempts, retention-dependent correction-adoption and
recovery measures are labeled unavailable rather than reported as full-run rates.

Acceptance:

1. Base qualification fails with `FastKeysOnly option is missing`; oracle
   qualification passes the identical hidden grader.
2. Both arms receive independent byte-equivalent base snapshots and exactly the
   same non-editing setup.
3. The treatment base instruction is derived from the stock prompt using the
   repository-owned replacement, and the retained diff contains no unrelated
   base-instruction changes.
4. Hidden tests are absent during agent execution and injected only after changed
   paths and the literal agent diff are captured.
5. A timeout, Codex failure, invalid JSONL, unauthorized path, or hidden-grader
   failure makes the attempt fail without deleting its evidence.
6. Results retain elapsed/token metrics and the actual agent diff; treatment
   artifacts retain structured router gain metrics and textual `hpatch gain`.
7. The summary attributes routed hpatch outcomes to their originating repetitions,
   distinguishes client file-change items from routed calls, and separates semantic
   edit-payload estimates from end-to-end output usage. It reports structured evaluator
   rejection evidence when present and identifies artifacts that predate that evidence.
