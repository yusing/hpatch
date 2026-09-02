# End-to-end benchmark

The benchmark compares actual Codex runs from independent historical workspaces. Hidden executable
tests and a changed-path boundary decide correctness. The capture path then reports provider usage,
transport savings, tools, and Hpatch delivery from observed traffic rather than production counters
or hypothetical baselines.

## Requirements

The runner checks its host dependencies. You need Docker Compose, Codex authentication at
`$CODEX_AUTH_PATH` or `$CODEX_HOME/auth.json`, and the task's local source repository under
`benchmarks/repos/`.

## Run it

Default paired run:

```sh
bash benchmarks/bench.sh
```

One Hpatch attempt against a matching published control:

```sh
BENCHMARK_MODE=hpatch-only REPETITIONS=1 bash benchmarks/bench.sh
```

One diagnostic run without a control:

```sh
BENCHMARK_MODE=hpatch-diagnostic REPETITIONS=1 bash benchmarks/bench.sh
```

Native Hpatch versus CTP/2:

```sh
TASK_ID=batch-diagnostic-collapse BENCHMARK_MODE=ctp-only \
  BENCHMARK_REPORT_ISSUES=false REPETITIONS=4 bash benchmarks/bench.sh
```

Hpatch versus Mentor Handoff:

```sh
MODEL=gpt-5.6-luna REASONING_EFFORT=xhigh REPETITIONS=2 \
  BENCHMARK_MODE=mentor-handoff BENCHMARK_REPORT_ISSUES=false \
  bash benchmarks/bench.sh
```

Exhaustive commentary coverage across diagnostic, native/CTP, and Mentor Handoff arms:

```sh
bash benchmarks/run-commentary-coverage.sh
```

The commentary suite runs one repetition in each mode and continues through all three modes so one
failed arm does not hide later evidence. It intentionally permits repeated edits because stale-target
recovery is part of the task. `MODEL` may select `gpt-5.6-luna` or `gpt-5.6-terra`, and
`REASONING_EFFORT` overrides the default `medium` effort.

Coverage includes Hpatch apply and recovery, optional issue reporting, Bash and POSIX runtime
publications, provider-owned exec invocation, Code Mode runtime publication, subagent start and
response projection, and terminal token telemetry. Runtime publications and exec invocation are
proven by successful command markers because Codex JSONL does not reliably retain their user-only
messages. Host-only continuation events are outside this retained event boundary.

`MODEL`, `REASONING_EFFORT`, and paired-mode `REPETITIONS` override the defaults. CTP/2 and Mentor
Handoff disable issue reporting so the reporting tool does not confound either treatment.

## One-listener topology

Each measured arm runs one `hpatch-router` process with one listener:

```text
Codex ──HTTP──> hpatch-router ──HTTP──> provider
                 │         │
                 └─ in-process capturer
```

The capturer wraps the existing Responses handler and provider transport. It is a Go subpackage,
not a service. It opens no port. The router's same listener exposes:

```text
POST /v1/responses
GET  /v1/models
GET  /api/metrics
GET  /                 # human-readable view of /api/metrics
```

The Compose file therefore contains `control` and `hpatch` router services but no front or back
capturer services. Each agent joins only its router's internal network. Each router also joins the
egress network and talks directly to the provider. The runner supplies `--capture-output` so each
router appends its own sanitized evidence file.

## What differs between arms

Paired control uses passthrough mode and the pinned stock instructions. Hpatch mode replaces the
supported Code Mode editing owner with Hpatch and shell while preserving unrelated tools. Each arm
gets a separate workspace and alternates execution order across repetitions.

CTP-only uses Hpatch in both arms; only the model protocol and owning guidance differ. Mentor
Handoff uses Hpatch in both arms; only the treatment router enables its bounded subagent model
schedule. Parent and child traffic remains visible through actual model names in capture exchanges.

## Capture and metrics

`capturer` records schema-4 JSONL at both boundaries without storing credentials, prompts,
instructions, tool arguments, command output, response text, diagnostics, scripts, reports, or
patches. Records contain sizes, token estimates, status, duration, provider usage, tool identities,
and sanitized delivery kinds or diagnostic codes. Correlation is process-private Go context; no
private header crosses the network.

Each response boundary retains at most 8 MiB for parsing while the complete stream remains forwarded
and byte-counted. Overflow is incomplete evidence. Diagnostic capture accepts only stable allowlisted
reason codes from a complete router-owned envelope; arbitrary `text(...)` content is discarded.

`GET /api/metrics` returns `hpatch.capture.metrics.v2`. It is authoritative for:

- logical requests and provider retry attempts;
- provider input, cached input, uncached input, output, and reasoning tokens;
- cold/new and eligible-prefix cache attribution between logical requests from each final attempt;
- client and provider payload bytes and GPT-5 token estimates;
- signed protocol input and output savings;
- provider-emitted and client-delivered tool shapes;
- correlated Hpatch calls, corrections, deliveries, rejections, diagnostics, and carrier savings;
- actual provider model for every attempt, including attempts without usage; and
- capture completeness, dropped-detail, and write health.

Cumulative totals cover the router lifetime. Detailed exchanges retain the latest 4,096 requests;
if that window fills, totals remain complete but dropped-detail health invalidates benchmark use.

The report validator requires both fresh arms in paired, CTP/2, and Mentor modes. It reconciles raw
records, exchanges, aggregate usage, capture health, each measured root thread's provider usage and
configured model, and Mentor child lineage and model schedules. It rejects partial or unproved
evidence. Configured CTP/2 compression requirements use signed snapshot savings and retain a failed
value in the summary before the run exits nonzero. The report formats snapshot values rather than
calculating another notion of gain. This means retries remain retries, negative expansion stays
visible, and Hpatch is compared with the native carrier actually delivered to Codex.

## Artifacts

A retained run includes:

```text
results.jsonl
summary.md
benchmark-config.json
control-metrics.json                 # when a fresh baseline arm ran
hpatch-metrics.json
captures/control.jsonl               # when a fresh baseline arm ran
captures/hpatch.jsonl
control-router.log                   # when applicable
hpatch-router.log
artifacts/                            # per-attempt result, events, patch, and grader evidence
agent-issue-reports.jsonl             # when issue reporting collected records
```

An opt-in commentary task also writes `commentary-coverage.json` beside each attempt's
`result.json`. Functional correctness remains in the hidden grader record; commentary coverage is a
separate result field derived from retained assistant messages, successful command markers, and
completed item types in Codex events.

Mentor Handoff renames the treatment snapshot and log to `hpatch-mentor-metrics.json` and
`hpatch-mentor-router.log`. Child event and content-free lineage proof artifacts remain under the
attempt directory. Summary output intentionally omits request, session, thread, call, and capture
identities.

## Validate reporting

```sh
bash benchmarks/commentary_coverage_test.sh
bash benchmarks/expected_final_response_test.sh
bash benchmarks/report_test.sh
```

The commentary fixture covers profile selection, operation and collaboration messages, successful
command markers, event minimums, missing evidence, malformed event streams, and unsupported modes.
The expected-response fixture proves router token telemetry remains auxiliary while later ordinary
assistant text remains authoritative. The reporting fixture covers every report mode and falsifies capture health,
aggregate usage, baseline presence and schema, configured provider models, Mentor lineage, and
required CTP compression. Go tests under `capturer/` prove retry correlation, privacy, streaming,
gzip, multiline SSE, and bounded detail with complete cumulative totals.
