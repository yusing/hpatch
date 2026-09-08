# Router-local diagnostic playback

## REQ-ROUTER-DIAG-001 — Router-local diagnostic playback

In hpatch mode, a user message beginning with `:hpatch_diag <target> [<args...>]`
selects statically authored router payloads instead of a provider response. Leading/trailing
whitespace is ignored. The prefix is case-sensitive and must end at a whitespace boundary.
Arguments may use literal shell quoting, but expansion, redirection, operators, and multiple
commands are not accepted. Missing/unknown targets and invalid arguments produce a local
assistant explanation listing the available target; they do not contact a provider or run tools.

Only the latest actual user message selects playback. String content and text-part arrays are
supported. Tool output, agent messages, quoted examples, code fences, and mentions later in prose
do not trigger it. Passthrough mode and non-diagnostic turns retain their normal forwarding path.
Ordinary hpatch catalog, metadata, host authorization, and runtime requirements still apply.
This command is independent of the opt-in `report_issue` tool in `REQ-DIAGNOSE-001`.

The first target is `cat_write_translation`, with no target arguments. It plays these shell
forms through the existing `REQ-SHELL-001` translation path, not directly constructed patches:

- single/double-quoted heredoc delimiters and either redirection order;
- explicit stdin/stdout descriptors and tab-stripped heredocs;
- empty content, blank lines, final LF, literal shell metacharacters, and UTF-8;
- single/double-quoted paths with spaces/metacharacters, relative workdir paths, and an absolute
  path without a params directive;
- newline/semicolon sequences, a parent created by an earlier command, and repeated writes;
- implicit Bash, explicit Bash/sh, an env Bash selector, and a direct sh selector.

Playback first sends the static `mktemp -d -t hpatch-diag.XXXXXXXXXX` shell payload. Only after
its successful terminal result supplies an absolute, clean diagnostic directory does the router
send write cases. Files are created only by Codex's ordinary host tools, in that isolated
directory. The router neither creates a fixture directory itself nor removes its contents.
The files remain for inspection, and the final local response reports their directory.

Each following response contains progress identifying the case and one fixed shell payload.
The router advances from validated tool-call history and successful terminal tool outputs, not
from a second mutable playback session store. It never replays a case whose result is already
in that turn's history. The existing bounded history retains diagnostic provenance. Missing,
duplicate, output-only, out-of-order, failed, truncated/unrecognized, or yielded outputs stop automatic
playback with an explanation. No failed write is retried; native continuation/cancellation
remains host-owned, and a new user message interrupts local playback.

Both JSON and SSE responses pass through the ordinary response transformer, so the client sees
the usual shell and native edit UI. Playback and its continuations bypass upstream forwarding,
CTP encoding, and provider usage fabrication. A subsequent user turn omits the diagnostic command
and router-authored diagnostic messages/calls/results from provider input while retaining unrelated
conversation, catalog, and system/developer items. Earlier provider prefixes remain unchanged.
Historical prefix commands without router-authored diagnostic transcript evidence remain
untouched, including commands previously forwarded in passthrough mode.

Acceptance:

1. `:hpatch_diag cat_write_translation` plays all listed forms without any provider call,
   in both native-tool and Code Mode catalogs and both JSON and SSE transport.
2. Every write fixture reaches the existing cat-write projector; literal file contents,
   overwrite ordering, and before/after commands are checked locally.
3. No write payload is released before successful isolated-directory setup. Failed or pending
   tool results release no next payload, including a failed Code Mode envelope containing an
   earlier successful nested result.
4. Unknown/malformed commands remain local; quoted/prose/tool-output occurrences do not trigger.
5. Subsequent normal requests preserve unrelated history and contain no diagnostic command,
   fixture payload, result, or local progress message. Passthrough requests are unchanged.
