# mekugi

A mekugi is the small peg that pins a Japanese sword's handle to the blade.
Take it out and the handle comes off. Leave it in and the blade is still the
blade.

Mekugi pins compact agent tools onto stock Codex: hashline edits, direct
scripts, and inline subagent activity. Codex keeps the sandbox, permissions,
command sessions, and patch diff UI. No fork, no config edits, no daemon.

[Install](#install) · [Features](#features) · [Usage](#usage) ·
[Metrics](#metrics) · [Documentation](#documentation)

## Features

### UX

- **Keep the familiar Codex workflow.**
  - Each launch gets its own router, with no persistent service or changes to
    your Codex configuration files.
- **See subagent progress and replies inline.**
  - A start notice shows each subagent's observed model and reasoning effort once its
    first request reaches the router. Other lifecycle actions add no extra notices.
  - Subagents' own commentary appears in the main conversation with their agent paths.
  - Received messages and final answers identify both parties and show plaintext
    replies in full when they fit the display budget. Encrypted collaboration messages are not exposed.
- **Follow work as it runs.**
  - Supported tool calls can carry an authored description before execution; calls without one stay quiet.
  - Scripts can publish progress such as “Running item 3/10” without mixing
    updates into command output.
  - Child commentary, tool, and script updates carry the agent's path when Codex supplies
    its identity, so concurrent agents' updates are distinguishable.
  - When Codex supplies parent-thread metadata, child activity also appears inline
    in the stock root TUI. Updates are offered at response-event boundaries;
    activity after a response closes waits for the next root response and is
    labelled as activity since the last update. This is not a continuous live
    feed during native waits, and requires no Codex panel or client patch.
- **See token usage for the main agent and subagents.**
  - Final answers with provider usage show input, cached-input, output, and reasoning
    token totals accumulated for that agent's thread during the router's lifetime, including across compaction.
    Intermediate tool calls do not produce token notices.
  - Router notices are removed from later model requests, so the display does
    not add repeated context. See [inline commentary](doc/spec/commentary.md).
- **Inspect a session in your browser.**
  - Each launch has its own dashboard with request metrics, provider token
    usage, compression measurements, and cache diagnostics.
- **Use [Grok native subagents](#grok-subagents) alongside OpenAI models.**
  - Opt in with `--grok` and separate Grok authentication.

### AX

- **Edit by reference, not repeated patch context.**
  - `functions.hpatch` accepts verified `LINE:HASH` rows, inclusive ranges,
    and exact literal text, including text the agent already knows.
  - Related edits across files share one validation pass before Codex applies
    the generated patch. Invalid targets or conflicting edits reject the whole script.
  - Successful reports return current row references for follow-up edits.
    Unchanged saved rows remain reusable after line shifts when their hash
    identifies exactly one row.
- **Read only what the edit needs.**
  - Inside `functions.shell`, `hgrep` searches with verified rows, `hsymbol`
    finds semantic definitions and references, and `hcat` reads exact source ranges.
  - `inspect_file` returns a structural outline with editable spans without
    exposing source bodies.
  - `hcat --max-tokens N` and `hgrep --max-tokens N` set a strict output token
    ceiling. Add `--preview-bytes N` for bounded long-line previews.
    Omitted content is explicit; previews retain the complete row's verified identity.
- **Catch supported syntax problems before applying edits.**
  - Changed Go files are parsed and formatted automatically. Supported Python,
    JavaScript, and TypeScript files receive syntax checks and indentation correction.
  - Rejections include localized repair context; successful reports expose
    newline and blank-separator advisories without treating them as errors.
    These checks do not replace tests.
- **Correct a rejected edit without starting over.**
  - `functions.hpatch_recover` repairs the retained rejected script while
    preserving unrelated prepared changes.
  - Stale-target shortcuts replace only the rejected targets. Ordinary script-text
    edits can repair values, paths, framing, or conflicting commands before the
    complete script is reevaluated.
- **Execute programs directly.**
  - `functions.shell` accepts Bash or a selected interpreter's native source,
    without a JavaScript wrapper or nested command-string quoting.
  - Interpreter selectors, per-call execution options, and command templates
    keep script source separate from standard-input data.
- **Batch commands and resume running work.**
  - With Code Mode available, one shell call can run separate noninteractive
    programs sequentially, including different interpreters.
  - Batches can continue after nonzero exits or stop before later programs start.
    Results preserve each program's output and report started and unstarted counts.
  - Recognized yielded results identify the next host continuation call, so the
    agent can resume the existing process or Code Mode cell rather than restart it.
- **Reuse executable source.**
  - Eligible shell programs return thread-private `@shell/` references that the
    agent can inspect, edit, and rerun without emitting the whole program again.
  - Retention metadata states the expiry and temporary scope. Reads and edits do
    not renew it; source that must survive belongs in a workspace file.

See [how editing and execution work](#how-editing-and-execution-work) for usage
and prerequisites.

### Token saving

- **Write the new code once.**
  - Replacing an 11-line function does not require reproducing all 11 old lines
    as patch context. The model names the verified range and writes the new
    function; the router generates the patch framing.
- **Spend output on the program, not its wrapper.**
  - Direct scripts avoid the JavaScript carrier, JSON argument object, and
    extra quoting layers needed to call the executor through Code Mode.
- **Avoid sending repeated text in full.**
  - [CTP/2](doc/spec/ctp.md), enabled by default, losslessly encodes eligible
    model-visible text using local dictionaries and references to earlier
    visible tool output lines in the same request.
  - Tool names and newly generated tool payloads stay native.
    Use `--model-protocol native` to disable CTP/2.

### Performance

- **Start eligible subagents with [Mentor Handoff](doc/spec/mentor.md).**
  - Enabled by default: `gpt-5.6-luna` and `gpt-5.6-terra` subagents start on
    `gpt-5.6-sol` with high reasoning, then hand back to their configured model.
  - Ordinary sessions and forks are unchanged.
    Disable it with `--mentor-handoff=false`.

Token savings and model handoffs are not a promise of faster commands or better
results on every task. See the [benchmark methodology](doc/benchmarks.md) for
comparisons.

## Install

### Requirements

- **Go 1.26+**, CGO enabled, and a C toolchain to build the binaries.
- **Codex CLI**, signed in with `codex login` using ChatGPT authentication.
- **Node.js 24+** available as `node`, and **ripgrep** available as `rg` on
  the router's `PATH` for mekugi mode.
- Any interpreter your agent selects, such as `python3`, on the executor's
  `PATH`. Bash and POSIX shell execution are built in.

Install both the router and its shell helper:

```sh
go install github.com/yusing/mekugi/cmd/mekugi@latest \
  github.com/yusing/mekugi/cmd/shell@latest
```

Add `$GOBIN`, or `$(go env GOPATH)/bin` when unset, to the `PATH` used by both
Mekugi and Codex. The fixed `shell` helper must be available to Codex's executor.

Then launch:

```sh
codex login
mekugi codex
```

Mekugi prints a dashboard URL before Codex opens. Use Codex as usual; the router
supplies the agent's tool guidance automatically.

### From a checkout

With **Bun** and **Make** installed:

```sh
make install
```

This regenerates the embedded plugins and installs both binaries. Installation
and uninstallation leave Codex configuration and instruction files untouched.
`make uninstall` removes only the installed `mekugi` and `shell` binaries.

## Usage

Mekugi keeps private replay records on disk so resumed and forked conversations
retain their original tool history. These records include tool inputs and recovery
diagnostics, not just metrics. See [replay storage](#replay-storage) for location,
limits, and cleanup.

Put Mekugi flags **before** `codex`; arguments after it belong to Codex:

```sh
mekugi codex
mekugi codex --model gpt-6-astra
mekugi codex exec "Explain this repository"
mekugi codex resume 'CONVERSATION_ID'
mekugi --model-protocol native --mentor-handoff=false codex
```

Each invocation starts a private router on a random loopback port and shuts it
down when Codex exits. Multiple sessions can run independently. Codex handles
terminal Ctrl-C, and its exit status is preserved.

The wrapper uses the fixed Codex ChatGPT upstream and overrides provider
selection for that invocation only. Standalone serving, fixed ports, custom
providers, and provider-selection arguments such as `--oss` are not supported.
It also forces `include_collaboration_mode_instructions=false` for the invocation,
so Codex does not inject collaboration-mode instructions, even if enabled in your
config or command-line overrides. No configuration files are changed.

The wrapper enables WebSockets between Codex and Mekugi for that invocation,
without changing Codex configuration. Mekugi keeps the ChatGPT connection open
across responses so a compatible Codex client can send
[mid-turn steering](https://developers.openai.com/api/docs/guides/steering)
updates. Steering requires a supporting client and model; enabling the transport
does not add steering to an older Codex client.

Networks must allow secure WebSocket connections to ChatGPT. Mekugi also accepts
HTTP/SSE clients and can fall back to HTTP for those requests when ChatGPT
explicitly rejects the WebSocket upgrade. It never silently replays a dropped
request or accepted steering. Grok provider requests remain on HTTP.

### Options

| Flag | Default | Purpose |
| --- | --- | --- |
| `--mode` | `mekugi` | Use `passthrough` to forward traffic without mekugi tools, plugins, CTP/2, or Mentor Handoff |
| `--model-protocol` | `ctp2` | Use `native` to disable CTP/2 in mekugi mode |
| `--mentor-handoff` | `true` | Use `false` to keep subagents on their configured models |
| `--grok` | `false` | Enable Grok subagents in mekugi mode |
| `--grok-auth-file` | `~/.grok/auth.json` | Select a Grok OAuth credential store |
| `--timeout` | `10m` | Wait for the upstream response to start |
| `--stream-idle-timeout` | `4m` | Limit gaps between provider messages during an active response, or HTTP response bytes |
| `--capture-output PATH` | Disabled | Append sanitized JSONL metrics |
| `--metrics-output PATH` | Disabled | Write the final metrics snapshot on shutdown, overwriting the destination |
| `--debug` | Disabled | Record diagnostics, capture, metrics, patched instructions, runtime reads, and an AX report; print all artifact paths on exit |

For a transport-only session:

```sh
mekugi --mode passthrough codex
```

Passthrough does not load the plugin registry, so it does not require Node.js or
plugin grammar validation. Capture remains available.

### Grok subagents

Opting in enables Grok requests and plaintext collaboration messages. Authenticate
with `grok login --oauth`, or supply `XAI_API_KEY` in the router's environment.
An API key takes precedence. Codex credentials are never forwarded to Grok.

```sh
mekugi --grok codex
```

Ask the main agent to spawn `grok:grok-4.6` in fresh context
(`fork_turns="none"`). Codex still manages the child, tools, permissions, and
follow-ups. At startup, Mekugi uses `codex debug models` to read your selected
catalog, adds Grok, and pins a private copy for the session. This requires a Codex
version with `debug models` and `model_catalog_json` support. A custom catalog must
contain a native v2 model whose instruction and tool metadata can be used for Grok.
Other Codex sessions cannot replace this session's catalog. Model availability is
fixed until restart; your configuration files are unchanged, and the private copy
is removed when Mekugi exits. `--grok` cannot be combined with Codex's named
`--profile` option or `exec --ignore-user-config` because `debug models` cannot
honor those configuration modes. Use the default configuration or an explicit
`-c model_catalog_json=...` instead.

OpenAI-hosted search and inherited encrypted OpenAI history are not supported
on this route. Explicit `max_output_tokens` limits are rejected because this
route cannot enforce a total budget including reasoning. See the
[Grok subagent requirements](doc/spec/subagents.md) for supported inputs and
credential handling.

## How editing and execution work

### Hashline edits

Instead of emitting old source lines, new source lines, and patch framing, the
agent selects a verified `LINE:HASH` target and sends the new text once. Mekugi
checks the script and generates the patch; Codex authorizes and applies it.
Supported language checks run before application.

Verification is not a workspace lock. A range checks its endpoint rows, not
every line between them. Agents editing overlapping content must coordinate the
complete read/edit/apply cycle and inspect current content after a handoff.

See the [editing guarantees](doc/spec/output.md) and
[target selection rules](doc/spec/select.md).

### Direct scripts

The agent can send a program directly to `functions.shell`, for example:

```python
#!python3
print("hello")
```

Bash is the default. Interactive and long-running programs still use Codex's
native execution and session facilities. Eligible literal `cat` heredoc writes
are converted to patches so they appear in the usual diff UI; other scripts
remain ordinary shell execution.

Recognized yielded results include a `continuation` notice with the next host call.
It distinguishes an outer Code Mode cell from a native process session and keeps
the original output intact. Following that call resumes existing work rather than
starting the script again.

With Code Mode available, a call can batch noninteractive programs in order:

```text
#!batch=NEXT_PROGRAM
#!params={"yield_time_ms":1000}
echo hello
NEXT_PROGRAM
#!python3
print("hello")
NEXT_PROGRAM
#!params={"yield_time_ms":2000}
echo goodbye
```

Choose a separator line absent from the programs, then name it in the first-line
`#!batch=` header. Exact matches separate two or more nonempty programs. Ordinary
single-script calls need no batch header, and selector-like lines inside source
strings or heredocs remain unchanged.

A params-only program header selects Bash. Omitted params inherit the previous
object; a supplied object replaces it, and `{}` clears it. Interpreters,
command templates, and shell state do not carry over.

Programs run sequentially, including waiting for long-running sessions, and
continue after nonzero exits by default. Use `#!batch-stop=SEPARATOR` to leave
later programs unstarted after a nonzero terminal exit, with the same params
inheritance and all-program validation. The ordered `results` array contains each
program's output and native result fields. A host error stops the batch while
preserving completed results and partial output. The `batch` summary reports the
policy, started/unstarted counts, and stop reason. Native-only clients require
separate calls. Use separate calls for interactive programs too, so their
prompts and session handles remain available for input.

The following commands are available **inside the tool's Bash and POSIX
programs**, not as standalone utilities in your terminal:

| Command | Purpose | Extra prerequisite on the executor's `PATH` |
| --- | --- | --- |
| `hcat` | Read verified source rows | None |
| `hgrep` | Search text with verified row references | `rg` |
| `hsymbol` | Look up definitions and references | `gopls` for Go; TypeScript 7 as `tsc` for JS, TS, and JSON; `pyright-langserver` for Python |
| `inspect_file` | Inspect a structural outline | None |

Semantic lookup can start with a known line number:
`hsymbol def source.go 42 MyFunction`. Use `LINE:HASH` instead when the query
must verify a prior read. `hsymbol --workspace /path/to/project refs source.go 42 MyFunction`
selects a resolver root without changing shell state and returns absolute result
paths. Semantic results stay confined to that root.

Structural inspection accepts one ordinary relative or absolute path.
`inspect_file source.go` returns an outline whose verified spans can be used as
HPATCH targets.

For long lines, both verified readers offer an explicit bounded preview:
`hcat --max-tokens 2000 --preview-bytes 160 source.ts` or
`hgrep --max-tokens 2000 --preview-bytes 160 -F needle source.ts`.
Preview records include the complete row's verified identity, a UTF-8 prefix,
and omitted-byte counts. Without preview mode, rows remain exact. A caller's
token ceiling is strict; omitted records are reported as incomplete, not silently
cut. See the [reader contract](doc/spec/read.md) for ranges and bounds.

Retained programs use thread-local `@shell/` references. Their result metadata
reports the original scheduled expiry and non-durable scope. They expire after
one hour by default or on router shutdown; reads and edits do not renew them.
Active operations can delay cleanup. Save source as an ordinary workspace file
when it needs to survive the thread. See the [shell reference](doc/spec/shell.md) for retention, editing,
reruns, and interpreter selection.

## Metrics

Open the dashboard URL printed at startup. It belongs to that session and stops
working when Codex exits. For an SSH session, forward its assigned port first.

Under **Exchanges → Provider attempts**, the **Transport** column shows
**WebSocket** or **HTTP** for each provider attempt. A completed ChatGPT attempt
using HTTP took the fallback path; Grok normally uses HTTP. This describes the
provider connection, not the Codex-to-mekugi HTTP/SSE connection.

From a command running inside wrapped Codex, fetch the same metrics as JSON:

```sh
curl -sS "${MEKUGI_BASE_URL%/v1}/api/metrics"
```

Metrics stay in memory unless you request an export. Capture appends JSONL;
the final snapshot overwrites its destination. Use separate paths:

```sh
mekugi --capture-output capture.jsonl --metrics-output metrics.json codex
```

Exports contain sanitized measurements, not raw prompts, scripts, patches, or
credentials. Provider-reported usage is authoritative; local token estimates
are not billing figures. Missing cache telemetry is not a confirmed cache miss.
See the [metrics reference](doc/spec/metrics.md) for interpretation.

To investigate tool confusion, inspect the affected thread's rewrite decision and delivered
calls in the JSON metrics:

```sh
curl -sS "${MEKUGI_BASE_URL%/v1}/api/metrics" |
  jq '.exchanges[] | {thread_id, model, instruction_rewrite, delivered_tools}'
```

`instruction_rewrite` separates the matched prompt shape from the selected model wording and
shows whether custom instructions were configured. A `shell-typescript-misuse` diagnostic means
a Bash submission was rejected as valid TypeScript/JavaScript before execution, not silently
rerouted. `shell-code-mode-recovered` instead identifies an established Code Mode call recovered
with a warning to use `functions.exec` directly. In the other direction, `exec-shell-recovered`
in a tool result means an interpreter script sent to `functions.exec` was routed through
the normal shell pipeline before execution. Recovery requires an explicit, valid shell header
and invalid JavaScript; valid JavaScript and ambiguous bare commands are left unchanged.
Missing fields mean the evidence was not recorded. Export capture or metrics before
shutdown if you need to investigate later; neither export contains raw prompts or scripts.

To record the patched instructions for new requests, use:

```sh
mekugi --debug codex
```

Debug mode creates a private `mekugi-debug-*` directory in the system temporary
directory. After Codex exits, it prints absolute paths to stderr for:

- `router.jsonl`: router lifecycle, parsed-request outcomes, and feature-usage observations,
  with safe failure codes and diagnostic references matching the notices in Codex,
  without raw error text or feature payloads.

- `capture.jsonl`: the same sanitized capture described above.
- `metrics.json`: the final metrics snapshot.
- `instructions.jsonl`: exact instruction text, developer messages, and tool declarations
  after request rewriting, with thread and request identifiers.

- `reads.jsonl`: actual private-reader start/finish evidence; `--debug` enables it
  automatically. An explicit `MEKUGI_AX_OUTPUT` path takes precedence.
- `ax.json`: an automatic AX report for observed threads, joining their local Codex
  rollouts to replay and runtime read evidence. No workspace argument is needed.
  Missing, ambiguous, or incomplete rollout evidence is labeled rather than guessed.
  Existing defect assessments can be added later with `inspect-session --defects`.

To check whether agents used in-tool commentary, query the printed router log path:

```sh
jq -c 'select(.event == "feature_usage" and .feature == "commentary") |
  {timestamp, source, stage, outcome, thread_id, request_id, call_id, message_id}' /path/to/router.jsonl
```

`tool_field / authored / observed` confirms a nonblank eligible commentary argument.
`code_mode / lowering / prepared` confirms a recognized awaited call was wired to a
publisher, not that it ran. `shell` or `code_mode / publication / accepted` confirms
runtime progress reached the broker. Publication outcomes also distinguish blank text,
oversized text, and capacity limits. `render / prepared` means a message was prepared
for a response, not proof of client display. Automatic router notices are excluded.
Shell commands that cannot reach a publisher remain unobserved.

Count a single stage, not all events together. Deduplicate authored observations by
thread and call ID, and rendering by `message_id`. Runtime publications have no original
request or public session ID; shell publications also have no call ID. Join accepted
publications to rendering by `message_id` for request/session correlation. The startup record
advertises the feature schema and instrumented features. Older logs without that marker,
interrupted logs, and logs with write failures cannot establish zero use. These events
are debug-only; they do not appear in capture or metrics exports. See the
[feature evidence contract](doc/spec/router.md#feature-usage-debug-evidence) for exact boundaries.

The dump separates the local request projection (`scope: projected_responses_request`)
from the prepared wire input. `developer_messages` and `additional_tools` include inherited
instructions; `wire_developer_messages` and `wire_additional_tools` contain only the items
being forwarded. `cached_input_items` counts the reused prefix. If inherited instructions
or tool declarations changed, `cache_rebased` is true, the full projected history is sent,
and `wire_previous_response_id` is null. `wire_request_present` is false for automatic
successors, which have no outgoing request. These records describe preparation, not proof
of provider acceptance. For Grok, they precede conversion to Chat Completions. Ordinary user
messages, tool calls, and authentication headers are excluded. Instruction text is not
sanitized and can contain private information supplied in your instructions.

Artifacts survive wrapper exit, but the operating system may eventually clean temporary
files. Copy them elsewhere if needed. Existing `--capture-output` and `--metrics-output`
paths take precedence over the debug defaults and are included in the exit listing.
Debug output failures are reported on exit without changing request execution.
Resuming with `mekugi --debug codex resume SESSION_ID` records future requests; it cannot
recover an earlier request that was not dumped.

## Configuration and troubleshooting

- **Custom instructions:** Mekugi supplies tool guidance in memory without
  editing your instruction file. If you use a custom prompt, configure it with
  Codex's `model_instructions_file` setting. Restart Mekugi after adding or
  removing that setting. See [guidance compatibility](doc/spec/guide.md).
- **Plugins:** put regular `.js` or `.mjs` modules in `mekugi/plugins` beneath
  your platform's user configuration directory. On Linux this is
  `$XDG_CONFIG_HOME/mekugi/plugins` or `~/.config/mekugi/plugins`; on macOS it is
  `~/Library/Application Support/mekugi/plugins`. Plugins are loaded at startup;
  changes require a new Mekugi launch. See the [plugin contract](doc/spec/plugin.md).
- **Executor environment:** the router and executor must see the same workspace
  paths and shell runtime directory. `MEKUGI_RUNTIME_DIR` overrides the default
  operating-system temporary directory; both must resolve it to the same
  absolute path. The shared `shell` helper follows the session's
  `mekugi-runtime-<thread>` locator.
- **Failures:** startup errors appear before Codex launches. Session failures
  appear as user-only commentary; undelivered notices appear on stderr after
  Codex exits. Mekugi does not create operational log files unless `--debug` is enabled.
- **Agent issue reports:** see [opt-in agent issue reports](doc/spec/diagnose.md).

### Replay storage

Replay records live at `$XDG_STATE_HOME/mekugi/replay`, or
`~/.local/state/mekugi/replay` when `XDG_STATE_HOME` is unset. An override must be
absolute. The directory and records are private to your operating-system user.
Multiple wrappers share this store, with workspace isolation; closing a wrapper
does not delete it. Passthrough mode does not open it.

Resuming a conversation or opening a side conversation needs no extra Mekugi flag.
Only inherited calls actually present in that conversation become available for
recovery. Replay does not rerun old commands or restore live shell processes,
continuation handles, or expired private scripts. History recorded by older
versions without durable replay records cannot be reconstructed reliably.

The store limits call records to 1 GiB in total and 32 MiB per record. Commentary
identities have a separate 16 MiB allowance. It rejects new call records when full
instead of silently discarding resumable history. To reset
storage, stop all Mekugi wrappers and move the replay directory aside. Conversations
whose records you remove lose replay restoration; keep the moved directory if you
may need to restore it later. Do not remove records just because one fork no longer
shows those calls: a parent or sibling conversation may still need them.

### Inspect a session

Inspect a local Codex rollout without decoding execution carriers or running old
commands. This is read-only and starts no router. By default, JSON output contains
logical tool names, call IDs, outcomes, text sizes, and pagination, not private text:

```sh
mekugi inspect-session --session /path/to/rollout.jsonl
mekugi inspect-session --session /path/to/rollout.jsonl \
  --call-id call_example --field script
```

Use `--field evaluated`, `patch`, `report`, `diagnostic`, `rejections`, or `output`
to inspect that evidence, or `all` for every text field. These fields may contain
private source and command output. `--text-bytes` bounds each UTF-8 prefix and
`omitted_bytes` identifies missing text. `--offset` and `--limit` page through calls;
`next_offset` identifies the next page. `--replay-dir` selects a moved replay store.

Workspace identity is inferred from the rollout's session and turn metadata, including
workspace changes. Use `--workspace` only to override missing or incorrect metadata.
Missing workspace metadata or replay records remain
explicitly unavailable rather than being reconstructed from carrier code.
`translated_unconfirmed` means a patch was prepared, not applied. `confirmed`
requires the matching executor report in the supplied rollout; `applied` records
router-owned application. Inspection does not establish that a change was correct
or restore a live session. See the [session inspection contract](doc/spec/session.md).

### Measure editing and reading effort

Use `mekugi --debug codex` to enable **executed private-reader instrumentation** and
include an AX report in the debug bundle automatically. To record only reads without
the other debug artifacts, set an absolute journal path before launching Codex. Its parent directory must already exist. The executor inherits the
setting; an existing journal must be a regular file with mode `0600`:

```sh
MEKUGI_AX_OUTPUT=/path/to/private/reads.jsonl mekugi codex
```

The journal records reader name, thread, start/finish, duration, and success, never
source paths, arguments, or output. It counts actual `hcat`, `hgrep`, `hsymbol`, and
`inspect_file` invocations, including loops and failures, not commands in skipped
branches or quoted examples. It does not count external programs' file accesses.
Evidence-write failures leave command behavior intact and produce an auxiliary
stderr notice. A start without a finish is incomplete, not successful.

Inspect the rollout and journal together:

```sh
mekugi inspect-session --session /path/to/rollout.jsonl --ax \
  --read-log /path/to/private/reads.jsonl
```

AX measurements cover the entire supplied rollout, regardless of call filtering or
pagination. They include matched edit retries, emitted bytes, exact line bytes repeated
from the preceding edit payload, observed turn-completion intervals, and runtime reader
counts for the rollout's thread. Missing journal/thread evidence is unavailable.
Repeated bytes are not automatically wasted, and read counts do not say a read was unnecessary.

Defects require explicit assessment, not inference from a rejection or successful
application. Pass `--defects /path/to/assessments.json` with an array such as:

```json
[
  {"call_id": "call_example", "verdict": "defect", "evidence": "failing-test.txt"},
  {"call_id": "call_other", "verdict": "no_defect", "evidence": "review-result.txt"}
]
```

Evidence paths resolve relative to the assessment file. Each must name a nonempty
regular artifact no larger than 1 MiB. The result includes its SHA-256 fingerprint
and the supplied verdict, separately from measured counters. Unassessed edits stay
unassessed; neither a test failure nor a verdict alone proves that an edit caused
a defect. See the [AX evidence contract](doc/spec/ax.md) for scope and limits.

### Older installations

Finish active sessions before replacing an older installation. Retire any old
service and provider configuration separately, preserving unrelated settings and
authentication. Use `mekugi codex` for future sessions.

## Go library

The root package, `github.com/yusing/mekugi`, also exposes workspace evaluation,
application, reporting, and host translation APIs. See the
[workspace API requirements](doc/spec/file.md) and
[translation contract](doc/architecture/translate.md).

Library callers must coordinate concurrent writers. Multi-file installation is
not crash-atomic or isolated from readers, and an application error can follow
filesystem changes. Inspect the outcome before retrying; see the
[complete guarantees](doc/spec/output.md).

## Documentation

- [Interface specifications](doc/spec/index.md)
- [Architecture and ownership](doc/architecture/index.md)
- [Benchmark methodology](doc/benchmarks.md)
- [Codex end-to-end checks](doc/codex-router-e2e.md)

## Development

Bun is required to regenerate and test plugin assets:

```sh
go generate ./internal/router/toolplugin
bun test ./internal/router/toolplugin/tests
go test ./...
go vet ./...
make install
```

For focused checks, use `go test .` for the engine,
`go test ./internal/router` for routing, or
`go test ./cmd/mekugi ./cmd/shell` for process entry points.

## License

MIT. See [LICENSE](LICENSE).
