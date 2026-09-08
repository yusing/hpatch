# hpatch

Verified edits and direct script execution for Codex, with less model-generated
boilerplate. Hpatch routes Codex requests through a private local router while
keeping Codex's sandbox, permissions, command sessions, and normal patch diff UI.

[Install](#install) · [Features](#features) · [Usage](#usage) ·
[Metrics](#metrics) · [Documentation](#documentation)

## Features

### UX

- **Keep the familiar Codex workflow.**
  - Each launch gets its own router, with no persistent service or changes to
    your Codex configuration files.
- **See subagent details and replies inline.**
  - Before launch, see the requested role, model, and reasoning effort.
  - Plaintext messages and final answers show the sender and exact reply, not
    just the main agent's summary. Encrypted collaboration messages are not exposed.
- **Follow work as it runs.**
  - Supported tool calls show a short description before execution.
  - Scripts can publish progress such as “Running item 3/10” without mixing
    updates into command output.
- **See token usage for the main agent and subagents.**
  - Completed responses with provider usage show input, cached-input, output,
    and reasoning token counts.
  - Router notices are removed from later model requests, so the display does
    not add repeated context. See [inline commentary](doc/spec/commentary.md).
- **Inspect a session in your browser.**
  - Each launch has its own dashboard with request metrics, provider token
    usage, compression measurements, and cache diagnostics.
- **Use [Grok native subagents](#grok-subagents) alongside OpenAI models.**
  - Opt in with `--grok` and separate Grok authentication.

### AX

- **Verified editing.**
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
  the router's `PATH` for Hpatch mode.
- Any interpreter your agent selects, such as `python3`, on the executor's
  `PATH`. Bash and POSIX shell execution are built in.

Install both the router and its shell helper:

```sh
go install github.com/yusing/hpatch/cmd/hpatch@latest \
  github.com/yusing/hpatch/cmd/shell@latest
```

Add `$GOBIN`, or `$(go env GOPATH)/bin` when unset, to the `PATH` used by both
Hpatch and Codex. The fixed `shell` helper must be available to Codex's executor.

Then launch:

```sh
codex login
hpatch codex
```

Hpatch prints a dashboard URL before Codex opens. Use Codex as usual; the router
supplies the agent's tool guidance automatically.

### From a checkout

With **Bun** and **Make** installed:

```sh
make install
```

This regenerates the embedded plugins and installs both binaries. Installation
and uninstallation leave Codex configuration and instruction files untouched.
`make uninstall` removes only the installed `hpatch` and `shell` binaries.

## Usage

Put Hpatch flags **before** `codex`; arguments after it belong to Codex:

```sh
hpatch codex
hpatch codex --model gpt-6-astra
hpatch codex exec "Explain this repository"
hpatch --model-protocol native --mentor-handoff=false codex
```

Each invocation starts a private router on a random loopback port and shuts it
down when Codex exits. Multiple sessions can run independently. Codex handles
terminal Ctrl-C, and its exit status is preserved.

The wrapper uses the fixed Codex ChatGPT upstream and overrides provider
selection for that invocation only. Standalone serving, fixed ports, custom
providers, and provider-selection arguments such as `--oss` are not supported.

### Options

| Flag | Default | Purpose |
| --- | --- | --- |
| `--mode` | `hpatch` | Use `passthrough` to forward traffic without Hpatch tools, plugins, CTP/2, or Mentor Handoff |
| `--model-protocol` | `ctp2` | Use `native` to disable CTP/2 in Hpatch mode |
| `--mentor-handoff` | `true` | Use `false` to keep subagents on their configured models |
| `--grok` | `false` | Enable Grok subagents in Hpatch mode |
| `--grok-auth-file` | `~/.grok/auth.json` | Select a Grok OAuth credential store |
| `--timeout` | `10m` | Wait for the upstream response to start |
| `--stream-idle-timeout` | `4m` | Limit inactivity between upstream response bytes |
| `--capture-output PATH` | Disabled | Append sanitized JSONL metrics |
| `--metrics-output PATH` | Disabled | Write the final metrics snapshot on shutdown, overwriting the destination |

For a transport-only session:

```sh
hpatch --mode passthrough codex
```

Passthrough does not load the plugin registry, so it does not require Node.js or
plugin grammar validation. Capture remains available.

### Grok subagents

Opting in enables Grok requests and plaintext collaboration messages. Authenticate
with `grok login --oauth`, or supply `XAI_API_KEY` in the router's environment.
An API key takes precedence. Codex credentials are never forwarded to Grok.

```sh
hpatch --grok codex
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

### Verified edits

Instead of emitting old source lines, new source lines, and patch framing, the
agent selects a verified `LINE:HASH` target and sends the new text once. Hpatch
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

From a command running inside wrapped Codex, fetch the same metrics as JSON:

```sh
curl -sS "${HPATCH_BASE_URL%/v1}/api/metrics"
```

Metrics stay in memory unless you request an export. Capture appends JSONL;
the final snapshot overwrites its destination. Use separate paths:

```sh
hpatch --capture-output capture.jsonl --metrics-output metrics.json codex
```

Exports contain sanitized measurements, not raw prompts, scripts, patches, or
credentials. Provider-reported usage is authoritative; local token estimates
are not billing figures. Missing cache telemetry is not a confirmed cache miss.
See the [metrics reference](doc/spec/metrics.md) for interpretation.

## Configuration and troubleshooting

- **Custom instructions:** Hpatch supplies tool guidance in memory without
  editing your instruction file. If you use a custom prompt, configure it with
  Codex's `model_instructions_file` setting. Restart Hpatch after adding or
  removing that setting. See [guidance compatibility](doc/spec/guide.md).
- **Plugins:** put regular `.js` or `.mjs` modules in `hpatch/plugins` beneath
  your platform's user configuration directory. On Linux this is
  `$XDG_CONFIG_HOME/hpatch/plugins` or `~/.config/hpatch/plugins`; on macOS it is
  `~/Library/Application Support/hpatch/plugins`. Plugins are loaded at startup;
  changes require a new Hpatch launch. See the [plugin contract](doc/spec/plugin.md).
- **Executor environment:** the router and executor must see the same workspace
  paths and shell runtime directory. `HPATCH_RUNTIME_DIR` overrides the default
  operating-system temporary directory; both must resolve it to the same
  absolute path.
- **Failures:** startup errors appear before Codex launches. Session failures
  appear as user-only commentary; undelivered notices appear on stderr after
  Codex exits. Hpatch does not create operational log files.
- **Diagnostics:** [local tool playback](doc/spec/router-diagnostics.md) and
  [opt-in agent issue reports](doc/spec/diagnose.md) are available when needed.

### Older installations

Installation does not stop an old service or remove old configuration. Finish
active sessions before retiring the old setup. If you previously installed the
systemd user service, stop and disable it when ready:

```sh
systemctl --user disable --now hpatch-router.service
```

Confirm old unit paths with `systemctl --user cat hpatch-router.service` before
removing them, then run `systemctl --user daemon-reload`. Locate any obsolete
binary with `command -v hpatch-router` before removing it. Remove only old
Hpatch-specific provider entries from Codex configuration, preserving auth and
unrelated settings. Use `hpatch codex` for future sessions.

## Go library

The root package, `github.com/yusing/hpatch`, also exposes workspace evaluation,
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
`go test ./cmd/hpatch ./cmd/shell` for process entry points.

## License

MIT. See [LICENSE](LICENSE).
