# Router-local tool plugins

## REQ-PLUGIN-001 — Router-local tool plugins

In hpatch mode, `hpatch-router` discovers tool plugins only from the `hpatch/plugins`
directory beneath the platform user configuration directory. Each direct regular file whose
name ends in `.js` or `.mjs` is one compiled ECMAScript-module declaration, loaded in lexical
filename order; directories, symlinks, and other entries are not declarations. A missing or
empty directory contributes no plugins. There are no plugin-related router flags,
workspace-local discovery, remote discovery, or hot reload. TypeScript is an authoring format,
and the complete registry remains immutable for the router process lifetime. Passthrough mode
neither loads nor exposes the contributed tools.

During declaration validation, translation, and execution, configured and built-in modules may import
`hpatch:core/v1`. This exact virtual ECMAScript module is supplied by the router's authenticated snapshot;
it requires no plugin-owned dependency or copied binary. It exposes deterministic verified-row hashing,
formatting, logical-line counts and UTF-8 byte bounds, positive integer and `LINE:HASH` parsing, quoted
operand decoding, source-format capability classification, Go identifier and string-literal handling,
shell-header parsing, and interpreter identity. An unknown `hpatch:` module fails declaration loading.
The shared core exposes no filesystem, workspace, symlink, process, network, credential, carrier, or
row-resolution authority. Existing declarations that do not import it retain their behavior.

Each plugin declares a stable plugin identity and one or more globally named tools. Each tool
provides its exact OpenAI Responses custom-tool specification, a bounded string-input parser,
a translator, and an executor-side implementation. A specification may omit `format` for
unconstrained text or use an OpenAI grammar format whose syntax is `lark` or `regex`. The
model-visible name, description, format, grammar definition, input limit, translator, and
implementation are part of the validated declaration. Standard JSON-schema function tools,
runtime TypeScript transpilation, and arbitrary undocumented specification fields are not
supported by this increment.
Configured executor-backed names must also differ from shell keywords and built-ins. This rule
ensures that their basename carrier selects an executable frontend instead of shell-owned
behavior.

Before opening its listener or installing any configured contributed-tool wrapper, the router
loads every discovered declaration and validates the complete registry. It reports all detected plugin
schema, API-version, identity, duplicate-name, input, translator, implementation, and wrapper
conflicts, then exits nonzero if any declaration is invalid. Failure exposes no
partial registry, forwards no Responses request, or starts an executor implementation. Locally deterministic grammar syntax and unsupported construct
checks occur at startup; this does not promise to reproduce a provider's model-specific or
complexity limits.

A successful translator returns a typed normal executor tool-call carrier. The router
validates the carrier kind, name, and payload against the tools available in that
request and retains ownership of response item IDs, call IDs, status, JSON and SSE framing,
history, and replay. A plugin cannot invent an unavailable carrier or return a raw Responses
envelope. The plugin API provides a canonical exec wrapper for tools that need one. The wrapper
owns the repeated outer Code Mode exec program, nested tool invocation, serialization, argument
quoting, and result forwarding. Its canonical Bash quoting keeps the worker command on one
physical line, escaping embedded line terminators while reconstructing each exact argv value.
The optional exec command template contains exactly one `{.}`
placeholder, which the router replaces with the complete quoted worker command. For configured
tools this is their frontend command. For built-in shell it is normally the fixed
`shell <interpreter> <program>` helper command; without a shebang, directive, or template, one
physical line containing one static external Bash command instead remains the complete outer
command. An optional JSON parameter object cannot contain `cmd`. The router supplies `cmd` from
the selected command. If the parameter object contains `login`, its value must be exactly `false`.

Requests may expose either the Code Mode custom `exec` owner in `additional_tools` or native
top-level custom `apply_patch` plus function `exec_command`. The router replaces the editing
surface in either shape without opening another listener. In native requests, `exec_command`
remains the executor-owned carrier. Hpatch invokes the executor's `apply_patch` command through
that carrier and returns the already-rendered report as its exact successful output; ordinary
exec-backed contributions use direct native function arguments rather than a Code Mode wrapper.
Response restoration and replay retain the request's original carrier shape.

For each configured executor-backed contributed tool, startup creates or verifies a stable
executable symlink beside the running `hpatch-router`. Its basename is exactly the contributed tool name,
and its target is the authenticated process-scoped snapshot wrapper with the same basename.
The snapshot wrapper targets the running `hpatch-router` executable. Without a command template,
the exec wrapper invokes only the basename and represents the parsed model input as its ordered
argv. With a command template, the router replaces `{.}` with that same independently quoted
basename and argv. When launched through both symlinks, the router verifies the stable frontend
location, snapshot identity, wrapper target, and registered implementation before passing the
remaining argv unchanged.
The configured-plugin worker keeps the frontend standard input separate from the JavaScript
host's JSON control stream. The host exposes that input only as a dedicated inherited descriptor during
executor calls.

Built-in shell and its private hread, hgrep, hsymbol, and inspect_file commands are the exception
to that frontend path. The PATH-installed `shell` name is a fixed shared locator, not a snapshot
wrapper or plugin implementation. For each eligible thread, the router writes one direct
`.runtime` link under `hpatch-$CODEX_THREAD_ID` to the current private `shell` wrapper in the
authenticated snapshot. The locator reads that link and replaces itself with its target. Bash
and POSIX evaluation dispatch private commands from the resolved worker after shell expansion, so none of the four
private names creates a snapshot wrapper, stable frontend, or `PATH` dependency.

An executor returns its current stdout, stderr, and exit status once. The worker returns that result
to Codex and never performs a second observation-only execution or returns a benchmark baseline.

Without exec parameters, the carrier supplies no working-directory or environment override.
With exec parameters, the router forwards the JSON values without replacing the request-specific
Codex contract. Codex validates those values and remains the owner of working directory, sandbox,
filesystem, process, network, terminal, and permission enforcement. Missing, conflicting,
incorrectly targeted, or unusable configured-tool symlinks fail startup before the listener
opens. When configured frontends exist, the router holds one exclusive frontend lock for its
process lifetime and a concurrent router fails startup. A built-in-only registry takes no
frontend lock. After a crash releases a configured frontend lock, a later router can replace
authenticated prior frontends even when the prior process snapshot remains.

Translated history retains the plugin identity, original tool name and input, and exact carrier
kind, name, and payload. Replay accepts only the byte-identical retained carrier and restores
the original model-visible call before upstream forwarding. Ordinary plugins do not enter
hpatch recovery ancestry. Runtime model-input rejection returns a bounded diagnostic
through an available executor carrier; a translator protocol violation, unavailable carrier,
or malformed carrier is a routing failure rather than a successful approximation.

Grammar compatibility for this requirement is pinned to OpenAI's Custom tools guide
(<https://developers.openai.com/api/docs/guides/function-calling#custom-tools>): regex
definitions use Rust `regex` syntax and do not support lookarounds or lazy quantifiers; Lark
definitions support common imports and `%ignore` while terminal priorities, templates,
non-common imports, and `%declare` are unsupported. Startup validates this stable subset
locally; provider model-specific and complexity limits remain provider-owned.

Acceptance:

1. A valid discovered JavaScript declaration contributes its exact unconstrained, Lark, or
   regex custom-tool object to hpatch-mode Responses requests without a plugin flag.
2. A missing or empty plugin directory preserves the built-in hpatch-mode behavior, while
   passthrough mode loads and exposes no contributed tools.
3. One invalid declaration or configured-tool symlink prevents the listener from opening;
   independent startup mismatches are reported together and no valid subset is exposed.
4. Duplicate tool names across plugins or built-ins fail startup, and the registry does not
   change until process restart.
5. A plugin may translate to any compatible executor tool call available in the current
   request; an unavailable or wrong-kind carrier rejects before upstream execution.
6. The exec wrapper renders the canonical Code Mode program or native function arguments and independently quotes every argv
   value. An optional template contains exactly one `{.}`, which expands to the complete worker
   command. The plugin declaration does not contain or generate the outer carrier shape.
7. Invoking a configured executor-backed tool resolves its stable basename frontend through the
   authenticated snapshot wrapper to `hpatch-router`, verifies the pinned registry, dispatches
   by `argv[0]`, and delivers the declared argv under Codex's cwd, sandbox, and permissions.
8. JSON and SSE responses preserve call identity while replacing a contributed call with its
   validated carrier. While the complete streaming input is buffered for validation, each withheld
   input delta becomes a content-free native `response.in_progress` event so downstream SSE remains
   active without exposing untranslated content. Native function-argument events replace custom
   input events when the request uses native tools. Replay restores the exact original contributed
   call after verifying the retained carrier.
9. A model-input diagnostic is bounded and recoverable, while an invalid translator result
   cannot be returned or counted as a successful tool call.
10. Observation failure cannot replace an otherwise successful translated carrier or executor
    result; request cancellation still propagates.
11. An executor returns one validated current result and does not run a comparison execution.
12. A configured plugin can import `hpatch:core/v1` and obtains the same verified-row, source, Go lexical,
    and shell-header semantics as built-in contributions. An unavailable core version rejects startup,
    and passthrough mode loads no core artifact.
