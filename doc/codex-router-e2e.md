# Codex router end-to-end behavior

The observations in this section were verified on 2026-07-28 with Codex CLI
0.145.0, `codex-dynamic`, and `gpt-5.6-luna`. Treat them as the current host
behavior, not an eternal Codex contract. Re-run the E2E checks after a Codex
upgrade before changing routing assumptions.

### Codex provides one implicit hpatch base directory

- A Codex session started in a directory inside this Git repository declares the enclosing repository root as its base directory. A nested `-C` directory does not create another hpatch base directory.
- In the Codex CLI 0.145.0 observation, `--add-dir` added a sandbox-writable root but did not add that directory to the `x-codex-turn-metadata` `workspaces` map. This metadata observation predates directory-based translation and must be rerun after Codex or router changes; it is not evidence of router confinement.
- A standalone non-repository `/tmp` workdir, even with `--skip-git-repo-check`, supplied no usable base directory in that observation. The router now forwards such a turn; without a selected directory, absolute hpatch operands work and relative operands reject without falling back to the router process CWD.
- Hpatch receives its base directory outside the script. Do not introduce `workspace_id`, workspace lists, dynamic workspace developer messages, patch rebasing, or multi-directory routing unless a real Codex request demonstrates that requirement.

The router validates and canonicalizes the optional declared base-directory string for normal server-side translation. It does not open a pinned engine root, impose filesystem confinement, or check directory identity before and after evaluation. Relative, `..`, symlink, and absolute operands use ordinary host path resolution when a directory is selected. Without one, only absolute operands are valid and router cwd is never used. Current Codex emits zero or one workspace entry. Codex remains responsible for permission checks when it executes the generated patch carrier.

### Interpret E2E output at the router boundary

- The router converts an upstream `hpatch` call into the Code Mode carrier visible
  to Codex. The Codex transcript can therefore label a successful hpatch operation
  as `apply patch`. That label does not prove that the model selected the native
  `apply_patch` tool; inspect router behavior or the reconstructed call before
  making that claim.
- Do not trust the model's final prose as evidence of the selected base directory or created path. The pre-`ad365ea` `--add-dir` experiment observed a similarly named destination under the only routed directory, but that path-placement result predates directory-based translation and is not a current security guarantee. Rerun the E2E probe and inspect the tool result and filesystem before making a current claim.
- The router serves `GET /v1/models`. A model-refresh `404 Not Found` now indicates a routing or version mismatch and should be investigated rather than treated as an expected router limitation.

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
