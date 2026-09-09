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
Hpatch mode also retains private durable replay state so resumed and forked conversations restore
their original model-visible tools. This is correctness state, not an operational session log.
It lives at `$XDG_STATE_HOME/hpatch/replay`, or `~/.local/state/hpatch/replay` when that variable is
unset, and survives wrapper shutdown. A relative `XDG_STATE_HOME` is invalid. Passthrough mode
does not open this store. Initialization failure prevents Codex launch. The store admits at most
1 GiB of call replay data and 32 MiB per call record; reaching a limit rejects new records rather
than discarding resumable history. Exact commentary provenance has an independent 16 MiB budget;
failure to retain it suppresses new commentary instead of consuming call-record capacity.
Cleanup is explicit, never inferred from one thread's truncation.
`--capture-output PATH` appends records; `--metrics-output PATH` overwrites a final
snapshot from the same capturer. The destinations must be distinct.

Acceptance:

1. Each invocation owns a bound random loopback port without close-and-rebind races.
2. Codex can reach it immediately on launch; no config or persistent service is changed.
3. Startup failure does not launch Codex; all exits release owned resources.
4. Codex arguments, exit status, terminal input, stdout, and stderr remain intact.
5. Simultaneous configured-plugin sessions have disjoint frontends and independent cleanup.
6. No operational logs or session log files are created or mixed with Codex output. Private replay
   correctness records survive shutdown and are shared safely by simultaneous wrappers.
7. Invalid native editing/execution catalogs and forced incompatible tool choices
   fail closed with actionable HTTP 400 errors, not retryable upstream 502 errors.
8. Fixed listener and provider flags, bare serving, and the former wrap command reject.

### Provider WebSocket transport

The Codex-facing interface remains HTTP: streaming Responses use SSE, and
nonstream requests return terminal response JSON. The ChatGPT Responses path
uses persistent WebSockets by default in both Hpatch and passthrough modes.
Models discovery and Grok keep their existing HTTP transports. No downstream
WebSocket endpoint or Codex WebSocket capability override is introduced.

Each `response.create` carries the complete transformed request input. HTTP's
`stream` field is omitted; incremental `previous_response_id` requests are
rejected rather than silently dropping their history dependency. Existing
`client_metadata` is preserved. Codex's per-request metadata channels carry
turn metadata and sticky turn state, while authentication, account, session,
thread, window, subagent, and capability headers partition connection reuse.
The connection owns no replay or conversation history. A new logical request
never inherits another request's turn metadata or response headers.

Known ancillary events `codex.response.metadata`, `codex.rate_limits`, and
`responsesapi.websocket_timing` remain forwarded and captured but are neutral
to terminal-state validation. Unknown non-Responses event kinds remain invalid.
Responses terminal event types own completion even if the embedded response
omits status; nonstream JSON supplies the missing status from that event type.

A connection serves at most one active response. The pool admits at most 32
connections including pending handshakes, evicts idle entries under pressure,
and waits cancellably when every entry is busy. Idle connections close after
one minute. Connections aged 50 minutes retire before reuse or after their
active response completes. Shutdown closes active, idle, and dialing entries.
Cancellation, early body close, malformed or oversized messages, and a stream
ending before a valid terminal event discard the connection. Individual JSON
messages and reconstructed nonstream output have a 64 MiB router buffer budget.
`--timeout` covers pool wait, handshake, send, and the first non-ancillary
response or error message. An ancillary-only stream does not reset that deadline.
The router buffers at most 64 KiB of ancillary startup messages while deciding
the HTTP status. Successful streaming responses preserve their original order;
an error returns only its structured JSON body and provider status/headers,
while the ancillary prefix remains part of provider capture.
`--stream-idle-timeout` limits gaps between complete WebSocket messages, while
HTTP response streams retain their byte-inactivity timeout.

Message queues, pending-delivery reservations, cancellation, and capture belong
to individual leases, not reusable connections. Receiver admission and lease
handoff are synchronized. A terminal read ends that lease's active receive
phase before reuse; a late callback or reserved delivery cannot target the next
lease. Early close/cancellation waits for already-read messages to be observed
before finalizing capture, including queued and blocked deliveries.

Only an explicit unsupported upgrade response (HTTP 404, 405, or 501), before
any `response.create` write, permits HTTP fallback. Authentication, rate-limit,
and other upgrade errors remain visible. Once a write begins, write failures,
provider error events, and dropped streams never cause transparent replay or
HTTP fallback. Handshake-only headers are not copied to downstream responses;
provider response headers from that handshake belong only to its first exchange.
Valid error-event status and headers remain visible to the HTTP client.

Acceptance:

1. Sequential full-input requests reuse a connection while turn metadata changes
   independently; credential or session changes never share that connection.
2. Concurrent requests cannot interleave responses on one socket. Pool capacity,
   retirement, cancellation, and shutdown release their owned resources.
3. Terminal events finish responses without waiting for socket EOF. Nonstream
   output reconstructs finalized items in index order when the terminal array
   is empty or absent, and applies tool translation once.
4. SSE translation, native tool carriers, CTP/2, provider usage, and capture
   remain integrated; neither nonstream delivery nor Grok needs WebSocket support
   in Codex.
5. Unsupported-upgrade fallback is pre-send only. Failed sends, partial streams,
   and provider error events are not replayed, and poisoned sockets are not reused.
