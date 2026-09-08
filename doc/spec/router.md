# Router launch

## REQ-ROUTER-001 — Session-scoped Codex launch

`hpatch-router [router flags] wrap codex [Codex arguments...]` starts the Hpatch router
on an operating-system-assigned TCP port bound to `127.0.0.1`. It launches
`codex` from `PATH` only after router initialization and binding succeed.
The Responses provider points at that listener through invocation-only `-c`
overrides, with Responses transport and Codex-managed OpenAI authentication.
The wrapper does not edit Codex configuration or start a persistent service.
It overrides provider selection from `config.toml` and profiles, uses the
router's default ChatGPT upstream, and does not support custom providers.
Provider-selection arguments (`--oss`, `--local-provider`, and provider-related
`-c`/`--config` overrides) are rejected before starting the router.
Router flags before `wrap`, including `--grok`, apply to the wrapped server.
`--listen` and `--provider-base-url` are rejected in wrapped mode: the listener
remains random and loopback-only, and the upstream remains the default.
Router and Codex arguments are separated using the router's ordinary flag parser,
so a flag value equal to `wrap` is not mistaken for the command.

Codex inherits the working directory, environment, stdin, stdout, and stderr.
Arguments after `codex` are forwarded in order. Terminal Ctrl-C remains under
Codex's control, so canceling a turn does not stop the router. SIGTERM to the
wrapper terminates Codex and shuts down its router, with bounded cleanup.
Codex exit, including nonzero and signal exits, shuts down the router and its
owned runtime resources. Ordinary Codex exit status is preserved; signaled
exits use `128 + signal`. Startup and cleanup failures are reported on stderr.
An unexpected router failure terminates Codex rather than leaving it connected
to a dead provider. No additional executable is installed.

The standalone `hpatch-router [router flags]` workflow remains available.
For both workflows, port-zero listeners publish their actual assigned port
in diagnostics and runtime commentary URLs.

Acceptance:

1. Each live wrapper owns a bound random loopback port, without a port-selection
   close-and-rebind race, and Codex can reach it immediately after launch.
2. Provider overrides select that listener without writing Codex configuration;
   prompts, subcommands, and ordinary Codex options remain intact.
3. Success, nonzero exit, launch failure, and termination leave no wrapper-owned
   listener or runtime snapshot behind.
4. Failed router initialization does not launch Codex.
5. Existing standalone defaults, flags, and shutdown behavior remain available.
6. `hpatch-router --grok wrap codex` enables Grok on the session-scoped router;
   valued router flags remain intact and Codex arguments are not parsed as router flags.
