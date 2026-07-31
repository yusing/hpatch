# hpatch

A Codex Responses router that lets agents edit with compact selectors and replacement text instead of emitting full patches.

`hpatch-router` sits between Codex and the Responses API. It replaces the Code Mode `apply_patch` surface with constrained `functions.hpatch`, resolves successful scripts against the declared workspace, and hands Codex a real `apply_patch` carrier so sandbox checks and the normal diff UI remain intact. The repository also includes the standalone `hpatch` CLI and reusable Go engine used by the router.

TL;DR:

| Goal | Start here |
| --- | --- |
| Route Codex edits through hpatch | [Install and configure the Codex router](#codex-router-systemd-user-service) |
| Make Codex prefer hpatch | [Override the base instructions](#override-base-instructions-prefer-hpatch-over-apply_patch) |
| Inspect measured token savings | [Metrics](#metrics), then run `hpatch gain` |
| Use the engine without Codex | [Standalone CLI](#standalone-cli) |
| Read the full editing contract | `hpatch --help`, `hpatch --tool-help`, [`doc/spec/interface.md`](doc/spec/interface.md) |

## Why hpatch?

A direct Code Mode edit makes the model repeat patch framing, old context, replacement text, and the JavaScript carrier. The router moves patch reconstruction out of model output:

```mermaid
flowchart LR
    subgraph output["Alternative model-output payloads"]
        H["hpatch path<br/>functions.hpatch + selectors + replacement"]
        A["apply_patch baseline<br/>functions.exec + JavaScript carrier<br/>+ old context + replacement + patch framing"]
    end

    subgraph router["Router and Codex after model output"]
        B["Router reads the immutable<br/>workspace baseline"]
        C["Router generates the<br/>apply_patch envelope"]
        D["Codex applies the patch<br/>sandbox checks + normal diff"]
    end

    H --> B --> C --> D
    A --> D
```

The patch is not eliminated: the router generates it after inference. Savings are the difference between the two model-output payload estimates shown above. State reports, rejection diagnostics, and the net cost of installing the hpatch tool definition while removing the Code Mode `apply_patch` section are tracked separately as input overhead. These are reproducible GPT-5 token estimates, not provider billing totals.

For an 11-line function replacement, hpatch asks the model for this:

```text
functions.hpatch
in parser.go
rsel 50:60
type <<PATCH
func parse(input []byte) (Document, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return Document{}, fmt.Errorf("tokenize: %w", err)
	}
	document, err := buildDocument(tokens)
	if err != nil {
		return Document{}, fmt.Errorf("build document: %w", err)
	}
	return document, nil
}
PATCH
```

The same edit through direct `apply_patch` in Code Mode:

```text
functions.exec
const result = await tools.apply_patch(`*** Begin Patch
*** Update File: parser.go
@@
-func parse(input []byte) (Document, error) {
-	tokens := tokenize(input)
-	if len(tokens) == 0 {
-		return Document{}, errEmptyInput
-	}
-	document := buildDocument(tokens)
-	if document.Empty() {
-		return Document{}, errEmptyDocument
-	}
-	return document, nil
-}
+func parse(input []byte) (Document, error) {
+	tokens, err := tokenize(input)
+	if err != nil {
+		return Document{}, fmt.Errorf("tokenize: %w", err)
+	}
+	document, err := buildDocument(tokens)
+	if err != nil {
+		return Document{}, fmt.Errorf("build document: %w", err)
+	}
+	return document, nil
+}
*** End Patch
`);
text(result);
```

The direct call repeats all 11 old lines, then writes the same 11 new lines plus patch framing and the JavaScript carrier. hpatch writes the new function once; the router reads the old region from the baseline.
The router also supplies `functions.hpatch` to the provider with a [Lark grammar](https://developers.openai.com/api/docs/guides/function-calling#context-free-grammars). As the model writes the tool call, only tokens that can still lead to a valid script are allowed. Bad syntax never becomes a finished tool call, so there is no generate-reject-retry cycle for it. Grammar is syntax only: a valid script can still fail for missing files, ambiguous selectors, or conflicting edits, and those failures stay atomic.

## Requirements

- Go 1.26 or newer (`go install` only; no clone required for normal use)
- For the router: Codex CLI with ChatGPT file auth (`codex login`, credentials at `~/.codex/auth.json` or `$CODEX_HOME/auth.json`)

## Codex router (systemd user service)

The router listens on HTTP, rewrites Responses traffic so Codex calls `functions.hpatch`, evaluates scripts against the workspace declared in `x-codex-turn-metadata`, and returns the translated `apply_patch` carrier to Codex. You see a real diff in the UI rather than a silent file rewrite.

On each request it strips the Code Mode `### apply_patch` section from the `functions.exec` / `additional_tools` description and installs a standalone `functions.hpatch` tool instead. That rewrites only the tool **definition** the model is given for that turn; the history may still label the apply step as `apply_patch` because that is what Codex actually runs.

Defaults:

| Setting | Default |
| --- | --- |
| Listen | `127.0.0.1:8080` (`--listen`) |
| Upstream start timeout | `10m` (`--timeout`) |
| Auth | `~/.codex/auth.json`, or `$CODEX_HOME/auth.json` |
| Metrics / hooks | `$XDG_CONFIG_HOME/hpatch` or `~/.config/hpatch` |
| Endpoints | `POST /v1/responses`, `GET /` (dashboard), `GET /api/metrics` |

The process must run as your login user so it can open the absolute workspace paths Codex sends and read your Codex credentials. A user systemd unit is the intended long-running setup.

### Install the binary

```sh
go install github.com/yusing/hpatch/cmd/hpatch-router@latest
```

Default install path is `~/go/bin/hpatch-router` (or `$GOBIN/hpatch-router` if `GOBIN` is set). Ensure that directory is on your `PATH`.

### Install and start the unit

The unit template is published at  
[`contrib/systemd/hpatch-router.service`](contrib/systemd/hpatch-router.service)  
(raw URL works after the repo is on GitHub):

```sh
mkdir -p ~/.config/systemd/user
curl -fsSL https://raw.githubusercontent.com/yusing/hpatch/main/contrib/systemd/hpatch-router.service \
  -o ~/.config/systemd/user/hpatch-router.service
systemctl --user daemon-reload
systemctl --user enable --now hpatch-router.service
systemctl --user status hpatch-router.service
```

Optional: keep the service after logout:

```sh
loginctl enable-linger "$USER"
```

One-shot without the unit (still uses the installed binary):

```sh
hpatch-router --listen 127.0.0.1:8080
```

If auth lives outside `~/.codex`, or the binary is not in `~/go/bin`, use a drop-in:

```sh
systemctl --user edit hpatch-router.service
```

```ini
[Service]
Environment=CODEX_HOME=%h/.codex
# ExecStart=
# ExecStart=%h/.local/bin/hpatch-router --listen 127.0.0.1:9090
```

Then `systemctl --user daemon-reload && systemctl --user restart hpatch-router.service`.

### Point Codex at the router

Add a Responses provider in `~/.codex/config.toml` (or another Codex profile config under `~/.codex/`):

```toml
[model_providers.hpatch]
name = "hpatch"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"
requires_openai_auth = true
```

Make it the default for the whole config:

```toml
model_provider = "hpatch"
```

Or pick it per invocation (same pattern as other local providers):

```sh
codex --local-provider hpatch --oss
```

Profiles work the same way: put the `[model_providers.*]` block in the profile config (or the main config), then run with `--profile <name> --local-provider <provider> --oss`.

Start sessions from a git worktree: the router requires exactly one usable absolute workspace root in turn metadata.

Useful checks:

```sh
systemctl --user status hpatch-router.service
journalctl --user -u hpatch-router.service -f
curl -sS http://127.0.0.1:8080/api/metrics
# open http://127.0.0.1:8080/ for the local dashboard
```

### Override base instructions (prefer hpatch over apply_patch)

The router exposes `functions.hpatch` and strips `apply_patch` from the Code Mode `functions.exec` definition, but Codex’s **default base prompt** still tells the model to use `apply_patch` for local edits, and a runtime `ALL_TOOLS` dump can still list `tools.apply_patch`. Point Codex at a custom base-instructions file and replace that section so the model prefers hpatch even when it can see both.

1. Fetch a recent copy of the Codex default base prompt (keep the rest of the file; only replace the file-editing section):

   - <https://github.com/openai/codex/blob/main/codex-rs/protocol/src/prompts/base_instructions/default.md>
   - <https://github.com/asgeirtj/system_prompts_leaks/blob/main/OpenAI/Codex/gpt-5.6.md>

2. In the file-editing section (often `## File editing` or `## File editing constraints`), replace only the `apply_patch` guidance with [`contrib/codex/file-editing-instructions.md`](contrib/codex/file-editing-instructions.md). Leave dirty-worktree handling and the non-destructive git rules as in the default base prompt; those are not hpatch-specific.

3. Point Codex at your file in `~/.codex/config.toml` (or a profile config):

```toml
model_instructions_file = "/absolute/path/to/your/base_instructions.md"
```

Do **not** rely on project `AGENTS.md` alone for this: the stock base prompt still steers file edits toward `apply_patch`. Override the base prompt the same way other host tooling (for example skills) does when it must replace a default section rather than append to `AGENTS.md`.

## Standalone CLI

Install the CLI (requires Go 1.26+; binary lands in `$(go env GOPATH)/bin` or `$GOBIN`):

```sh
go install github.com/yusing/hpatch/cmd/hpatch@latest
```

Apply a script (writes only after the full script validates and stages; success report on stderr):

```sh
hpatch <<'EOF'
in src/app.go
tsel 12 "oldName"
type "newName"
EOF
```

Translate to an OpenAI `apply_patch` envelope without touching files (patch on stdout, pending report on stderr):

```sh
hpatch translate <<'EOF'
new message.txt
type "hello world\n"
EOF
```

Script paths are workspace-relative.

| Surface | Workspace root | Path base inside root |
| --- | --- | --- |
| Standalone CLI | Process current directory, or absolute `--root` | `.`, or `--cwd` (relative or absolute, must stay under root) |
| Codex router | The single usable absolute path from `x-codex-turn-metadata` | Root itself (no CLI flags) |

Translated patches always use root-relative paths. Details: `hpatch --help`.

| Mode | Mutates files? | stdout | stderr |
| --- | --- | --- | --- |
| `hpatch` | Yes, after full validation | empty on success | final-state report |
| `hpatch translate` | No | `apply_patch` envelope | pending final-state report |
| `hpatch gain` | No | metrics report | empty on success |
| `--help` / `--tool-help` / `--version` | No | help or version | empty |

Built-in references:

```sh
hpatch --help
hpatch --tool-help
hpatch translate --help
hpatch --version
```

## Editing language (summary)

Authoritative guidance: `hpatch --help` and `hpatch --tool-help`. Contract: [`doc/spec/interface.md`](doc/spec/interface.md).

Selectors (prefer the first that fits):

1. Complete logical lines or indentation changes: `rsel START:END`
2. Exact non-whitespace content: `tsel FROM_LINE "TEXT" [N]`

Common commands: `in` / `new` / `mv` / `rm`, `type "…"` or `type <<PATCH` … `PATCH`, `del`, `copy` / `cut` / `paste`, `commit`.

Rules worth remembering:

- Start `tsel` at stable non-whitespace content; avoid leading spaces or tabs.
- First `in` of a file freezes an immutable baseline; selectors use that baseline until `commit`.
- Disjoint edits can land together; overlapping replacements fail the whole script atomically.
- Rejection changes nothing. Router-owned retries can replace, delete, or insert failed commands by index without resending the full script.

Multiline example:

```text
in parser.go
rsel 50:60
type <<PATCH
func parse(input []byte) (Document, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return Document{}, fmt.Errorf("tokenize: %w", err)
	}
	document, err := buildDocument(tokens)
	if err != nil {
		return Document{}, fmt.Errorf("build document: %w", err)
	}
	return document, nil
}
PATCH
```

## Metrics

Successful CLI and router edits record paired GPT-5 **output-token** estimates for:

- hpatch: `functions.hpatch` + the model-emitted script
- baseline: `functions.exec` + a fixed program that calls `tools.apply_patch` with the translated envelope

Failed calls charge the full rejected hpatch payload against an empty-patch baseline. Final-state reports, diagnostics, and once-per-session tool definitions are tracked as **input** overhead separately.

```sh
hpatch gain
```

These are reproducible payload estimates, not provider billing totals. They omit reasoning tokens, commentary, and host-specific framing.

Hand-authored scenario comparison (does not update `hpatch gain`):

```sh
go run ./compare
```

## How it works

**CLI:** resolve workspace (`--root` / `--cwd` or process cwd) → parse script → evaluate against an in-memory baseline → stage multi-file result → commit (normal mode) or emit one `apply_patch` envelope (translate).

**Router:** load ChatGPT Codex auth → accept `POST /v1/responses` → expose `functions.hpatch` instead of Code Mode `apply_patch` → on tool call, translate against the single usable workspace from Codex metadata (CLI `--root` / `--cwd` are unused here) → return a carrier that makes Codex apply the real patch and surface the diff.

Workspace selection is host-owned. Zero or multiple usable roots fail closed. Codex still enforces sandbox permissions on apply.

## Project structure

```text
.
├── cmd/
│   ├── hpatch/           # CLI entry point and command-line contract
│   └── hpatch-router/    # Router process entry point
├── internal/
│   ├── router/           # Responses proxy, hpatch translation, auth, metrics, and dashboard
│   ├── hpatchsyntax/     # Shared quoted-string and heredoc parsing
│   └── patchtest/        # Test helper for applying translated patch envelopes
├── compare/              # Hand-authored hpatch vs. apply_patch token scenarios
├── contrib/              # Codex prompt guidance and systemd service template
├── doc/                  # Product brief, interface specification, and architecture index
├── *.go                  # Core parser, editor, workspace, transaction, translation, hooks, and metrics
├── tool_description.md   # Embedded function-tool instructions
└── tool_grammar.lark     # Embedded constrained-decoding grammar
```

Tests live beside the packages they exercise. The root `hpatch` package is the reusable engine; `cmd/hpatch` and `internal/router` call it rather than maintaining separate editing implementations. The router dashboard is embedded from `internal/router/dashboard.html`.

## Documentation

| Doc | Contents |
| --- | --- |
| [`doc/brief.md`](doc/brief.md) | Product brief and scope |
| [`doc/spec/index.md`](doc/spec/index.md) | Spec inventory |
| [`doc/spec/interface.md`](doc/spec/interface.md) | CLI, script, correction, metrics contracts |
| [`doc/spec/comparison.md`](doc/spec/comparison.md) | Token comparison scenarios |
| [`doc/architecture/index.md`](doc/architecture/index.md) | Architecture ownership |
| [`contrib/systemd/hpatch-router.service`](contrib/systemd/hpatch-router.service) | User unit template |
| [`contrib/codex/file-editing-instructions.md`](contrib/codex/file-editing-instructions.md) | Base-prompt replacement for file editing |
| [`AGENTS.md`](AGENTS.md) | Codex router E2E notes for agents |

Library use: module path `github.com/yusing/hpatch`. Importable as a library (`hpatch.Translate`, `hpatch.Workspace`, host metrics helpers); hosts should open an `*os.Root` capability for the workspace before calling in.

## Development

```sh
git clone https://github.com/yusing/hpatch.git
cd hpatch
go test ./...
go vet ./...
go install ./cmd/hpatch ./cmd/hpatch-router
```
