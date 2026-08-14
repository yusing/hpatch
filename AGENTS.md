AGENTS.md

`README.md` is the source of truth for installation, deployment, user-visible router and shell workflows, and requirements. For those tasks, open its relevant section before inspecting implementation; this documentation-owner rule overrides any general preference to inspect code first. Update it when the requested change alters one of those user-visible surfaces. Use `doc/spec/interface.md` for normative engine, router, plugin, shell, hread/hgrep, recovery, and metrics contracts; open it when behavior or acceptance criteria are in question. Use `doc/architecture/index.md` for stable ownership boundaries; open it before moving responsibilities. Use `contrib/codex/file-editing-instructions.md` and `tool_grammar.lark` only when editing or validating HPATCH syntax or Codex model guidance.

## Hpatch's target guidance

Who is hpatch for?

The agent, YOU. Think from your perspective, not user's.

## Quick workflow

1. Identify the authoritative owner in the table below.
2. Open only the contract or help surface that governs the requested behavior.

3. Resolve unknown paths with one scoped search before reading; do not probe guessed conventional locations.
4. Make the narrowest owner-local change; keep generated files and downstream carriers derived from their sources.
5. Run the cheapest check covering every changed owner and report the result.

Completion means every requested behavior is implemented in its authoritative owner, affected contracts agree, and one focused check has had a chance to falsify each changed boundary.

## System model

The hpatch/router path uses one reusable edit engine and one thin router binary:

- The root `hpatch` package owns HPATCH/2 parsing, workspace handling, immutable baselines, target verification, conflict detection, language validation, atomic planning and commit, translation, reports, diagnostics, hooks, and engine metrics.
- `internal/router` owns Responses HTTP and authentication, Code Mode extraction and restoration, the immutable plugin registry and authenticated frontends, recovery and replay, shell and private-tool routing, router metrics, and the dashboard. Passthrough mode skips registry construction and replacement surfaces.
- `cmd/hpatch-router` installs signal cancellation, dispatches private plugin workers, calls `router.Run`, and maps errors to exit codes.

There is no second edit engine in the router and no separate top-level `hpatch-router` package.

Deployment invariant: in hpatch mode, the router and Codex executor must see the frontend directory, authenticated tool snapshot, and router executable at the same absolute paths. The frontend directory must precede unrelated commands on the executor's trusted `PATH`. Isolated runtime filesystems are valid when those mounts are provided; this is separate from the declared workspace.

## Routed request flow

1. Codex sends `POST /v1/responses`; request parsing rejects malformed or unsupported request framing, including background Responses requests.
2. In hpatch mode, the server derives the routing session and validates `x-codex-turn-metadata`; its `workspaces` member is an optional directory hint rather than a request requirement.
3. The proxy finds exactly one supported Code Mode custom `exec` owner, strips native `apply_patch` and `exec_command`, preserves siblings, and installs model-visible `functions.hpatch` and `functions.shell`. Configured contributions marked model-visible join that catalog; the existing Responses instructions remain byte-equivalent.
4. `internal/router/client.go` validates Codex-managed credentials and forwards the rewritten request to the Codex backend.
5. A terminal hpatch call is translated once by `TranslateForHostAt` without mutating files. A selected metadata directory resolves relative operands; without one, only absolute operands are valid and router cwd is never used. Retained `@shell/` edits instead use router-owned session storage and `ApplyForHostRoot`.
6. Response transformation restores the original Code Mode carrier shape, replacing the routed call with a validated native `functions.exec` carrier and generated `apply_patch` operation while preserving model-visible history for replay.
7. Codex executes the carrier under its own working directory, sandbox, permissions, process-session facilities, and visible diff. The router does not silently commit declared-workspace translation.

Shell calls use the generic plugin carrier and forward the native executor's complete result. Hread and hgrep are private authenticated frontends invoked through `functions.shell`, not standalone model-visible tools.

When changing Code Mode owner discovery or sibling preservation, open `doc/spec/interface.md` and the CLI-shape tests in `internal/router/hpatch_proxy_test.go` plus the app-server/request tests in `internal/router/server_test.go`. The supported owner is one custom `exec` under the leading `additional_tools` item: nested under `functions` for CLI traffic or direct for app-server traffic. Unsupported direct and top-level layouts fail closed.

A rejected script changes nothing. The engine owns evaluation diagnostics and repair context. The root text editor owns ordinary target semantics against an in-memory rejected-script baseline. The router owns bounded rejected-script ancestry, recovery detection, and complete-script reevaluation. Passthrough mode forwards Responses traffic without installing hpatch or plugins.

## Ownership and change boundaries

Start with the owner that matches the behavior:

| Behavior | Authoritative area |
| --- | --- |
| Public engine entry points and workspace APIs | `run.go` |
| Parsing, targets, edit planning, transactions, translation, reports, hooks, and engine metrics | Root-package `*.go` files and adjacent tests |
| Shared quoted-string and heredoc framing | `internal/hpatchsyntax` |
| Router lifecycle, modes, and HTTP endpoints | `internal/router/server.go` |
| Codex authentication and upstream Responses transport | `internal/router/client.go` |
| Tool replacement, host translation, response restoration, replay, and rejected-script recovery | `internal/router/hpatch_proxy.go`, `internal/router/hpatch_recovery.go` |
| Carrier catalog and model-visible projection | `internal/router/tool_carrier.go`, `internal/router/tool_registry.go` |
| Built-in tool sources and private execution runtime | `plugins`, `internal/router/toolplugin` |
| Configured plugin discovery, authenticated snapshots, and frontends | `internal/router/toolplugin/runtime.go`, `internal/router/tool_registry.go`, `internal/router/tool_wrapper.go` |
| Router process signals and top-level exit behavior | `cmd/hpatch-router/main.go` |
| Normative interface requirements | `doc/spec/interface.md` |
| Stable ownership contracts | `doc/architecture/index.md` |

Preserve these boundaries:

- The root package is the only edit engine; parser, evaluator, target, transaction, and patch-rendering semantics stay there.
- The router owns Codex transport, tool exposure, sessions, replay, and rejected-script ancestry; recovery mutations rebuild one complete script through ordinary root APIs.
- Normal router translation is non-mutating and directory-based through `TranslateForHostAt`. Retained shell application is a separate root-scoped path through `ApplyForHostRoot`.
- Workspace authority changes must preserve this directory-based versus root-scoped split across code, `doc/spec/interface.md`, and `doc/architecture/index.md`.
- Routed history stays in the original Code Mode carrier shape even though the model sees standalone registry tools.
- `functions.hpatch` and `functions.shell` are model-visible; hread and hgrep remain private shell frontends.
- Codex owns executor cwd, sandbox, permissions, process sessions, and final patch application.
- Metrics, hooks, dashboards, and diagnostics remain auxiliary; their failures cannot replace successful edits, reads, translations, or command results.
- Benchmark and dashboard concerns stay outside edit-engine ownership.

## Complexity gate

Before adding limits, retries, compatibility, validation, buffering, history, metrics, or defensive branches, name the authoritative owner and a concrete local failure. Prefer the smallest owner-local protection and reuse an existing boundary instead of duplicating it. If the owner, reproducer, or invariant is unknown, inspect code and tests before editing. `doc/architecture/index.md` owns the complete project-specific gate.

## Focused validation

Run the cheapest check covering every changed owner:

| Changed owner | Focused check |
| --- | --- |
| Root engine | `go test .` |
| Router request, response, recovery, workspace, plugin, or transport | `go test ./internal/router` |
| TypeScript plugin source | `go generate ./internal/router/toolplugin`, then `bun test ./internal/router/toolplugin/tests` |
| Router process entry point | `go test ./cmd/hpatch-router` |
| Cross-package or broad contract | `go test ./...` |
| Specification, architecture, or local documentation links | `pjdoc validate --scope root` |

Use `go test ./...` only when a change crosses package owners. Run `go vet ./...` for broad Go checks and `make install` when validating generation, `hpatch-router` installation, and Codex instructions. Run `pjdoc validate --scope all` only before claiming project-wide documentation integrity.

Targeted router falsifiers include:

- `internal/router/hpatch_proxy_test.go` for tool exposure, translation, response restoration, and replay.
- `internal/router/hpatch_recovery_test.go` for rejected-script recovery.
- `internal/router/hpatch_root_test.go` for workspace routing and retained root application.
- `internal/router/server_test.go` for server and request behavior.
- `internal/router/codex_e2e_test.go` for the Codex-facing carrier contract.

The project targets Go 1.26. Broad development commands are documented in `README.md`.
