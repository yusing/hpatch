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

- **Hashline edits.**
  - `functions.hpatch` identifies existing text with `LINE:HASH` references and
    writes the replacement once.
  - Invalid scripts are rejected as a whole before Codex applies the generated patch.
- **Direct execution.**
  - `functions.shell` accepts a program in its native syntax, without a
    JavaScript wrapper or nested command-string quoting.
- **Read only what the edit needs.**
  - `hgrep` finds matching text, `hsymbol` locates definitions and references,
    and `inspect_file` outlines a file without returning its full source.
  - Their verified references can be used directly as edit targets; `hread`
    supplies source text when more context is needed.
- **Correct without starting over.**
  - Eligible shell programs can be retained, inspected, edited, and rerun
    instead of emitted again.
  - When an edit is rejected solely because its target rows are stale, the
    agent can correct the references without repeating the replacement text.

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
| `--debug` | Disabled | Record diagnostics, capture, metrics, and patched instructions; print all artifact paths on exit |

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
follow-ups. Start a new session after enabling Grok; a custom
`model_catalog_json` must also include its entry.

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

The following commands are available **inside the tool's Bash and POSIX
programs**, not as standalone utilities in your terminal:

| Command | Purpose | Extra prerequisite on the executor's `PATH` |
| --- | --- | --- |
| `hread` | Read verified source rows | None |
| `hgrep` | Search text with verified row references | `rg` |
| `hsymbol` | Look up definitions and references | `gopls` for Go; TypeScript 7 as `tsc` for JS, TS, and JSON; `pyright-langserver` for Python |
| `inspect_file` | Inspect structure without full source bodies | None |

Retained programs use thread-local `@shell/` references and expire after one
hour by default. They are not workspace files and are removed on router
shutdown. See the [shell reference](doc/spec/shell.md) for retention, editing,
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
with a warning to use `functions.exec` directly. Missing fields mean the evidence was not recorded. Export capture or metrics before
shutdown if you need to investigate later; neither export contains raw prompts or scripts.

To record the patched instructions for new requests, use:

```sh
mekugi --debug codex
```

Debug mode creates a private `mekugi-debug-*` directory in the system temporary
directory. After Codex exits, it prints absolute paths to stderr for:

- `router.jsonl`: router lifecycle and parsed-request outcomes, with safe failure codes and
  diagnostic references matching the notices in Codex, without raw error text.

- `capture.jsonl`: the same sanitized capture described above.
- `metrics.json`: the final metrics snapshot.
- `instructions.jsonl`: exact instruction text, developer messages, and tool declarations
  after request rewriting, with thread and request identifiers.

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
