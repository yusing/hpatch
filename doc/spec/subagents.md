# Third-party native subagents

## REQ-SUBAGENTS-001 — Grok-backed native subagents

`--grok` enables `grok:grok-4.6` in Hpatch mode. Passthrough mode rejects the flag.
Without it, the existing model catalog, collaboration schemas and OpenAI routing remain unchanged;
a `grok:` request fails locally rather than sending it to the OpenAI provider.

The router appends a Grok model to the authenticated Codex model catalog, retaining the catalog's
native v2 instruction and executor metadata. The entry advertises text/image input, the 500,000-token
context window, and low/medium/high/xhigh reasoning (high by default). It does not inherit OpenAI's
Responses Lite transport, hosted search, service tiers or upgrade schedule. Existing catalog entries
remain unchanged. A conflicting alias or a catalog without a native v2 template fails explicitly.
Codex must fetch the enabled catalog before it can select the model; a new session is required when
switching from an unbridged session or a stale custom catalog.

Only a native child request with one `x-openai-subagent: collab_spawn` header, a thread ID and valid
`subagent_kind: thread_spawn` metadata may use the Grok route. Codex owns spawning, listing, messaging,
waiting, interruption, follow-up, tool execution, permissions and sandboxing. The router never
creates a substitute agent process or executes a tool itself.

When enabled, the provider-visible collaboration namespace is `hpatch_collaboration`. Its message
schema has no OpenAI encryption annotation. Response calls are restored to the original native
`collaboration` namespace; spawn, send-message and follow-up calls carry the explicitly empty
`encrypted_function_args` marker required by Codex for plaintext delivery. Replay consistently maps
native calls back to their provider-visible identities. Both ordinary and additional-tool catalogs
are covered. The reserved OpenAI collaboration schema is not modified in place, because the provider
rejects that operation. User-visible agent lifecycle remains native.

Grok uses streaming Chat Completions, either through the public xAI API with `XAI_API_KEY`, or through
the Grok CLI chat proxy with the existing Grok OAuth credential store. An API key takes precedence.
`--grok-auth-file` selects a different credential file; otherwise the router uses the current user's
Grok OAuth store. This route supports the standard `https://auth.x.ai` Grok public client, not custom
enterprise issuers. Grok owns interactive login; Hpatch refreshes expired/near-expiry OAuth tokens
and retries one rejected access token. Refresh uses Grok's cross-process advisory lock, re-reads
credentials under that lock, and atomically saves rotated credentials while preserving unrelated
accounts and fields. No credentials, provider error bodies, or prompts enter diagnostics.
Codex credentials and internal account/thread headers are never forwarded to Grok, and Grok credentials
never reach OpenAI. Credential-bearing requests do not follow redirects.

The adapter preserves supported text/image messages, plaintext agent messages, custom/function tool
calls and their identities/results, parallel calls, structured output, reasoning effort and usage.
Custom tool grammars remain explicit input instructions and are still validated by their existing
router/executor owners. Provider-hosted OpenAI search is not offered on the Grok route; the model is
informed of its absence. Other unsupported provider tools/content fail explicitly rather than being
silently approximated. A non-null `max_output_tokens` fails before inference: Chat completion
limits exclude reasoning and cannot enforce the Responses total output budget. Encrypted agent messages, encrypted reasoning, opaque provider file IDs and
provider-only history items cannot be translated. Fresh-context spawning (`fork_turns=none`) avoids
inherited OpenAI encrypted history; unsupported history must fail without a provider request.

Text and content-free progress stream while complete tool arguments are buffered and validated.
Truncated streams and malformed/unknown tool calls never become successful executable results.
JSON clients receive the equivalent terminal Responses object. Cancellation and stream inactivity
limits propagate to the upstream HTTP request; request start and execution have distinct lifetimes.
Metrics measure the actual Chat Completions transport and provider usage, not estimated usage from
synthesized Responses events. Chat completion and reasoning counts are combined into Responses
output tokens, retaining reasoning as a breakdown rather than counting it twice. Provider-returned model metadata is not rewritten to disguise aliases.

Acceptance:

1. Disabled routing preserves existing OpenAI behavior; enabled catalog registration allows native
   model validation without changing Codex configuration files or launching another agent runtime.
2. JSON/SSE projection and replay preserve native agent call IDs and plaintext handoffs, including
   messages sent while a child runs and follow-ups after it completes or is interrupted.
3. A Grok-generated custom/function tool call is executed once by Codex, replayed with its result,
   and followed by a native child final answer.
4. Image content, parallel tool results, structured output and supported reasoning settings survive
   conversion; unsupported/encrypted data fails before inference.
5. Fresh credentials, concurrent refresh, token rotation, untrusted discovery, redirects and rejected
   tokens preserve credential separation and the shared credential store.
6. Downstream cancellation aborts upstream work; missing terminal markers, partial tool arguments,
   provider errors and idle timeouts cannot be reported as completed responses.
7. Provider transport, usage, final output and tool-call metrics remain correlated and sanitized.
