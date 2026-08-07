# Interface contract

## REQ-CLI-001 — Modes and informational output

`hpatch [--root ROOT] [--cwd CWD]` reads a complete HPATCH/2 script from standard input,
evaluates its complete change set in memory, stages required filesystem content, and
then commits it. After a successful commit it writes the final active-file state report
defined by `REQ-OUTPUT-001` to stderr. Stdout remains empty.

`hpatch translate [--root ROOT] [--cwd CWD]` performs the same parsing, filesystem
reads, target verification, and in-memory evaluation but never modifies a file. It writes
one OpenAI `apply_patch` envelope that represents the same net change set to stdout, then
writes the pending final active-file state report to stderr.

For normal and translate modes, omitted `--root` means the process current directory
and omitted `--cwd` means `.`. An explicit root must be absolute and is canonicalized
before it is opened. A relative cwd resolves beneath root. An absolute cwd is accepted
only when its canonical location is beneath root. Cwd must identify an existing
directory. The CLI opens root once and uses that pinned capability for the invocation.

`hpatch gain` reads no script and reports the persistent aggregate defined by
`REQ-METRICS-001`. `hpatch --help` is the complete built-in agent reference for
stdin usage, process and editing commands, orchestration, trust boundaries, and
validation. `hpatch --tool-help` emits a separate, shorter model-facing summary of
target choice, baseline rules, and safety. It omits CLI usage and mode descriptions,
root and cwd options, metrics, and version material, and includes workspace-relative path
and parent-directory preparation guidance. It is not a generated slice of top-level help.
`hpatch translate --help` summarizes translate-mode I/O and points to top-level help.
`hpatch --version` writes the module build version, or `devel` for an unversioned build.
Informational commands do not read stdin, resolve a working or configuration directory,
access metrics, or inspect project files. Any other argument list is invalid.

Acceptance:

1. Given valid edits spanning multiple files, normal mode produces the specified final
   paths and contents, empty stdout, and one final-state report on stderr.
2. For LF inputs, applying translate mode's patch-only stdout produces the same final
   paths and UTF-8 contents as normal mode. For other line endings, it represents the
   same logical-line edits subject to the normalization rule in `REQ-OUTPUT-001`.
3. Translate mode leaves the source tree unchanged and reports the pending result rather
   than the unchanged source tree.
4. Gain mode leaves the source tree and metrics unchanged.
5. Each supported informational form writes its complete result to stdout with status
   zero and empty stderr without reading stdin or requiring a valid current directory.
6. Tool help remains a concise model-facing summary of target choice, baseline rules,
   safety, and tool-path guidance while excluding CLI-only material; top-level help remains
   the complete agent reference.
7. Unsupported aliases, trailing arguments, and unknown or future options fail with no
   stdout or final-state report.
8. A nested cwd changes relative path resolution while normal mutations and translated
   patch paths retain the same root-relative file identity.
9. A relative, absolute, or symlink path that escapes root fails without mutation,
   patch output, or final-state report.

## REQ-READ-001 — Routed verified-row reader

In hpatch router mode, the model receives a read-only `hread` custom tool beside
`hpatch`. Its grammar-constrained free-form input contains one to 6 newline-delimited
read specifications. Each specification starts with a bare path containing no whitespace or
a JSON-quoted path, optionally followed by a space and `START:END` for an inclusive bounded
logical-line range, for example `editor.go 40:80`. A single specification remains valid and
retains its existing output. Line endpoints are positive one-based base-ten integers and must
be ordered. The start line must exist. An end line past EOF returns through the final line.
Invalid syntax or a missing endpoint rejects the complete tool input; a reversed range or
start past EOF fails that item.

The router requires a Code Mode `exec` carrier before forwarding an hpatch-enabled
request. It creates or reuses one process-scoped executable shell wrapper named `hread`
and invokes it by absolute path in the translated nested exec command, followed by the
complete grammar input as one shell-quoted argument. The carrier sets neither an
environment override nor a working directory. A direct `apply_patch` carrier or
wrapper-creation failure rejects the rewrite before forwarding.

The Codex executor must see the wrapper directory and the router executable at the same
absolute paths as the router process. A deployment that isolates their filesystems must
provide those runtime mounts separately from the user workspace.

The wrapper launches a private worker in Codex's exec context. Relative paths resolve
from the exec process's current directory; absolute paths retain their ordinary filesystem
meaning. Codex, not the router or hread, owns the sandbox and filesystem permissions.
The worker accepts only regular UTF-8 files and never mutates them. A single read emits
only its requested logical lines. A batch emits one non-row header containing the original
read specification before each item's result, preserves input order, and reports an item's
read error beneath that header without hiding independent successful items. Every
successful logical line retains the single-read form:

```text
LINE:HASH TEXT
```

`LINE` is the positive one-based logical line number. `TEXT` is exact logical-line
content without its terminator. `HASH` is lowercase hexadecimal for the first two bytes
of SHA-256 over that exact content, including leading spaces and tabs. A trailing file
terminator does not create an additional empty line. The router does not execute the
read, duplicate its filesystem rules, or encode its output in an `apply_patch`-shaped
carrier.

For input metrics, hread produces its current and stock results from the same read. The stock result
preserves batch headers, diagnostics, order, selected `TEXT`, and one LF per returned logical line.
It omits the `LINE:HASH ` prefix from each successful row.

Acceptance:

1. A legacy single whole-file or bounded read emits the same exact UTF-8 rows as before.
   Equal lines at different positions have distinct row references, and indentation changes
   the hash.
2. Up to 6 read specifications in one call emit labeled item results in input order.
   A missing, inaccessible, non-regular, non-UTF-8, reversed-range, or start-past-EOF
   item reports a read diagnostic without suppressing successful siblings; an end past EOF
   succeeds through the final line. Invalid batch syntax rejects the complete tool input.
3. The router restores a replayed `hread` call to the original custom-tool shape just as
   it restores routed hpatch calls, without treating reads as editable correction history.
4. Reading and whole-file UTF-8 validation use bounded streaming storage and observe
   cancellation during processing. A single formatted item rejects before exceeding 16 MiB.
   A batch never exceeds the same complete-call bound; if another result would exceed it,
   the batch successfully returns its existing rows followed by a diagnostic instructing the
   agent to retry the remaining items in a narrower batch.
5. Success returns the worker's exact stdout through a successful Code Mode exec call.
   A failing legacy single read retains concise stderr and nonzero shell status, while a
   syntactically valid batch returns per-item diagnostics with successful shell status and
   does not increment hpatch edit-failure counters.
6. Turns and retained-session eviction do not create or own wrapper state. Router
   shutdown removes the process wrapper.
7. Hread input metrics compare returned verified rows with the same result without each
   `LINE:HASH ` prefix. The comparison does not read a file twice.

## REQ-GREP-001 — Routed verified-row search

In hpatch router mode, the model receives a read-only `hgrep` custom tool beside `hpatch`
and `hread`. Its grammar-constrained free-form input is a nonempty, whitespace-separated
ripgrep argument list. Bare arguments, single-quoted arguments, double-quoted arguments,
and backslash-escaped characters preserve one argument each; quoting and escaping perform
no shell expansion or execution. The tool accepts ordinary ripgrep pattern, matching, file,
glob, type, ignore, context-selection, and resource-selection arguments.

The worker invokes the installed `rg` from Codex's exec context with internal
`--json --no-config` arguments. Its process-scoped executable accepts ordinary ripgrep argv;
the routed carrier safely quotes each free-form input argument into the basename command. It
rejects model-supplied output, multiline,
preprocessor, compressed-input, informational, and other modes that cannot identify one
complete editable source row per result. With no explicit path argument, search defaults to
the exec process's current directory. Ripgrep retains ownership of regex parsing, ignore
rules, traversal, matching, and search diagnostics; hgrep does not implement a fallback
search engine.

The worker consumes ripgrep's structured match and context events and emits each first-seen
logical row once in ripgrep result order:

```text
"PATH":LINE:HASH TEXT
```

`PATH` is JSON-quoted. `LINE:HASH TEXT` has exactly the identity and complete UTF-8
logical-line semantics of `REQ-READ-001`; match highlighting, match-only fragments,
replacement output, trimming, and line truncation cannot change it. Multiple matches on one
path and line produce one row. Ripgrep's no-match exit status is a successful empty result.
Execution, syntax, filesystem, encoding, cancellation, and missing-executable failures remain
concise `hgrep` diagnostics with nonzero status. Output contains only complete rows and stays
within 16 MiB; reaching the bound preserves completed rows and adds a limit diagnostic.

For input metrics, hgrep produces its current and stock results from the same ripgrep event stream.
The stock result preserves each JSON-quoted `PATH`, `TEXT`, LF, result order, and diagnostic. It
omits the `LINE:HASH ` portion from each successful row.

The router exposes, translates, and replays hgrep through the same Code Mode exec boundary
as hread. The nested carrier invokes the worker basename and supplies neither environment nor
working-directory overrides, so deployment must place the trusted wrapper directory first on
the Codex executor's PATH. The router does not execute or pre-read the search, and hgrep calls
never enter edit correction history or editing metrics.

Acceptance:

1. A search using a regular expression, explicit path, and glob emits JSON-quoted paths,
   positive line numbers, standard four-digit hashes, and exact complete matching lines that
   can be copied directly into an hpatch target without an intervening hread.
2. Search arguments containing whitespace or shell metacharacters reach ripgrep as literal
   argv values and cannot execute another command. A conflicting output or transformed-input
   mode rejects before ripgrep starts.
3. Requested before/after context emits complete verified rows alongside matches. Repeated
   match or context events on one row emit that row once; no matches return successful empty
   stdout; invalid patterns and unavailable ripgrep return concise nonzero diagnostics.
4. A replayed hgrep call is restored to its original custom-tool name and input, remains
   scoped to its routed workspace session, and is not eligible for hpatch correction.
5. Passthrough mode exposes neither hgrep nor the other replacement tools. Router startup
   fails before serving if its private hgrep worker cannot be installed.
6. Hgrep input metrics compare returned verified rows with the same result without each
   `LINE:HASH ` portion. The comparison does not run ripgrep twice.

## REQ-PLUGIN-001 — Router-local tool plugins

In hpatch mode, `hpatch-router` discovers tool plugins only from the `hpatch/plugins`
directory beneath the platform user configuration directory. Each direct regular file whose
name ends in `.js` or `.mjs` is one compiled ECMAScript-module declaration, loaded in lexical
filename order; directories, symlinks, and other entries are not declarations. A missing or
empty directory contributes no plugins. There are no plugin-related router flags,
workspace-local discovery, remote discovery, or hot reload. TypeScript is an authoring format,
and the complete registry remains immutable for the router process lifetime. Passthrough mode
neither loads nor exposes the contributed tools.

Each plugin declares a stable plugin identity and one or more globally named tools. Each tool
provides its exact OpenAI Responses custom-tool specification, a bounded string-input parser,
a translator, and an executor-side implementation. A specification may omit `format` for
unconstrained text or use an OpenAI grammar format whose syntax is `lark` or `regex`. The
model-visible name, description, format, grammar definition, input limit, translator, and
implementation are part of the validated declaration. Standard JSON-schema function tools,
runtime TypeScript transpilation, and arbitrary undocumented specification fields are not
supported by this increment.
Executor-backed names must also differ from shell keywords and built-ins. This rule ensures that
the basename carrier selects an executable frontend instead of shell-owned behavior.

Before opening its listener or installing any contributed-tool wrapper, the router loads every
discovered declaration and validates the complete registry. It reports all detected plugin
schema, API-version, identity, duplicate-name, input, translator, implementation, and wrapper
conflicts, then exits nonzero if any declaration is invalid. Failure exposes no
partial registry, forwards no Responses request, starts no executor implementation, and
changes no durable metrics. Locally deterministic grammar syntax and unsupported construct
checks occur at startup; this does not promise to reproduce a provider's model-specific or
complexity limits.

A successful translator returns a typed normal Code Mode tool-call carrier. The router
validates the carrier kind, name, and payload against the Code Mode tools available in that
request and retains ownership of response item IDs, call IDs, status, JSON and SSE framing,
history, and replay. A plugin cannot invent an unavailable carrier or return a raw Responses
envelope. The plugin API provides a canonical exec wrapper for tools that need one. The wrapper
owns the repeated outer Code Mode exec program, nested tool invocation, serialization, argument
quoting, and result forwarding. The optional exec command template contains exactly one `{.}`
placeholder, which the router replaces with the complete quoted frontend command. An optional
JSON parameter object cannot contain `cmd`. The router supplies `cmd` from that frontend
command. If the parameter object contains `login`, its value must be exactly `false`.

For each executor-backed contributed tool, startup creates or verifies a stable executable
symlink beside the running `hpatch-router`. Its basename is exactly the contributed tool name,
and its target is the authenticated process-scoped snapshot wrapper with the same basename.
The snapshot wrapper targets the running `hpatch-router` executable. Without a command template,
the exec wrapper invokes only the basename and represents the parsed model input as its ordered
argv. With a command template, the router replaces `{.}` with that same independently quoted
basename and argv. When launched through both symlinks, the router verifies the stable frontend
location, snapshot identity, wrapper target, and registered implementation before passing the
remaining argv unchanged.
The private worker keeps the frontend standard input separate from the JavaScript host's JSON
control stream. The host exposes that input only as a dedicated inherited descriptor during
executor calls.

An executor returns its current stdout, stderr, and exit status once. It may also return one
optional stock result with the same fields. The stock result represents the output that the
displaced stock tool path would have returned for the same operation. The executor computes both
results during the same execution; the router does not invoke a second metric-only implementation.
The worker returns only the current result to Codex. It validates and records the stock result as
metric evidence without allowing it to change the current output or status. When the stock result
is absent or invalid, metrics use the validated current result as its stock result. Invalid optional
metric evidence cannot replace or modify the current executor result.

Without exec parameters, the carrier supplies no working-directory or environment override.
With exec parameters, the router forwards the JSON values without replacing the request-specific
Codex contract. Codex validates those values and remains the owner of working directory, sandbox,
filesystem, process, network, terminal, and permission enforcement. Missing, conflicting,
incorrectly targeted, or unusable symlinks fail startup before the listener opens.
The router holds one exclusive frontend lock for its process lifetime. A concurrent router fails
startup. After a crash releases the lock, a later router can replace authenticated prior
frontends even when the prior process snapshot remains.

Translated history retains the plugin identity, original tool name and input, and exact carrier
kind, name, and payload. Replay accepts only the byte-identical retained carrier and restores
the original model-visible call before upstream forwarding. Ordinary plugins do not enter
hpatch correction ancestry. Runtime model-input rejection returns a bounded diagnostic
through an available Code Mode carrier; a translator protocol violation, unavailable carrier,
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
3. One invalid declaration or tool symlink prevents the listener from opening; independent
   startup mismatches are reported together and no valid subset is exposed.
4. Duplicate tool names across plugins or built-ins fail startup, and the registry does not
   change until process restart.
5. A plugin may translate to any compatible Code Mode tool call available in the current
   request; an unavailable or wrong-kind carrier rejects before upstream execution.
6. The exec wrapper renders the canonical outer exec shape and independently quotes every argv
   value. An optional template contains exactly one `{.}`, which expands to the complete frontend
   command. The plugin declaration does not contain or generate the outer carrier shape.
7. Invoking an executor-backed tool resolves its stable basename frontend through the
   authenticated snapshot wrapper to `hpatch-router`, verifies the pinned registry, dispatches
   by `argv[0]`, and delivers the declared argv under Codex's cwd, sandbox, and permissions.
8. JSON and SSE responses preserve call identity while replacing a contributed call with its
   validated carrier, and replay restores the exact original contributed call after verifying
   the retained carrier.
9. A model-input diagnostic is bounded and recoverable, while an invalid translator result
   cannot be returned or counted as a successful tool call.
10. Startup validation and tool-call metrics failures cannot replace an otherwise successful
    translated carrier or executor result; request cancellation still propagates.
11. An executor can return one validated current result with or without a validated stock result.
    The worker returns only the current result and does not run a second comparison execution.

## REQ-SHELL-001 — Installable free-form script tool

The first working path in `doc/brief.md` § Outcome supplies a repository
plugin declaration at `plugins/shell.mjs`. The declaration contributes an unconstrained custom
tool named `shell`, limits its UTF-8 input to the executor argv limit, and translates successful
input through the canonical exec carrier from `REQ-PLUGIN-001`. The repository `make install`
target installs `hpatch`, `hpatch-router`, and each repository plugin declaration into the
platform user configuration `hpatch/plugins` directory. The plugin remains configured rather
than built in.

The tool treats the first logical line as a shebang when that line, after trimming only its
leading and trailing ASCII spaces and tabs, starts with `#!`. It removes `#!`, trims the
remaining selector, and separates the selector at ASCII spaces or tabs. A bare executable name
is valid. A direct executable path remains unchanged. A leading `env` or `/usr/bin/env` and an
optional following `-S` are removed so the inherited `PATH` selects the next executable.
An empty selector, an `env` selector without an executable, a NUL byte, or too many or oversized
argv values rejects before execution. Without a shebang, the selected interpreter is `bash`.

When a shebang is present, the script body is every input byte after the complete first-line
terminator. The tool removes only the shebang line and its terminator. It preserves all leading
and trailing body whitespace, including an absent or final line terminator. Without a shebang,
the complete input is the body. The translated argv contains each normalized interpreter field
followed by the exact body as its final value. The resulting Codex exec carrier therefore shows
a command equivalent to `shell python3 'print("Hello")'`; the model does not author its quoting.

After an optional interpreter shebang, a leading directive block can contain one `#!cmd=` line
and one `!params JSON` line in either order. The tool trims ASCII spaces and tabs around each
complete directive line. The nonempty command value is a shell command template containing
exactly one `{.}` placeholder. The params value is a JSON object that cannot contain `cmd`
because the script body supplies `cmd`. A present `login` value must be exactly `false`.
A duplicate directive, malformed JSON, non-object JSON, unsupported leading `!` directive,
params object containing `cmd`, or unsafe `login` value rejects.

The tool removes recognized directive lines and their complete line terminators from the body.
The router replaces `{.}` with the canonical independently quoted `shell` frontend command and
argv. The command template then runs through the normal exec carrier shell. Without an
interpreter shebang, the nested frontend command selects `bash`. Without either directive,
current direct execution behavior remains unchanged. After the first body line, directive-like
lines remain ordinary body data.

The executor runs the first translated argv field as the selected interpreter and passes any
middle fields as interpreter arguments. It supplies the final exact body through an anonymous
script descriptor and invokes the interpreter with that descriptor's `/dev/fd` path. The
interpreter inherits the frontend standard input as program data. The executor stores no
intermediate script file. Without `!params`, the process inherits Codex's execution context.
With `!params`, Codex applies the accepted outer exec arguments before it launches the frontend.
The executor resolves bare interpreters through `PATH` and returns stdout, stderr, and exit
status without copying the script body into either output stream.

Acceptance:

1. A free-form call containing `#!/usr/bin/env python3` translates to an exec carrier whose
   visible command arguments are `shell`, `python3`, and the exact body; execution runs
   `python3` with that body as its anonymous script source.
2. `#!python3`, `#! python3`, and `#!/usr/bin/env python3` select `python3`. A directly supplied
   path such as `#!/opt/python/bin/python3` remains unchanged.
3. `#!/usr/bin/env -S python3 -u` runs `python3` with `-u` and the exact body as its anonymous
   script source.
4. `#!cmd=curl -fsSL URL | {.} | jq` without an interpreter shebang expands `{.}` to the
   independently quoted `shell bash` frontend command. The curl response becomes Bash standard
   input while the exact remaining body remains the script source.
5. When `#!python3` precedes that command directive, `{.}` expands to the independently quoted
   `shell python3` frontend command. The command-template input becomes Python standard input.
6. A missing, empty, or repeated `{.}` placeholder rejects before execution. A command directive
   in any later body line remains ordinary body text.
7. Input without a shebang or command directive selects `bash` and uses the complete input as the
   script source.
8. Python indentation and all other body-leading or body-trailing whitespace remain byte-exact
   after recognized directive removal.
9. The child inherits cwd, environment, and frontend standard input. Its stdout, stderr, and
   nonzero status are returned without script-source duplication or an intermediate script file.
10. Malformed selectors and input that cannot fit the bounded exec argv return a concise
    diagnostic without starting an interpreter.
11. A temporary installation root receives both Go binaries and `shell.mjs` from `make install`;
    the normal install target uses the Go binary destination and platform user configuration
    plugin directory.
12. `!params {"workdir":"/tmp","tty":true}` before or after `#!cmd=` produces an exec carrier
    containing those fields and the router-supplied `cmd`. An object containing `cmd` rejects,
    and a present `login` value must be `false`.
13. The authoritative Code Mode owner is exactly one custom `exec` tool. App-server requests place
    it directly in an `additional_tools` input item's tool list; CLI requests place it under that
    item's `functions` namespace. When `shell` is installed, the router removes the exact Markdown
    `exec_command` section and introductory `tools.exec_command` example from the owning
    description. It does not copy the removed contract into another model-visible description.
    Backtick and plain headings are accepted without depending on one Codex app or Codex CLI
    parameter schema. Without `shell`, the router preserves the native command contract.
14. Direct `additional_tools` entries named `functions.exec` and top-level tools named `exec` or
    `functions.exec` are unsupported and fail before forwarding. Defining more than one eligible
    owner also fails before forwarding. The existing `apply_patch` section extractor remains
    independent. Every sibling direct tool, sibling namespace, unrelated top-level tool, and other
    nested section remains byte-equivalent after the request rewrite.

## REQ-METRICS-001 — Persistent token, command, target, and failure metrics

Every recognized normal or translate invocation is classified after its terminal outcome.
A successful nonempty change set that parses, evaluates, translates, and completes its
requested output or mutation contributes paired estimates for two semantically equivalent
tool calls. A failed invocation contributes only its generated `hpatch` call estimate to
the ineffective-output counter; it contributes nothing to the effective `hpatch` counter.
A failed routed invocation is represented downstream by a Code Mode carrier that returns its
diagnostic and repair context. Its comparison baseline is the fixed direct-call program
carrying `*** Begin Patch\n*** End Patch\n`; that tokenized semantic baseline contributes
to the failed translated counter. The diagnostic carrier itself never counts as translated
hpatch output. The complete failed hpatch call remains in the ineffective-output counter and
reduces the overall output savings. `gain`, informational commands, and unsupported argument
forms do not contribute metrics.

Every routed contributed-tool call classified by `REQ-PLUGIN-001` contributes a row keyed by
plugin identity and tool name. Its emitted estimate counts the model-visible tool name followed
by the exact input the model emitted. Its translated estimate counts the validated Code Mode
carrier name followed by the router's canonical serialized carrier payload. Provider-generated
item IDs, call IDs, status, and JSON or SSE envelopes are excluded from both shapes. The router
derives both counts from the installed specification and validated carrier; plugins neither
supply token counts nor override their metric shape. A translated row's reduction is
`(translated - emitted) / translated * 100`, may be negative, and is `n/a` when translated
tokens are zero. Router-side input rejection uses a separate failed row with `n/a` reduction.
Executor failures after Codex accepts the carrier do not retroactively become router translation
failures.

For hpatch, both effective and ineffective emitted estimates count the `functions.hpatch` tool
name followed by the editing payload the model emitted. When a correction names commands of a
rejected script, the shorter correction is charged while the rebuilt complete script is used
only for evaluation. The successful translated side counts the Code Mode carrier name and a
fixed free-form program that passes the complete translated patch envelope, serialized as one
string argument, to `tools.apply_patch`, then returns that nested tool's result. The router-only
marker and hpatch final-state report are excluded from this established semantic baseline.
All estimates use the tokenizer library's GPT-5 model mapping. Tool inputs and translated
payloads remain data and cannot alter the fixed programs used for counting.

A final-state report successfully emitted by normal or translate mode contributes its exact
rendered text to a separate estimated state-report input-token counter. This is model-input
overhead because the tool result becomes subsequent model context; it is not added to either
model-output counter.

The host tool definitions are also model input. The router obtains the session identity, the
exact serialized collection of installed built-in and plugin tool objects, its stable per-plugin
and per-tool definition breakdown, the displaced native patch definition, and the displaced
request-specific `exec_command` fragments directly from the routed request. The first classified
request of a session counts these inputs once. Each removed fragment is tokenized independently,
without synthetic separator text. Subsequent requests in the same session add nothing because
the resent definition is served from the provider's prompt cache. The two removed-definition
counters remain separate. The installed-definition total is authoritative; per-tool rows and a
shared framing row reconcile it without being added again when computing net input. A host that
supplies no session or definition leaves these counters at zero, and gain states which inputs
were measured so a zero is not read as a free tool.

A failed or cancelled invocation emits no report and contributes zero report-input tokens. A partial
or failed report write does not count as a complete emitted report. For each completed contributed
tool execution, the current input estimate tokenizes the current stdout followed by current stderr.
The stock estimate tokenizes the optional stock stdout followed by stock stderr. The current result
is also the stock result when the executor omits the optional result. Exit status, provider-hidden
protocol and reasoning tokens, assistant commentary, and server-generated identifiers are excluded.

A contributed tool's input reduction is `(stock - current) / stock * 100`, may be negative, and is
`n/a` when stock tokens are zero. Its signed input overhead is `current - stock`. The sum of these
signed tool-result overheads contributes to net added input but does not add plugin rows to the
input-overhead source table. The router's end-to-end Responses and per-session usage totals remain
authoritative for provider-consumed model input. These token counts are reproducible estimates rather
than provider billing totals.

The router's in-memory metrics snapshot also attributes successful and rejected hpatch
translations and rejected-call diagnostic input tokens to the request session. Each session
retains the latest 32 evaluator rejection identities: command index, physical source line,
operation, target kind when known, stable reason, affected path when known, the physical
multiline value row when localized, and the generated line and column reported by Go syntax
validation when applicable. Each session also retains the latest 128 routed attempt identities:
chain/call identity, attempt and outcome, correction scope (`command` or `value-row`), value-row
operation count, affected base-body row count, affected base-command token count, emitted and
comparison token counts, evaluated command count, and its bounded rejection identities. These
count limits are reinforced by per-session text-byte limits, so an oversized rejection identity
is not retained. Session records use the same session identity as request lifecycle metrics and are not written
to `metrics.bin`. They retain neither scripts, replacement text, diagnostics, nor repair
context. Base-command text exists only long enough to count it and is not retained. Proxy
failures that occur before evaluator invocation do not fabricate evaluator rejection identities.
The snapshot also exposes aggregate counters so a benchmark can reconcile routed calls with
client-visible file-change items without inferring failures from stderr envelopes.

Classification is persisted only after the invocation's outcome is known. Translate mode
records a paired effective estimate after its complete patch reaches stdout; normal mode
records one after the staged changes commit. Each records report-input tokens only after
the complete final-state report is emitted. Stdin-read, parse, evaluation, translation,
stdout-write, and commit failures record only the canonical hpatch estimate as ineffective.
Successful no-op scripts contribute command counts and an emitted report estimate without
paired effective token estimates. In normal mode, failure to render the equivalent patch
after a successful commit emits a warning and records neither paired token classification,
but retains command and fully emitted report metrics.

Every supported command reached by evaluation contributes one invocation. A supported
operation rejected by syntax parsing contributes one invocation and one error when its
operation and attempted variant are structurally recognizable. An operation whose path
resolution or execution fails contributes one error after its invocation. Unknown or
future operations and failures outside command processing are not attributed to a
supported command. Successfully evaluated commands retain their invocation counts when a
later output or filesystem-commit boundary fails. Supported command counters are:

```text
in  new  mv  rm  type  type-  type+
```

Every structurally recognized explicit target attempt increments one target counter:

```text
line  range  text-single  text-multiple
```

Targetless `type VALUE` initialization has no target counter. A text target with omitted
count or count one is `text-single`; an explicit count intended to exceed one is
`text-multiple`, including an invalid multiple count. Unsupported HPATCH/1 commands and
unknown future commands are syntax failures but do not receive supported-command or
target attribution.

Terminal command errors carry stable internal reason identifiers grouped as:

```text
script-syntax
row-missing
row-stale
occurrence-missing
invalid-count
target-order
edit-conflict
active-file
initialization
file-path
language-syntax
other
```

The aggregate is stored in `hpatch/metrics.bin` beneath the platform user configuration
directory returned by Go's `os.UserConfigDir`. Updates hold an exclusive interprocess lock at
`hpatch/metrics.lock`; gain reads hold a shared lock. The current-version metrics format uses
two alternating bounded slots holding global hpatch counters and a keyed collection of
plugin-and-tool definition, call, emitted, translated, failed-translation, current-result, and
stock-result counters, plus a persistence generation and checksum. A reader uses the valid
greatest persistence generation, so an interrupted write to the inactive slot leaves the
preceding aggregate available. The file does not grow after its current-version slots are
created. Per-counter, per-tool, collection, and aggregate overflow fails without changing the
tool result.

Only the latest metrics magic is decoded. A complete, checksummed slot whose eight-byte magic
starts with `HPATCH` but does not equal the current version resets the reported totals to zero.
A malformed slot, including a mismatched version with an invalid checksum, does not qualify
for reset. When a current-format slot is also valid, its totals take precedence over
mismatched-version slots. Other invalid data fails rather than producing a misleading report.
Metrics writes use normal operating-system page-cache writeback and do not request a
per-invocation filesystem sync; sudden power loss may lose increments that the operating
system had not yet flushed.

`hpatch gain` first writes an output-token table with one stable row per plugin and tool,
placing a failed-translation row immediately after its successful row when present, followed
by an all-tools row. Its columns are emitted tokens, translated tokens, and reduction. The
hpatch failed row retains the fixed direct-call program carrying the empty patch as its
established semantic baseline and reports `n/a`; its downstream diagnostic carrier remains
excluded. The all-tools row sums the displayed emitted and translated quantities and applies
the same reduction formula when its translated denominator is nonzero.

Gain then writes an input-token table with one stable row per executed plugin and tool, followed by
an all-tools row. Its columns are current tokens, stock tokens, and reduction. Gain next writes the
input-token overhead table for final-state reports, failure diagnostics, the exact displaced
`apply_patch` definition credit, the displaced `exec_command` section credit, and the aggregate
installed tool-definition total. Indented stable plugin-and-tool rows and any shared
serialization-framing row reconcile the installed-definition total and are descriptive children
rather than additional input. Net added input is reports plus diagnostics plus installed
definitions minus both removed definitions plus the signed sum of current tool-result tokens minus
stock tool-result tokens. Gain does not subtract definitions from output, convert input to output,
or calculate a combined input/output percentage. Unmeasured definition sources are labeled
`not measured`.

The router gain page places the input-token and input-token overhead tables below the output-token
table in left and right columns. It uses the headings `Input token estimates` and
`Input token overhead estimates`.

Gain then writes stable-order compact tables for aggregate command invocation and error
rates; line, range, text-single, and text-multiple target counters; error reasons; and
each error attributed to the command that raised it. The last table lists only nonzero
command-and-reason pairs and renders one `none` row when no errors are recorded. Every
error appears in both the aggregate reason table and the attributed table, so the two
reconcile. Percentages are rounded to one decimal place and are zero when their denominator
is zero. With no metrics file or only an obsolete record, all totals and percentages are
zero. Gain reads no stdin and does not create or rewrite a metrics file. Failure to
tokenize, lock, read, write, or close metrics emits a concise `hpatch: warning:` diagnostic
but does not change the success or failure of the requested effect.

Acceptance:

1. Repeated successful normal and translate invocations persist cumulative paired
   hpatch estimates and fully emitted report-input estimates; failed invocations persist only
   ineffective hpatch estimates and zero report-input tokens.
2. Every successfully translated contributed-tool call persists a plugin-and-tool output row whose
   emitted count uses the exact model-visible call shape and whose translated count uses the
   validated canonical Code Mode carrier shape. Runtime executor failure does not reclassify it.
3. Every completed executor result persists current and stock input estimates for its plugin and
   tool. An omitted stock result produces equal estimates and zero reduction without a second
   execution. A zero-token stock result reports `n/a`.
4. Gain reports stable per-plugin and per-tool output rows, optional adjacent failed rows, and one
   all-tools output row. It reports a separate input table with current, stock, reduction, and one
   all-tools row.
5. The input-overhead table has no plugin child rows. Net added input includes the signed difference
   between current and stock tool-result estimates.
6. The seven supported hpatch command counters and four target counters reconcile with
   aggregate command attempts and errors. No selector, clipboard, editor-generation, or
   script-level commit counter remains.
7. Every definition-bearing request increments the definition-request counter, while the exact
   installed tool collection, its reconciling per-tool breakdown, and the displaced baseline
   definition accumulate only once per distinct session. An absent session or definition
   leaves definition counters zero and reports which inputs were measured.
8. Failed hpatch invocations contribute their complete output to the ineffective counter; the
   failed translated counter receives the fixed direct-call program carrying the empty patch
   envelope, while the downstream diagnostic carrier is excluded.
9. A correction is charged as the shorter payload the model emitted for both effective and
   ineffective invocations while evaluation uses the rebuilt complete script.
10. Tool inputs and translated payloads containing quotes or program-like text remain data and
   cannot alter the canonical programs used for counting.
11. Concurrent writers lose no records, concurrent gain reads never observe a partial
   aggregate, and an interrupted or damaged latest state falls back to the preceding valid
   aggregate.
12. A valid mismatched `HPATCH` version resets totals when no current state exists; malformed
    data does not count as a version mismatch, and current state takes precedence.
13. Metrics collection failure warns without changing the success or failure of the requested
    edit, translated carrier, executor result, or final-state report.
14. Router snapshots attribute successful and rejected hpatch translations, diagnostic token
    totals, at most the latest 128 correction-aware attempt identities, and at most the latest
    32 structured evaluator rejection identities to their request sessions without persisting
    scripts, replacement text, diagnostics, repair context, or new per-session records in
    `metrics.bin`; per-session text-byte limits may retain fewer identities.

## REQ-SCRIPT-001 — HPATCH/2 script grammar

HPATCH/2 replaces HPATCH/1. There are no compatibility aliases. Outside a heredoc body,
blank lines are ignored and every other physical line begins exactly one command:

```text
in PATH
new PATH
mv PATH
rm
type TARGET VALUE
type- TARGET VALUE
type+ TARGET VALUE
type VALUE
```

The final form is new-file initialization and is valid only under `REQ-FILE-001`.

Targets are:

```text
ROW                         complete logical line
ROW..ROW                    inclusive complete-line range
ROW "TEXT" [COUNT]          anchored exact literal occurrence(s)

ROW   := LINE:HASH
LINE  := positive one-based decimal logical line
HASH  := exactly four lowercase hexadecimal digits
COUNT := positive decimal integer; default 1
```

No whitespace is permitted inside `ROW..ROW`. A line target owns the complete logical
line, including its terminator when one exists. A range owns all
complete logical lines between its endpoints, inclusively.

A text target verifies its anchor row, starts at that row's column 1, and searches exact
literal content forward through EOF. `TEXT` is nonempty and cannot contain a logical-line
terminator. Matching is left-to-right and resumes after each complete match. The target
contains the first `COUNT` non-overlapping matches and rejects if fewer exist. Matches
may occur on different lines even though each match stays within one logical line.

`VALUE` is either a JSON-compatible quoted string or the fixed heredoc header `<<PATCH`.
Inline strings decode JSON escapes and Unicode escapes and additionally accept literal
horizontal tabs. Quotes, backslashes, line terminators, NUL, and other C0 controls remain
escaped. A heredoc consists of its command header, following literal UTF-8 body, and an
unindented closing line exactly equal to `PATCH`:

```text
type 12:a1b2..15:c3d4 <<PATCH
replacement
text
PATCH
```

No escape, interpolation, dedent, or delimiter substitution occurs. Payload bytes begin
after the header terminator and end before the closing delimiter. A nonempty final body
line therefore contributes its physical terminator. The header, body, and delimiter are
one command indexed at the header. An exact `PATCH` payload line must use inline escaped
text instead. Unterminated or oversized heredocs fail as one bounded header-owned syntax
error.

The grammar is unambiguous by operand shape. For example:

```text
type 12:a1b2 "line replacement"
type- 37:8c2f "// parseCommand parses one physical script line.\n"
type 12:a1b2 "needle" "replacement"
type 12:a1b2 "needle" 3 "replacement"
type+ 12:a1b2..15:c3d4 <<PATCH
inserted after the range
PATCH
```

Paths are nonempty and consume the remainder of their command line. Relative paths
resolve from cwd and are stored as root-relative identities. Absolute paths are accepted
only beneath the canonical root and are converted to root-relative identities. Root
lookup rejects lexical and symlink escape. Translation always emits root-relative paths.
Trailing operands, malformed rows, forbidden controls, missing values, and unknown
commands are invalid.

Acceptance:

1. Every accepted nonblank command is one of the seven public commands; `tsel`, `rsel`,
   `copy`, `cut`, `paste`, `del`, and script-level `commit` are syntax errors.
2. Line, range, and text targets parse without a separate selection command, and inline
   replacement values remain distinguishable from a text target's quoted literal.
3. JSON-compatible values and the fixed `<<PATCH` heredoc reproduce their exact decoded
   payloads without parsing body lines as commands.
4. Invalid rows, ranges, counts, strings, heredocs, operands, and commands fail before
   filesystem mutation, patch output, or final-state reporting.
5. File and mutation commands may be interleaved while all targets retain the immutable
   baseline meaning defined by `REQ-SELECT-001`.
6. With root `/workspace` and cwd `bin/worktree`, path `main.go` denotes
   `/workspace/bin/worktree/main.go` and translates as `bin/worktree/main.go`.

## REQ-CORRECT-001 — Compact rejected-script correction

After a routed hpatch script is rejected, a correction payload may transform the rejected
script with one operation per nonblank command header:

```text
N: COMMAND     replace command N
N: accept      apply hpatch's displayed safe correction for command N
-N             delete command N
+N: COMMAND    insert before command N
N+: COMMAND    insert after command N
N.R: "VALUE"   replace physical body row R of command N
-N.R            delete physical body row R of command N
+N.R: "VALUE"  insert before physical body row R of command N
N.R+: "VALUE"  insert after physical body row R of command N
```

A replacement or insertion whose command ends in `<<PATCH` consumes its heredoc body and
closing delimiter as part of that one correction operation. All indices refer to the
original rejected script before any correction operation is applied. Replacements,
acceptances, and deletions may name an index at most once and conflict with each other for
the same index. An acceptance is valid only when the immediately repairable rejected
script retained an exact correction for that command; it never approves the rejected
mutation itself. Multiple insertions at one anchor are allowed and retain payload order;
their position is relative to the original anchor even when that anchor is deleted. Every
nonblank line outside a correction heredoc must be a correction operation.

Body-row addressing is available only for the physical rows between the opener and closing
delimiter of a complete fixed `<<PATCH` value. It does not address decoded inline-string
lines. `VALUE` is one JSON-compatible quoted physical row, optionally including its own
terminator. Replacement preserves the addressed row's terminator when `VALUE` omits one;
an explicit terminator is authoritative. Insertions are byte-exact and synthesize no
terminator. Deletion removes the addressed row and its terminator. A replacement or
insertion cannot materialize an exact `PATCH` delimiter row; replace the complete command
with an inline-escaped value instead. Body-row `accept` is not supported. A complete-command
replacement, acceptance, or deletion conflicts with any body-row mutation of the same
command; multiple insertions at one body-row anchor retain payload order. Diagnostic row
numbers use the same LF/CRLF physical framing as correction indices, so an embedded standalone
carriage return remains within one escaped display row.

The router validates all operations, retained acceptances, and referenced indices before
rebuilding the script. It then reparses and reevaluates the complete transformed script
against the unchanged workspace. A correction failure changes nothing. A successful
transformation becomes the base for a later correction, whose command and body-row indices
resolve against that latest evaluated script. The chain retains the correction-chain
correlation ID, increments the attempt for every evaluated or proxy-rejected correction, and
charges metrics for only the compact payload the agent emitted. A proxy-rejected correction
leaves the last evaluated script as the repair base. The core evaluator has no correction mode.

Acceptance:

1. `N: COMMAND` remains compatible with existing replacement corrections.
2. `N: accept` substitutes exactly the safe correction displayed for command N; an absent
   or stale suggestion rejects without evaluating or mutating the workspace.
3. `-N`, `+N: COMMAND`, and `N+: COMMAND` can remove obsolete commands and insert new
   commands without resending the complete script.
4. Multiple same-anchor insertions preserve payload order, including when the anchor is
   deleted, while duplicate replacement/acceptance/deletion or an absent index rejects
   the complete correction.
5. A correction heredoc is one operation; an invalid or unterminated correction heredoc
   produces one bounded diagnostic and does not reinterpret its body as operations.
6. Every corrected script is revalidated atomically against the unchanged workspace and
   retains the established correlation and emitted-payload metrics behavior.
7. `N.R` operations address only a complete fixed-heredoc body, obey physical terminator
   ownership, reject absent rows and mixed whole-command/body-row mutation, and reindex
   against the latest evaluated rejected script in a chained correction. They cannot create
   an exact fixed delimiter row.

## REQ-FILE-001 — File scope and lifecycle

An invocation has one immutable baseline for each touched existing file. The first
`in PATH` loads the regular UTF-8 contents visible at invocation start and makes that
logical file active. Returning to the same logical file reuses that baseline and retains
its pending edits. There are no generations and no command can materialize pending
content as a new target baseline inside the script.

`new PATH` creates and activates a pending empty file. It fails if the logical path
exists in the invocation workspace or pending state. Its immediately following nonblank
command may be one targetless `type VALUE` initializer; any intervening command closes
that initialization opportunity. The initializer is consumed even when its value is
empty. No target-bearing mutation is valid on a new file because hread could not have
produced a baseline reference for it. Further or dependent content changes require a
successful invocation followed by a fresh read.

`mv PATH` moves the active logical file to an unoccupied pending path. The destination
becomes active; its original baseline and pending edits move with it. Later `in` resolves
the new path, not the old one. Repeated moves collapse to one original-to-final move.

`rm` marks the active existing file deleted and clears the active file. Removing an
existing file after any content mutation in the same invocation is an edit conflict;
pending content is never silently discarded. Removing a moved, otherwise unedited file
deletes its invocation-original path. Removing a file created in the same invocation
cancels that creation, including an empty initializer.

`in` fails for missing or deleted paths. `mv` and `rm` fail without an active file.
`new` and `mv` fail on destination collision. Parents of `new` and `mv` destinations must
already exist. Hpatch does not create directories. All file and content changes remain
in memory until the complete invocation crosses the normal or translate boundary.

Acceptance:

1. A script can edit multiple files and return to an earlier path without shifting that
   file's targets or losing pending disjoint edits.
2. A new file accepts at most one immediately following targetless initializer and does
   not expose introduced content as a same-script target.
3. Moves preserve baseline identity and pending edits; repeated moves collapse to one net
   action.
4. Removal after an existing-file content mutation, path collision, use after deletion,
   unsupported target-bearing new-file edits, and lifecycle commands without an active
   file reject before external mutation or patch output.
5. Failure or cancellation after any number of commands exposes no intermediate change.

## REQ-SELECT-001 — Verified immutable-baseline targets

Every explicit target resolves against the active existing file's immutable invocation
baseline. A row resolves by locating its one-based logical line and comparing its four
digit hash with the hash of the exact current baseline line content. An absent or
out-of-bounds line is `row-missing`; a present line with a different hash is `row-stale`.
Hpatch never scans for another line with the supplied hash and never chooses nearby or
duplicate content. Line number disambiguates equal lines; the 16-bit hash retains an
accepted approximately 1-in-65,536 random false-acceptance residual.

Both endpoints of a range must verify independently and remain ordered. A text target
then searches the verified baseline suffix exactly as defined by `REQ-SCRIPT-001`.
Pending edits never alter row verification, literal search, matches, or positions.
Content introduced by any command is not targetable in that script. Dependent edits
require successful application, hread inspection of the new content, and a later
invocation with fresh references.

Resolution produces one nonempty baseline span for a line or range and one or more
nonempty spans for a text target. A mutation over multiple spans validates and registers
all of them or none. There is no persistent selection, cursor, clipboard, shadow buffer,
generation, or resume state.

Acceptance:

1. A copied hread row verifies only the same line with the same complete content,
   including indentation; duplicate content at other line numbers is irrelevant.
2. Missing and stale rows are distinct failures and neither searches for a substitute.
3. Inclusive ranges verify both endpoints and reject reversed order.
4. Text targets select the requested first N non-overlapping matches from the verified
   anchor through EOF and reject incomplete multiplicity.
5. Independent targets retain their original meaning after pending edits; introduced
   content cannot be addressed without a later hread. A whole-file move preserves the
   moved file's existing baseline under its new logical path.

## REQ-EDIT-001 — Target-bearing mutations

`type TARGET VALUE` replaces every target span with the decoded value. An empty target-bearing
value deletes every target span, including a terminator owned by a complete-line or range
target. `type- TARGET VALUE` inserts the value immediately before every span and preserves
the target. `type+ TARGET VALUE` inserts immediately after every span and preserves the
target. A command with multiple text matches is atomic: resolution or conflict at any match
records none of its mutations.

Replacements and deletions must have disjoint baseline interiors. An insertion strictly
inside a replacement or deletion conflicts. Insertions exactly at either boundary are
permitted. Multiple insertions at the same baseline boundary are permitted and render in
script command order. Conflicts identify the prior command and affected baseline range;
they reject the complete script before filesystem mutation or patch output.

For a complete-line or range replacement whose target owns a final LF, CRLF, or
standalone-CR terminator, nonempty `type` preserves that exact final terminator when the
replacement does not end in a terminator. A replacement-supplied final terminator is
authoritative and is not doubled. No terminator is synthesized for an unterminated selected
final line. An empty target-bearing `type` value removes owned terminators. Inserted values
are otherwise byte-exact decoded UTF-8. Existing line endings outside explicit inserted or
replaced text remain unchanged.

The engine orders registered immutable-baseline edits once and renders one final content
value per file. It never reads pending mutated content while resolving a later target.
Content movement requires emitting the destination content; `mv` moves whole files only.

Acceptance:

1. Replacement, before insertion, after insertion, and deletion produce the specified
   result directly from their targets without a selection command.
2. Multi-match text mutation applies the same action to every requested match or none.
3. Disjoint edits are script-order independent except for deliberate insertions at the
   same boundary, which retain script order.
4. Overlapping destructive spans and insertions strictly inside them reject atomically;
   boundary insertions remain valid.
5. LF, CRLF, and standalone-CR complete-line replacement preserve the owned terminator
   for a nonempty value unless the value supplies one; an unterminated final line stays
   unterminated, while an empty value deletes any owned terminator.

## REQ-OUTPUT-001 — Output, final state, and failure behavior

Input is read completely and the entire script is evaluated before an external filesystem
commit or stdout. Before finalization, every changed file whose final path ends in `.go`
is parsed and formatted with Go's standard-library `go/format`; a parse failure rejects
the complete transaction. For at most 32 content-mutating commands in one invalid Go file,
the evaluator replays command-group subsets against the immutable baseline to select a
one-minimal syntax-failing set, then attributes the failure to the retained edit nearest the
generated parser position. Larger groups or an invalid baseline use nearest-edit attribution
without subset replay. The rejection includes at most two generated lines before and after
the failing line; neighboring lines are capped at 64 runes and the failing line at 200.
Non-Go files receive no language validation. An unchanged normal-mode change set performs no
filesystem operation but still reports final state.
An unchanged translate result emits no patch and fails because it cannot represent an
update; it emits no final-state report.

Translate output contains file actions in deterministic first-touch order:

```text
*** Begin Patch
*** Update File: PATH
*** Move to: NEW_PATH
<unified diff hunks>
*** Delete File: PATH
*** Add File: PATH
+<content>
*** End Patch
```

Each action includes only syntax relevant to that file: additions use `Add File`,
deletions use `Delete File`, moves use `Update File` plus `Move to`, and content edits
use `Update File` hunks. A moved and edited file combines its content hunks and move in
one update action, with `Move to` immediately after `Update File`. Because OpenAI
`apply_patch` rejects an empty update action, a move with unchanged contents includes
a minimal verification hunk: one unchanged context line for a nonempty file, or an
equal remove/add of the empty line representation for an empty file. Translation is
fully rendered before stdout is written.

After every command and the requested normal filesystem commit or translated patch write
succeed, the CLI writes one final-state report to stderr. Its line forms are:

```text
in PATH
last OP PATH COUNT ranges RANGE[, RANGE[, RANGE]] [ +N more]
files add=A update=U move=M delete=D
LINE:HASH TEXT
```

The first line is `no active file` when `rm` leaves none. Otherwise it names the active
final path. The `last` line is `last none` when no mutation changed final content;
otherwise it names the last effective mutation operation, that file's surviving final
path, the number of affected target spans, and at most three verified immutable-baseline
ranges. Extra ranges are summarized by `+N more`. `RANGE` is a half-open
`START_LINE:START_COLUMN-END_LINE:END_COLUMN` pair in one-based Unicode coordinates; a
complete-line range includes its final terminator when present. The `files` line counts
net original-to-final actions.

Up to three rows from the active final file follow in `REQ-READ-001` format. When it owns
the last mutation, the window is centered on the first reported range's starting line
number, clamped into the rendered final content; otherwise it begins at line 1. `TEXT`
contains at most the first 64 Unicode code points of rendered line content, without a line
terminator or added ellipsis. Tabs are preserved and other controls are escaped so each
preview stays on one line. An empty active file reports row `1` with the hash of empty
content. The report describes only the completed invocation; no target or editing state
persists into a later invocation. Active paths reflect pending moves.

The complete report is rendered before commit or patch output, but it is emitted only
after that mode-specific effect succeeds. A report-write failure after the effect is
best-effort and cannot retroactively change the successful effect or claim rollback.

Normal mode stages new contents in same-directory temporary files before starting the
commit. Parse, validation, read, and evaluation failures leave the initial tree unchanged.
A staging failure attempts to remove all temporary artifacts; cleanup failure returns
nonzero and identifies every artifact it could not remove. Commit-time filesystem failures
trigger rollback attempts using staged backups. Ordinary filesystems cannot provide a
portable crash-atomic transaction over multiple paths: termination, machine failure, or
rollback failure during commit can leave a partial change set. Such a failure must return
nonzero and name the affected paths; it must never report success or claim rollback
succeeded when it did not. Existing file permission bits are preserved; files created by
`new` use mode `0644`.

OpenAI `apply_patch` is a logical-line format and cannot preserve CRLF or standalone-CR
bytes when its output is applied by the tool. Translate mode therefore emits LF-only
patch text and normalizes line endings only in its displayed before/after lines. It does
not modify source files. Normal mode continues to preserve existing line endings outside
explicitly inserted strings. Applying translated output to a non-LF file may normalize
that file to LF; this is a declared format limitation, not byte equivalence.

Failures emit concise diagnostics to stderr, prefixed with `hpatch:`. Independently
parseable syntax failures may be reported together before evaluation. Script diagnostics
identify the one-based nonblank command index, one-based source line, operation, relevant
operand or active path when one exists, and the stable reason defined by
`REQ-METRICS-001`. A heredoc failure is owned by its header and may additionally report
its physical source-line span. Control bytes in every diagnostic are escaped and embedded
newlines are folded so one failure remains one logical line. Failures return nonzero and
emit no stdout or final-state report. Malformed row syntax receives a syntax diagnostic.

A stale row reports the actual `LINE:HASH TEXT` at that line and up to two neighboring
baseline rows. A missing literal occurrence reports the verified anchor context. An edit
conflict identifies the prior command and affected immutable-baseline lines. If a command
depends on content introduced by another command, the diagnostic directs the agent to
apply the prerequisite independently, reread, and submit a later invocation. A missing
row or failure without a verified baseline does not choose repair context. Repair context
is supplementary: it never changes exit status, stdout, mutation, or metrics classification.
When invalid generated Go is localized to a fixed-heredoc mutation, its rejection identity
includes the non-sensitive `value_line`, and the transient diagnostic displays that
`COMMAND.ROW` plus at most two neighboring physical body rows on each side. Inline decoded
multiline values and failures outside a multiline replacement do not fabricate a value row.

Acceptance:

1. Normal success has empty stdout and one rendered final-state report on stderr after
   commit; translate success has patch-only stdout and one pending-state report on stderr
   after the patch is completely written.
2. Active paths, bounded last-mutation ranges, bounded `LINE:HASH` previews, net file
   counts, Unicode columns, truncation, and control escaping produce the specified report
   without implying cross-invocation persistence.
3. Changed Go files are formatted with the standard library before output, and invalid Go
   rejects the transaction without mutation; non-Go files receive no language validation.
4. Malformed input, missing, stale, reversed, or incomplete targets, edit conflicts,
   unknown or future commands, invalid UTF-8, missing or non-regular files, path collisions,
   staging failure, translation failure, and cancellation produce no mutation, patch
   output, or final-state report.
5. Injected external filesystem commit and rollback failures are reported without false
   atomicity claims and without a successful final-state report.
6. Failure to write a fully rendered report after a successful external effect does not
   reverse that effect or record a complete report-input token estimate.
7. Stale rows, incomplete literal targets, and edit conflicts emit verified repair context;
   a missing row fails without guessing, and a failure with no active baseline emits its
   diagnostic alone.
8. Invalid Go localized inside a fixed `<<PATCH` value reports its physical body row in
   bounded repair context and structured host rejection identity without retaining body text.

## REQ-GUIDE-001 — Agent guidance

Top-level help owns the complete CLI, editing, validation, trust-boundary, report, and
metrics reference. Tool help is a separately maintained concise model-facing summary. It
excludes CLI modes, options, metrics, version material, and the full correction DSL.
The router supplies indexed correction syntax once in the first correctable rejection
diagnostic of a correction chain; later rejection diagnostics in that chain do not repeat it.

Both references teach this workflow:

1. Use hgrep to locate matching complete lines and copy its current `LINE:HASH` directly
   when that exact row is sufficient for the edit. Use hread for surrounding or nonmatching
   context rather than repeating an exact hgrep result. Put up to 6 already-known files or
   ranges into one newline-delimited hread call, and copy references only from current output
   for that exact path.
2. Choose a line, inclusive range, or anchored literal target inside the mutation command.
3. Batch all ready short supporting edits that share a failure domain into one call with
   repeated `in PATH` sections instead of issuing one call per file. Keep unrelated large
   `<<PATCH` values in separate failure-domain calls, with at most one syntax-sensitive
   multiline Go declaration or function replacement per call; short supporting edits for
   that same change may remain with it.
4. Prefer the smallest mutation that expresses the semantic change. When a formatter owns
   formatting, alignment, or indentation, do not replace surrounding lines merely to reproduce
   its output; let the formatter apply those changes. For example, add one struct field with one
   insertion rather than replacing the declaration. Preserve required indentation prefixes in
   indentation-sensitive languages such as Python. Before a later invocation targets a file
   changed by a successful call, discard its saved references and hread only the required region
   again; do not reread a file that needs no further edit.
5. Use nonempty `type` to replace, empty target-bearing `type` to delete, `type-` to insert
   before, and `type+` to insert after; do not construct a separate selection or clipboard
   program.
6. Encode short single-line values inline. Include `\n` when a before/after insertion
   must form a complete new line; reserve `<<PATCH` for multiline or escape-heavy values.
7. After rejection, prefer a compact indexed command or multiline-value-row correction when
   the desired targets still belong to the same baseline; reread stale rows instead of guessing.
8. Do not run redundant `gofmt`; hpatch formats changed Go files before success.

Guidance includes minimal examples for line replacement, range deletion, inline
single-line before/after insertion, multiline insertion, text multiplicity, new-file
initialization, stale-row recovery, and a dependent edit split across two inspected
invocations. It states that HPATCH/1 commands are invalid rather than documenting aliases.
Host base-prompt overrides may steer tool choice without duplicating the language;
complete semantics remain in top-level help and this contract.

Acceptance:

1. A model can choose and encode every HPATCH/2 operation from tool help without learning
   HPATCH/1 state concepts.
2. Examples are parseable under `REQ-SCRIPT-001` and demonstrate dependency layering
   rather than same-script editing of introduced content.
3. Persistent guidance stays compact; correction grammar appears only with an actionable
   rejected-script context.
