# Agent guide

Use this file as the short architecture and navigation guide. Read `README.md` for
installation and user-facing behavior, `doc/spec/interface.md` for the complete
interface contract, and `doc/architecture/index.md` for approved ownership rules.
Do not duplicate HPATCH syntax or tool-call mechanics here; `hpatch --help`,
`hpatch --tool-help`, `tool_description.md`, and `tool_grammar.lark` own those
details.

## System model

The hpatch/router path uses one edit engine and two thin binaries:

- The root `hpatch` package is the reusable engine. It owns HPATCH/2 parsing,
  workspace confinement, immutable baselines, target verification, conflict
  detection, language validation, atomic change planning and commit, patch
  translation, reports, diagnostics, hooks, and engine metrics.
- `cmd/hpatch` exposes that engine as the standalone `hpatch` CLI. Normal mode
  evaluates and commits atomically; `translate` evaluates the same script without
  mutation and emits an `apply_patch` envelope.
- `internal/router` owns the Codex Responses integration: HTTP serving, Codex
  authentication and upstream forwarding, request tool rewriting, response
  transformation, built-in and configured tool plugins, bounded session/history state,
  rebuilding, replay, router metrics, and the dashboard.
- `cmd/hpatch-router` is only the process entry point. It installs signal
  cancellation, dispatches the generic private plugin worker mode, calls `router.Run`,
  and maps errors to exit codes.

There is no second edit engine in the router and no separate top-level
`hpatch-router` package.

## Routed request flow

The normal hpatch-mode path is:

1. Codex sends `POST /v1/responses` to `hpatch-router`.
2. `internal/router/server.go` bounds and parses the request, derives the routing
   session, and requires valid turn metadata with exactly one usable workspace.
3. `internal/router/hpatch_proxy.go` retains the original Code Mode tool state,
   removes the model-visible `apply_patch` surface, and installs standalone
   grammar-constrained `hpatch`, `hread`, and `hgrep` tools.
4. `internal/router/client.go` validates the Codex-managed bearer token and
   account ID, then forwards the rewritten request to the ChatGPT Codex backend.
5. When a terminal response contains an `hpatch` call, the router invokes
   `hpatch.TranslateForHost` against the declared workspace. The engine evaluates
   the complete script once without mutating files and returns a translated
   patch, final-state report, diagnostics, repair context, and metrics.
6. The router restores the response to the Code Mode shape Codex expects,
   replacing the standalone call with a `functions.exec` carrier containing the
   generated `apply_patch` operation and hpatch report.
7. Codex executes that carrier under its normal sandbox and permissions and
   presents the normal diff. The router itself does not silently write the
   translated workspace changes.

`hread` and `hgrep` are TypeScript-authored built-in plugins. The router embeds their
Bun-generated JavaScript module and routes both through the generic plugin registry,
translator, wrapper, and private worker. Their exec carriers run in Codex's actual
working directory, so Codex continues to enforce filesystem and process permissions.

A rejected script changes nothing. The engine owns evaluation diagnostics and
exact command repair context. The router owns bounded, workspace-and-session
scoped rejected-script history and converts a later indexed correction into one
complete script before sending it through the ordinary engine boundary. The
core engine has no correction mode.

Passthrough mode forwards Responses traffic without installing hpatch or any plugin.

## Ownership and change boundaries

Start with the owner that matches the behavior:

| Behavior | Authoritative area |
| --- | --- |
| Public engine entry points and workspace capability | `run.go` (`Workspace`, `RunWorkspace`, `Apply`, `Translate`, `TranslateForHost`) |
| Script parsing, targets, edit planning, transactions, translation, reports, hooks, and engine metrics | Root-package `*.go` files and adjacent tests |
| Shared quoted-string and heredoc framing | `internal/hpatchsyntax` |
| Standalone CLI arguments, streams, help, and exit status | `cmd/hpatch` |
| Router lifecycle and HTTP endpoints | `internal/router/server.go` |
| Codex authentication and upstream Responses transport | `internal/router/client.go` |
| Tool replacement, host translation, response restoration, replay, and corrections | `internal/router/hpatch_proxy.go` |
| Built-in hread and hgrep declarations, parsing, and execution | `internal/router/toolplugin/src/builtin` |
| Router process signals and top-level exit behavior | `cmd/hpatch-router/main.go` |
| Interface requirements | `doc/spec/interface.md` |
| Stable ownership contracts | `doc/architecture/index.md` |

Preserve these boundaries:

- Do not reimplement parser, evaluator, target, transaction, or patch-rendering
  semantics in the router.
- Do not move Codex transport, authentication, tool exposure, session, replay, or
  correction ancestry into the root engine.
- Corrections must rebuild a complete script before engine evaluation.
- Router translation must remain non-mutating; Codex executes the generated
  carrier.
- Workspace authority is a pinned `*os.Root`. Router translation verifies that
  the declared workspace remains unchanged around engine evaluation.
- Keep translated history in the Code Mode carrier shape even though the model
  sees standalone registry tools.
- Treat metrics and hooks as auxiliary: their failures must not replace a
  successful edit, read, translation, or rejection diagnostic.
- Keep benchmark and dashboard concerns out of the edit-engine ownership model.

## Focused validation

Use the cheapest package-level check that covers the changed owner:

- Root engine behavior: `go test .`
- Standalone CLI/help contract: `go test ./cmd/hpatch`
- Router request, response, correction, workspace, hread, or transport behavior:
  `go test ./internal/router`
- Plugin TypeScript behavior: `bun test ./internal/router/toolplugin/tests`
- Router process entry point: `go test ./cmd/hpatch-router`
- Cross-package or broad contract changes: `go test ./...`

Important router contract tests include:

- `internal/router/hpatch_proxy_test.go` for tool exposure, translation, response
  restoration, and replay.
- `internal/router/hpatch_correction_test.go` for rejected-script corrections.
- `internal/router/hpatch_root_test.go` for workspace routing and confinement.
- `internal/router/server_test.go` for server/request behavior.
- `internal/router/codex_e2e_test.go` for the Codex-facing end-to-end carrier
  contract.
- `cmd/hpatch/main_test.go` for the standalone public help and CLI contract.

The project targets Go 1.26. The broad development checks documented in
`README.md` are `go test ./...`, `go vet ./...`, and
`go install ./cmd/hpatch ./cmd/hpatch-router`.
