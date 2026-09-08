# Session-scoped Codex launch

## REQ-ROUTER-001 — Session-scoped Codex launch

`hpatch [flags] codex [Codex arguments...]` starts one private router on an
OS-assigned port bound to `127.0.0.1` and launches Codex from PATH only after
initialization and binding succeed. There is no standalone or daemon command,
fixed-listener flag, custom-provider flag, or installed old-name alias.

Invocation-only provider overrides select the listener, Responses transport,
and Codex-managed authentication against the fixed ChatGPT upstream. Provider
selection in config and profiles is overridden without modifying configuration.
Provider-selection arguments are rejected. Hpatch flags precede `codex`; subsequent
arguments remain intact, including subcommands and `--` delimiters.

Codex inherits cwd, stdin, stdout, stderr, and the environment, augmented only
with `HPATCH_BASE_URL` and the private configured-plugin frontend directory at
the front of PATH. Terminal Ctrl-C remains Codex-owned. SIGTERM to the wrapper
terminates Codex and the router with bounded cleanup. Codex exit, launch failure,
and cancellation clean up owned runtime resources. Ordinary exit status is
preserved; signal exits use `128 + signal`. Unexpected router termination also
terminates Codex rather than leaving a dead provider connection.

After successful binding and before launching Codex, the wrapper prints exactly
one `hpatch dashboard: http://127.0.0.1:PORT/` line to stderr. It does not write
the announcement to stdout or repeat it during the active Codex UI. The URL and
in-memory metrics belong to this invocation and expire on shutdown.

Operational logging is absent. Startup and cleanup failures are concise stderr
errors outside the active Codex UI. Critical request failures use the user-only
commentary contract. The launcher prints undelivered notices and repetition
summaries after Codex exits. In-memory metrics, explicit sanitized capture and
final metrics exports, and opt-in issue reports are not operational logging.
`--capture-output PATH` appends records; `--metrics-output PATH` overwrites a final
snapshot from the same capturer. The destinations must be distinct.

Acceptance:

1. Each invocation owns a bound random loopback port without close-and-rebind races.
2. Codex can reach it immediately on launch; no config or persistent service is changed.
3. Startup failure does not launch Codex; all exits release owned resources.
4. Codex arguments, exit status, terminal input, stdout, and stderr remain intact.
5. Simultaneous configured-plugin sessions have disjoint frontends and independent cleanup.
6. No operational logs or session log files are created or mixed with Codex output.
7. Invalid native editing/execution catalogs and forced incompatible tool choices
   fail closed with actionable HTTP 400 errors, not retryable upstream 502 errors.
8. Fixed listener and provider flags, bare serving, and the former wrap command reject.
