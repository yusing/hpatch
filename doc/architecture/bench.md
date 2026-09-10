# Benchmark trust and execution boundary

## CTR-BENCH-001 — Benchmark trust and execution boundary

The benchmark runner owns historical workspace creation, instruction preparation, arm scheduling,
network isolation, Codex invocation, pre-grader artifact capture, hidden grading, result retention,
and report validation. Sourceable runner modules separate configuration, task identity, preparation,
execution, and artifact finalization without starting a run. One normalized arm plan owns fresh and
imported membership, executor service, router mode/protocol, instructions, and capture destinations.
One block scheduler owns alternating order and per-block cancellation for all measured modes. Agents cannot reach the historical oracle or hidden grader before their
changes are captured.

The runner freezes Docker build inputs in a retained archive and pins subsequent containers to
the built image ID. Candidate Git metadata is never an authority: a separate runner-owned index
compares a trusted baseline with a filesystem copy, and only that captured copy reaches grading.
Grading runs in a capability-free, network-disabled container with private temporary build caches
and read-only dependency material. It cannot write authoritative artifacts or another attempt.
The session launcher places replay state under its private runtime mount, writable to the router
but read-only in the executor namespace.

Each fresh arm has one `mekugi` process and one router listener. Codex connects directly to
that listener. The router connects directly to the provider. The root `capturer` package observes
both boundaries in-process and writes the arm's sanitized JSONL. The benchmark never inserts a
capturer proxy or service and never needs three servers for one router.

Each attempt's container owns one session wrapper and one Codex process. Separate
arm networks and an immutable executor primary group keep network access distinct:
ipv4/ipv6 OUTPUT rules allow Codex only its assigned loopback port. The trusted
launcher retains firewall/mount setup capabilities; Codex runs in private mount/PID
namespaces with all capabilities removed and privilege elevation disabled. Trusted
runtime, capture, and configuration mounts are read-only to Codex. Qualification
fails before inference if group changes, external access, capabilities, or writable
trusted mounts are possible.

The session's `--capture-output` and `--metrics-output` artifacts survive shutdown.
The benchmark-only merger validates each session against its raw records and uses
capturer-owned calculations for combined arm exports. It rebases combined sequence
and predecessor identities, rejects repeated threads, and leaves originals intact.
No standalone router, permanent listener, log collection, or post-exit HTTP scrape
is involved.

The capturer snapshot is the authoritative calculation surface. Reporting validates raw records,
snapshot exchange totals, capture health, and result-reported per-thread provider usage before it
formats provider usage, cache attribution, payload savings, actual provider and delivered tool
shapes, Hpatch delivery, model attribution, and completeness. It never reconstructs a hypothetical
stock command, result, or patch.

Fresh two-arm modes require both arms' current capture and snapshot. The validator binds each root
thread to its configured model, binds Mentor child threads to retained child proofs, rejects model
traffic outside the allowed schedule, and rejects any unproved thread. Configured CTP/2 compression
requirements use the capturer's signed end-to-end protocol totals and fail after preserving the
summary when required input or output savings are not positive.

Arm labels are evidence-backed: the validator requires passthrough/native for control,
Hpatch/native for hpatch-only and native-protocol arms, and Hpatch/CTP2 for the CTP arm.
Paired and diagnostic Hpatch treatment use the protocol retained in benchmark configuration. Control-only starts and
collects only the stock router and validates its evidence without a treatment or comparison.
Mentor arms use Hpatch with the shared protocol selected by the retained benchmark configuration,
native by default. The main model is selected independently of the router-owned child mentor
schedule; the validator binds treatment children to the configured mentor model, not the main
model. Required compression is checked for both Mentor arms when their protocol is CTP/2. Raw
records must agree with the snapshot. Model reporting groups every provider attempt and separately
counts attempts carrying provider usage.

The runner binds fresh results to a content hash of the task manifest, visible prompt, and hidden
grader files. Imported controls must match that hash and the current stock instruction hash as
well as task ID, model, effort, and successful grading. Changing task behavior therefore cannot
silently reuse a published control from a different contract. Content checks before agent launch
and around grading reject mid-run task edits without replacing the recorded identity.

Hidden tests and the allowed-path boundary decide correctness. Timing and token comparisons are
interpreted only after correctness. CTP/2 and Mentor Handoff use the same capture and reporting
path as paired and diagnostic runs; their distinct model/protocol behavior is visible in exchange
models, protocol savings, and provider usage rather than production metric callbacks.

Normal benchmark completion invokes finalization directly with the measured status; the EXIT trap
owns the same finalization for earlier failures. Finalization preserves the original nonzero result
while collecting available evidence, stopping task-owned Compose resources, publishing the retained
run, and rendering available reports.

The router package's test-only replay harness owns private real-session corpus selection, freezing,
manifest integrity checks, and reconstructed-request codec measurements. Freezing is explicit and
separate from replay. The manifest owns fixed sample membership; replay never replaces missing or
changed samples with live history. Raw conversations remain outside the repository and are not
diagnostic output. This harness consumes the production codec without adding production metrics;
its local compression measurements do not replace graded provider-backed task evaluation.

The runner also owns opt-in commentary coverage over retained Codex events. Task manifests own the
mode/arm profile assignments and required message, successful-command-marker, and item minimums;
the checker owns schema validation and evidence matching. Runtime publication markers are matched
only in successful command executions because Codex JSONL does not retain those publications as
assistant messages. The artifact remains distinct from hidden functional graders and from
capturer-owned transport and usage metrics.

For task-owned exact responses, the runner selects the last substantive agent message. It excludes
only the router's synthetic token-usage commentary, leaving any later ordinary assistant message
authoritative.
