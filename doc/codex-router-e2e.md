# Codex router end-to-end behavior

The observations in this section were verified on 2026-07-28 with Codex CLI
0.145.0, `codex-dynamic`, and `gpt-5.6-luna`. Treat them as the current host
behavior, not an eternal Codex contract. Re-run the E2E checks after a Codex
upgrade before changing routing assumptions.

### Codex provides one implicit hpatch worktree

- A Codex session started in a directory inside this Git repository declares the
  enclosing repository root as its translation root. A nested `-C` directory does
  not create another hpatch worktree.
- `--add-dir` adds a sandbox writable root, but Codex CLI 0.145.0 does not include
  that directory in the `x-codex-turn-metadata` `workspaces` map. This remains
  true for another directory inside this repository and for a separate external
  root. Never use the sandbox summary or `--add-dir` as evidence that hpatch can
  translate files there.
- A standalone non-repository `/tmp` workdir, even with
  `--skip-git-repo-check`, supplies no usable translation root. The router rejects
  the request with `hpatch rewrite requires exactly one usable workspace`.
- hpatch has no workspace-selection protocol. Scripts contain ordinary hpatch
  commands only; do not introduce `workspace_id`, workspace lists, dynamic
  workspace developer messages, patch rebasing, or multi-workspace routing unless
  a real Codex request demonstrates that requirement.

The router uses the one canonical root internally while running server-side
translation. Codex remains responsible for permission checks when it applies the
generated patch. If metadata contains zero or multiple distinct usable roots, the
router fails closed instead of selecting one.

### Interpret E2E output at the router boundary

- The router converts an upstream `hpatch` call into the Code Mode carrier visible
  to Codex. The Codex transcript can therefore label a successful hpatch operation
  as `apply patch`. That label does not prove that the model selected the native
  `apply_patch` tool; inspect router behavior or the reconstructed call before
  making that claim.
- Do not trust the model's final prose as evidence of the selected workspace or
  created path. In the `--add-dir` experiment, Luna described the requested
  external target even though the tool output showed that it created a similarly
  named path under the only routed workspace. Verify the tool result and the
  filesystem.
- The router currently has no `/v1/models` endpoint. Codex may log model-refresh
  `404 Not Found` errors before a request; those messages are non-fatal when the
  subsequent `/v1/responses` request succeeds.

### Reproduction shape

Start the router from this repository:

```sh
go run ./cmd/hpatch-router --listen 0.0.0.0:8080
```

For a non-interactive Codex probe, global options such as approval policy must
appear before the `exec` subcommand:

```sh
codex --local-provider codex-dynamic --oss --model gpt-5.6-luna \
  --sandbox workspace-write --ask-for-approval never \
  exec --ephemeral -C /absolute/path/inside/this/repository "PROMPT"
```

Use a temporary directory inside the repository for edit-producing probes,
constrain the prompt to that directory, verify the actual output path, and remove
the temporary artifacts afterward. Stop the router when the probe is complete.
