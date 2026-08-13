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

## REQ-READ-001 — Shell-routed verified-row reader

In hpatch router mode, the model receives `hpatch` and `shell` as standalone custom tools.
All persistent hread, hgrep, inspect_file, shell-execution, and HPATCH workflow guidance comes
from the Codex `model_instructions_file` installed from
`contrib/codex/file-editing-instructions.md`. The router never creates, changes, or removes the
top-level Responses `instructions` value.
Hread, hgrep, and inspect_file remain private executable contributions; their custom-tool
specifications are not sent to the model and direct model calls to their names are not routed.

The private `hread` command accepts exactly one file:

```text
hread PATH [START:END]
```

The shell owns quoting and argument separation. A path containing whitespace is therefore one
ordinary quoted shell argument. `START:END`, when present, is an inclusive logical-line range
whose positive one-based base-ten endpoints must be ordered. The start line must exist. An end
past EOF returns through the final line. One `hread` process never accepts a second path or a
newline-delimited batch. The model batches related reads as separate hread commands in one
shell script.

The router creates process-scoped executable frontends named `shell`, `hread`, `hgrep`, and `inspect_file`.
The Codex executor must see the frontend directory and router executable at the same absolute
paths as the router process, and that directory must precede unrelated commands on its trusted
`PATH`. A deployment that isolates their filesystems must provide those runtime mounts
separately from the user workspace.

Hread runs in the shell carrier's actual working directory. Relative and absolute paths keep
their ordinary process meaning. Codex, not the router or hread, owns sandbox and filesystem
permissions. The worker accepts only regular UTF-8 files and never mutates them. It emits only
the requested logical lines:

```text
LINE:HASH TEXT
```

`LINE` is the positive one-based logical line number. `TEXT` is exact logical-line content
without its terminator. `HASH` is lowercase hexadecimal for the first two bytes of SHA-256
over that exact content, including leading spaces and tabs. A trailing file terminator does
not create an additional empty line. Missing, inaccessible, non-regular, non-UTF-8,
reversed-range, and start-past-EOF reads return concise stderr and nonzero status.

For input metrics, hread produces its current and stock results from the same read. The stock
result preserves selected `TEXT` and one LF per returned logical line while omitting the
`LINE:HASH ` prefix. The comparison does not read a file twice.

Acceptance:

1. A whole-file or bounded read emits exact UTF-8 rows. Equal lines at different positions
   have distinct row references, and indentation changes the hash.
2. `hread PATH`, `hread PATH START:END`, and a shell-quoted path containing whitespace work.
   Extra path or range arguments fail instead of being interpreted as a batch.
3. Several hread commands in one shell call execute in authored shell order without an
   hread-owned batch format, buffer, header, or partial-success policy.
4. Reading and whole-file UTF-8 validation use bounded streaming storage and observe
   cancellation. A formatted result rejects before exceeding 16 MiB.
5. Success and failure reach Codex through the model-visible shell carrier. Replay retains
   the original shell call and output; it never synthesizes a model-visible hread call or
   includes the shell call in editable rejected-script recovery history.
6. Router startup fails before serving if the private hread frontend cannot be installed.
   Passthrough mode installs and exposes none of these replacement surfaces.

## REQ-GREP-001 — Shell-routed verified-row search

The private `hgrep` command is available through the model-visible shell tool. The shell owns
quoting, redirection, pipelines, and command composition; hgrep receives the resulting ordinary
argv. It accepts familiar ripgrep pattern, matching, file, glob, type, ignore, context, and
resource-selection arguments. With no explicit path, search defaults to the shell process's
current directory.

The worker invokes installed `rg` with internal `--json --no-config` arguments. It rejects
model-supplied output, multiline, preprocessor, compressed-input, informational, and other
modes that cannot identify one complete editable source row. Ripgrep retains ownership of
regex parsing, ignore rules, traversal, matching, and search diagnostics; hgrep provides no
fallback search engine.

The worker consumes ripgrep's structured match and context events and emits each first-seen
logical row once in ripgrep result order:

```text
"PATH":LINE:HASH TEXT
```

`PATH` is JSON-quoted. `LINE:HASH TEXT` has exactly the identity and complete UTF-8 semantics
of `REQ-READ-001`. Match highlighting, match-only fragments, replacement output, trimming,
and line truncation cannot change it. Multiple matches on one path and line produce one row.
Ripgrep's no-match exit status is a successful empty result. Execution, filesystem, encoding,
cancellation, invalid-pattern, and missing-executable failures return concise nonzero
diagnostics. Output contains only complete rows and stays within 16 MiB; reaching the bound
preserves completed rows and adds a limit diagnostic.

For input metrics, hgrep produces its current and stock results from the same ripgrep event
stream. The stock result preserves each JSON-quoted `PATH`, `TEXT`, LF, result order, and
diagnostic while omitting the `LINE:HASH ` portion. The comparison does not run ripgrep twice.

Acceptance:

1. A regular-expression search with an explicit path and glob emits JSON-quoted paths,
   positive line numbers, four-digit hashes, and exact complete matching lines that can be
   copied directly into an hpatch target.
2. Shell quoting determines literal arguments, and ordinary redirection or pipelines operate
   as shell syntax rather than becoming hgrep argv. A conflicting hgrep output or transformed
   input mode still rejects before ripgrep starts.
3. Requested before/after context emits complete verified rows beside matches. Repeated match
   or context events on one row emit that row once; no matches return successful empty stdout.
4. The model-visible shell call and output are replayed unchanged. No standalone hgrep call is
   exposed, routed, or admitted to hpatch recovery history.
5. Router startup fails before serving if the private hgrep frontend cannot be installed.
   Passthrough mode installs and exposes none of these replacement surfaces.

## REQ-INSPECT-001 — Shell-routed structural file inspection

The private `inspect_file PATH` command is available only through the model-visible shell tool.
It accepts exactly one shell-separated workspace-relative path and no options. The canonical
workspace is `process.cwd()`. Absolute paths, lexical escapes, `@shell` paths, and symlinks whose
canonical targets escape that workspace fail. In-workspace symlinks are allowed, and the final
target must be a regular file.

Extension matching is exact and case-sensitive. `.go`, `.js`, `.ts`, `.py`, `.md`, and `.json`
use pinned parsers; TypeScript selects the JavaScript parser's `ts` dialect, while Markdown uses
Lezer for headings and YAML for a closed initial frontmatter block. Every other extension returns
`kind: "none"`, the reported regular-file byte size, `line_count: null`, `parse_complete: true`,
and an empty outline without reading or decoding content. Supported files must be strict UTF-8.
Their logical line count matches `REQ-READ-001`, including CRLF, lone CR, empty files, and final
terminators.

Success is one LF-terminated JSON document with `ok`, `data`, `truncated`, and `truncation`.
`data` contains the normalized requested path, kind, language, exact inspected byte size, logical
line count, parser-completeness flag, and a flat source-ordered outline. Code entries include only
imports, top-level constants and variables, types, classes, functions, and direct methods.
Markdown includes only ATX headings outside fences and top-level scalar keys parsed from a closed
initial `---` YAML frontmatter block. JSON includes every recognized value as a depth-first RFC
6901 pointer and value type, including the empty root pointer. No result contains raw excerpts,
bodies, fields, comments, frontmatter values, or JSON scalar values.

The complete successful stdout, including its final LF, is at most 65,536 UTF-8 bytes. When
necessary, the worker retains the longest complete outline prefix and returns
`truncation: {"reason":"output_bytes","after_entries":N}`. Lezer parser recovery or YAML
frontmatter diagnostics set `parse_complete: false` independently of output truncation. There is
no input-size or entry-count limit. If an empty-outline success envelope cannot fit, the command
fails with `output_limit`.

Command failures write one closed LF-terminated JSON envelope to stdout, leave stderr empty, and
exit nonzero. Stable codes are `usage`, `not_found`, `not_regular`, `not_utf8`,
`outside_workspace`, `read`, `parse`, and `output_limit`. The centralized Codex guidance and
private call contract embed a concise success, failure, and outline-entry shape rather than the
normative specification schema. Shell replay keeps the original call and output; inspect_file is
not model-visible, directly routed, or included in hpatch recovery ancestry. Passthrough mode
installs and advertises none of these surfaces.

Acceptance:

1. Each supported language projection returns only its declared navigation identifiers and exact
   inclusive one-based ranges, while malformed recoverable input remains a successful partial
   result with `parse_complete: false`.
2. Markdown excludes fences, Setext headings, nested YAML frontmatter keys, and all frontmatter
   values while preserving source order for repeated top-level scalar keys; JSON escapes `~` and
   `/`, preserves duplicate pointers, and never returns scalar values.
3. Unsupported files are confined and checked as regular without content reads, UTF-8 validation,
   line counting, content detection, or command-level truncation.
4. Router startup installs an authenticated `inspect_file` frontend and exposes or routes only
   hpatch, shell, and configured model-visible contributions. Request instructions remain unchanged.

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

An exec translator may also return one nonempty stock command for output metrics. The router
applies the same optional command template and JSON parameters, then renders the stock command
through the canonical exec wrapper. This stock carrier is metric evidence only: the response,
history, replay, and execution paths retain the validated frontend carrier. Without a stock
command, output metrics use that frontend carrier as before.

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
hpatch recovery ancestry. Runtime model-input rejection returns a bounded diagnostic
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

## REQ-DIAGNOSE-001 — Agent issue reports

When hpatch mode starts with the inherited environment variable `HPATCH_DIAGNOSE` exactly
equal to `1`, the immutable built-in registry contributes a model-visible unconstrained custom
tool named `report_issue`. Any other value, including an unset variable, omits that contribution.
Passthrough mode remains unchanged because it does not construct the registry.

The tool accepts agent-authored Markdown for problems encountered while using hpatch and its
related tools. The router snapshots `hooks.diagnose` from the existing `settings.json` hook
configuration at startup and invokes those commands directly when the model calls `report_issue`.
It does not install an executable wrapper or frontend and does not route the report through the
executor plugin worker.

Each command is rendered against an event whose `Body` is the exact Markdown and whose `Title`
is the current Codex task title, using the same session-title lookup as other routed hooks; when
no title is available, `Title` is `hpatch diagnostic`. `format_markdown` returns that same body,
and `shellquote` retains its existing behavior. All configured diagnose commands share the
existing 10-second error-hook timeout. A missing or empty diagnose list is a successful no-op.
Successful dispatch returns `Issue reported.` through the existing Code Mode result carrier;
settings or registry initialization failures prevent startup. Rendering, execution,
cancellation, or timeout failures return an `hpatch: warning:` in the tool result and do not
fail or interrupt response routing.

Acceptance:

1. Exactly `HPATCH_DIAGNOSE=1` in hpatch mode exposes the free-form `report_issue` specification;
   all other values and passthrough mode expose none of it.
2. One report reaches each configured `hooks.diagnose` command byte-for-byte through `.Body` and
   `format_markdown`, exposes the task title through `.Title`, and does not run `hooks.error`.
3. No configured diagnose hook succeeds without side effects, while hook failures remain
   observable as tool-result warnings without failing the translated response.
4. `report_issue` has no executable wrapper, stable frontend, or plugin-worker implementation.

## REQ-SHELL-001 — Installable free-form script tool

The first working path in `doc/brief.md` § Outcome supplies the built-in declaration at
`plugins/shell.mjs`. The generated plugin bundle contributes an unconstrained custom tool named
`shell`, limits its UTF-8 input to the executor argv limit, and translates successful input
through the canonical exec carrier from `REQ-PLUGIN-001`. The repository `make install` target
regenerates that bundle and installs `hpatch`, `hpatch-router`, and the centralized Codex model
instructions. It does not copy a configured shell declaration.

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

After an optional interpreter shebang, a leading directive block can contain one `#!cmd=`
assignment and one `#!params=` assignment in either order. All canonical directives use
`#!key=value`. The tool trims ASCII spaces and tabs around each complete directive line. The
nonempty command value is a shell command template containing exactly one `{.}` placeholder.
The params value is a JSON object that cannot contain `cmd` because the script body supplies
`cmd`. A present `login` value must be exactly `false`. Within the leading directive block,
the tool safely normalizes `# !params JSON`, `#!params JSON`, and legacy `!params JSON` through
the same params validation. A duplicate directive, malformed JSON, non-object JSON, unsupported
leading directive, params object containing `cmd`, or unsafe `login` value rejects.

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
intermediate script file. Without `#!params=`, the process inherits Codex's execution context.
With `#!params=`, Codex applies the accepted outer exec arguments before it launches the frontend.
The executor resolves bare interpreters through `PATH` and returns stdout, stderr, and exit
status without copying the script body into either output stream.

The shell carrier forwards the complete native `exec_command` result defined by the owning Code
Mode contract rather than only its output field. A result containing the native continuation
handle remains yielded rather than terminal, and the same host-owned continuation operation
resumes that session. The router and shell plugin do not poll, resume, cancel, retry, replace, or
persist the session. They do not define a second result envelope or continuation protocol. Exact
result fields, yield timing, continuation arguments, and session lifetime remain owned by Codex's
executable tool definitions in that request.

For output metrics, the shell translator supplies a stock exec command for the normalized
interpreter and exact body. Python-family executables pass the shell-quoted body as the `-c`
argument, and Bun and Node-family executables pass it as the `-e` argument. Other interpreters
receive `/dev/fd/3`; a quoted heredoc supplies that descriptor while leaving program stdin
available. Its interpreter-derived delimiter changes when the body contains that delimiter as a
complete line. The router applies any command template and parameters and counts the complete
canonical Code Mode exec shape. It still executes and replays only the authenticated `shell`
frontend carrier.

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
11. `make install` installs both Go binaries, no configured shell declaration, and complete
    Codex model instructions. If the top-level config key is absent, it renders the selected
    bundled model instructions, installs the default file, and adds the key. If the key exists,
    it remains byte-equivalent and the referenced customized file is updated only when its
    owned section is stock, legacy hpatch, or marked hpatch guidance; content outside that
    section is preserved. Every `model_instructions_file` declared by a personal agent TOML
    under the adjacent `agents` directory is updated under the same preservation rules;
    relative values resolve from the declaring agent TOML and the TOML files remain unchanged.
    The installed router embeds shell and creates shell, hread, hgrep, and inspect_file basename
    frontends beside its executable at startup.
12. `#!params={"workdir":"/tmp","tty":true}` before or after `#!cmd=` produces an exec carrier
    containing those fields and the router-supplied `cmd`. Safe leading params near-misses
    produce the same carrier after normalization. An object containing `cmd` rejects, and a
    present `login` value must be `false`.
13. The authoritative Code Mode owner is exactly one custom `exec` tool. App-server requests place
    it directly in an `additional_tools` input item's tool list; CLI requests place it under that
    item's `functions` namespace. The router removes the exact Markdown `exec_command` section and
    introductory `tools.exec_command` example from the owning description. It derives the
    request-specific argument-object shape from the app declaration or parameter-list shape from
    the CLI description, removes `cmd`, and appends only that sanitized shape under `#!params` in
    the built-in `shell` description. Neither model-visible description contains
    `tools.exec_command`. An eligible owner without a recognizable parameter shape retains the base
    `shell` description and does not reject.
14. Direct `additional_tools` entries named `functions.exec` and top-level tools named `exec` or
    `functions.exec` are unsupported and fail before forwarding. Defining more than one eligible
    owner also fails before forwarding. The existing `apply_patch` section extractor remains
    independent. Every sibling direct tool, sibling namespace, unrelated top-level tool, and other
    nested section remains byte-equivalent after the request rewrite.
15. A terminal shell carrier forwards the complete native exec result. When native execution
    yields, the carrier forwards that same complete result, including its continuation handle,
    without calling the continuation operation or starting the frontend again. No router session
    record or plugin-defined continuation surface is created.
16. For one built-in shell input, the router emits one warning for every distinct detected
    interpreter-wrapper or heredoc kind rather than stopping after the first. Recovered Code Mode
    JavaScript emits its recovery warning first and then every detected nested shell warning.
    Warning insertion preserves the exact submitted command, carrier result, replay behavior, and
    metric classification.

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
carrier name followed by the router's canonical serialized stock payload. The stock payload is
the execution carrier unless an exec translator supplies a validated stock command. In that case,
the stock carrier uses the semantic name `functions.exec`, and the router renders the command
through the same canonical exec wrapper, template, and parameters.
Provider-generated item IDs, call IDs, status, and JSON or SSE envelopes are excluded from both
shapes. Plugins supply content evidence but not token counts or outer carrier serialization. A
translated row's reduction is `(translated - emitted) / translated * 100`, may be negative, and
is `n/a` when translated tokens are zero. Router-side input rejection uses a separate failed row
with `n/a` reduction. Executor failures after Codex accepts the execution carrier do not
retroactively become router translation failures.

For hpatch, emitted estimates count the model-visible tool name followed by the exact payload.
A correlated routed recovery chain settles once: its hpatch side is the encoded initial
`functions.hpatch` call plus every encoded `functions.hpatch_recover` call, while its comparator
side is exactly one final `functions.exec` carrier containing the generated `apply_patch` program.
Rejected recovery attempts add their emitted payload but no additional comparator. A successful
recovery atomically compensates every provisional failed row and records one combined `hpatch`
output row against the final comparator; it never records a separate `hpatch_recover` output row.
An abandoned chain retains the combined ineffective tokens and the initial failed comparator.
Per-attempt router telemetry remains individual, and the `hpatch_recover` definition remains
ordinary definition input overhead.
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
multiline value row when localized, and the generated line and column reported by language
syntax validation when applicable. A command with several distinct repair locations retains one
identity per location as defined by `REQ-OUTPUT-001`. Each session also retains the latest
128 routed attempt identities: chain/call identity, attempt, recovery marker, and outcome,
emitted and comparison token counts, evaluated command count, and its bounded rejection
identities. These count limits are reinforced by per-session text-byte limits, so an oversized
rejection identity is not retained. Session records use the same session identity as request
lifecycle metrics and are not written to `metrics.bin`. Each record also carries the client's
own display title for that session when the client exposes one, resolved once per session and
treated as an optional label rather than a counter. They retain neither scripts, replacement
text, diagnostics, nor repair context. Proxy failures that occur before evaluator invocation do
not fabricate evaluator rejection identities.
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

`hpatch gain` first writes an output-token table with one stable row per plugin and tool that
has output-call activity, placing a failed-translation row immediately after its successful
row when present, followed by an all-tools row. Definition-only tools remain visible in the
definition-input breakdown without creating a zero-valued output row. Output-table columns are
emitted tokens, translated tokens, and reduction. The
hpatch failed row retains the fixed direct-call program carrying the empty patch as its
established semantic baseline and reports `n/a`; its downstream diagnostic carrier remains
excluded. A separate recovery table has `Recoveries` and `Count` columns with stable
`white-space error`, `indentation shift`, and `luna misuse` action rows.

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
   emitted count uses the exact model-visible call shape. Its translated count uses the validated
   stock carrier when supplied and otherwise the validated execution carrier. A stock carrier does
   not change execution, history, replay, or runtime-failure classification.
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
9. A recovery is charged as the shorter payload the model emitted for both effective and
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
    totals, at most the latest 128 recovery-aware attempt identities, and at most the latest
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
one command attributed to the header. An exact `PATCH` payload line must use inline escaped
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

Paths are nonempty and consume the remainder of their command line. For root-scoped CLI and library evaluation through `hpatch`, `Translate`, or `TranslateForHost`, relative paths resolve from cwd, absolute paths must remain beneath the canonical root, lexical and symlink escapes fail, and translation emits root-relative paths. Router host evaluation through `TranslateForHostAt` instead uses an optional canonical metadata directory without filesystem confinement. With a directory, relative operands resolve from it; without one, relative operands reject and absolute operands remain valid. Router process cwd is never an implicit base. Emitted patch paths retain cleaned host identities for Codex to authorize.
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
6. For root-scoped evaluation with root `/workspace` and cwd `bin/worktree`, path `main.go` denotes `/workspace/bin/worktree/main.go` and translates as `bin/worktree/main.go`.

## REQ-CORRECT-001 — Rejected-script recovery

The router exposes a separate model-visible `functions.hpatch_recover` custom tool with an
independent embedded Lark grammar. Recovery is unavailable from the standalone CLI, root public
API, root `tool_grammar.lark`, and ordinary `functions.hpatch`. A payload beginning with `type`,
`type-`, or `type+` is therefore always an ordinary complete HPATCH/2 script.

Each rejected-script command has a `C<number>:<hash>` handle covering its complete attributable
command frame. Complete heredocs may expose `V<physical-row>:<hash>` value-row handles. The
recovery operations are `drop`, field-level `target`, `operation`, and `value`, value-row
`value` / `value-` / `value+`, and structural `replace` / `before` / `after`. Inline quoted and
fixed `<<PATCH` values use the shared hpatch syntax framing. There is no sentinel line and no
`accept` operation.

The router owns recovery grammar, parsing, handle resolution, ancestry, worktree isolation,
dispatch, replay, diagnostics, and reevaluation. Every operation resolves against the latest
visible evaluated rejected script as one immutable baseline. The router rebuilds the complete
script through the root `EditText` primitive, then evaluates that script normally.

A malformed, stale, conflicting, incomplete, cross-worktree, or otherwise invalid recovery
changes neither workspace state nor retained rejected ancestry. Proxy-rejected attempts keep
the last evaluated script as the next baseline. A re-rejected recovery becomes the next
baseline, and replay restores the exact `functions.hpatch_recover` payload while retaining its
rebuilt script for later recovery. Non-hpatch plugin and shell failures never enter this
ancestry. Input truncation removes calls the conversation no longer shows.

Every evaluator rejection renders a complete compact manifest of current command handles without
copying mutation values, marks attributable commands, reports their structured correction scope,
and adds bounded heredoc value-row context. Recovery guidance directs the model to submit every
known independent handle-local operation in one atomic payload instead of resubmitting the
complete rejected script. A handle from an older baseline is stale. A re-rejection explicitly
states that no workspace file changed, successful corrections survive only in the new rejected-
script baseline, and every earlier handle is invalid. Correlation IDs remain stable and attempt
numbers increase across evaluated and proxy-rejected calls. Per-attempt telemetry preserves
the emitted tool identity and outcome. Gain metrics settle the correlated hpatch/recovery chain
once according to the combined-payload and single-comparator rules above.

Outcome hooks report one routed attempt once. Their structured event includes tool identity,
chain and call identity, attempt number, correction marker, lifecycle stage, outcome, and
emitted, evaluated, and translated-patch byte counts. `unevaluated/rejected` means the router
rejected the request before engine evaluation. `evaluated/rejected` means engine evaluation
failed without host mutation; `translated/succeeded` means a host patch was produced but does
not claim Codex applied it; `applied/succeeded` means root-owned application completed, while
`applied/failed` means root-owned commit or cleanup failed. Recovery hook Markdown treats the
exact short recovery payload as model-emitted, renders its compact resolved-operation delta,
and identifies any larger complete script as router-rebuilt. Routed evaluator rejection invokes
the outcome hook, not a second per-command error hook. Standalone per-command error hooks are
unchanged.

Acceptance:

1. `functions.hpatch_recover` has a dedicated grammar, is router-only, and omits `accept`.
2. Every recovery operation resolves against one immutable latest evaluated rejected script.
3. A successful rebuild is reevaluated as one complete ordinary HPATCH/2 script.
4. Re-rejection advances the baseline, emits a complete refreshed manifest, and invalidates every prior handle; proxy rejection leaves the baseline unchanged.
5. Recovery cannot cross sessions or selected worktrees, and unrelated tools cannot become bases.
6. Replay restores `hpatch_recover` identity and the exact emitted short payload.
7. Ordinary mutation-leading hpatch scripts are never detected as recovery.
8. Per-attempt telemetry remains individual, while gain counts every chain payload and one
   final or failed comparator.
9. The removed no-`in`, indexed, and dotted value-row recovery forms are ordinary script syntax errors, not compatibility paths.
10. One recovery payload can combine field, value-row, and structural operations against multiple command handles atomically.

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
Hpatch never silently substitutes another line with the supplied hash and never chooses nearby
or duplicate content. Repair diagnostics may list current-line and relocated-hash candidates,
but the caller must verify and choose the target. Line number disambiguates equal lines; the
16-bit hash retains an accepted approximately 1-in-65,536 random false-acceptance residual.

Both endpoints of a range must verify independently and remain ordered. A text target
then searches the verified baseline suffix exactly as defined by `REQ-SCRIPT-001`.
Pending edits never alter row verification, literal search, matches, or positions.
Content introduced by any command is not targetable in that script. Dependent edits
require successful application, hread inspection of the new content, and a later
invocation with fresh references.

Independently detectable row-missing, row-stale, occurrence-missing, and target-order failures
are collected across later commands whose active baselines can still be evaluated safely. The
transaction remains atomic. Dependency-sensitive lifecycle, conflict, and language failures
still stop at their authoritative boundary.

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
is parsed and formatted with Go's standard-library `go/format`; parse failures are collected
from every changed Go file before the complete transaction rejects. For at most 32
content-mutating commands in one invalid Go file, the evaluator replays command-group subsets
against the immutable baseline to select a one-minimal syntax-failing set, then attributes
each useful parser failure to the retained edit nearest its generated parser position. Larger
groups or an invalid baseline use nearest-edit attribution without subset replay. Supported
changed `.py`, `.js`, and `.ts` files are syntax-checked when Tree-sitter language support is
available and contribute all discovered failures to the same validation result. Parser
cascades are collapsed when blanking an earlier repair line removes a later parser failure.
Failures are deduplicated by originating command and physical heredoc value row, or by the
command's script row when no physical value row exists. Each retained location includes at
most two generated lines before and after the failing line; neighboring lines are capped at
64 runes and the failing line at 200. Supported baseline-aware indentation corrections are
applied before validation; unsupported extensions remain byte-exact or reject under
indentation policy.
An unchanged normal-mode change set performs no
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
refs COMMAND OP PATH
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

One `refs` block follows for every effective content-mutating command on every surviving
edited file. `COMMAND` is the command's positive one-based nonblank script index, `OP` is
its authored mutation operation, and `PATH` is the file's final path after pending moves.
Blocks retain authored command order. Each block contains at most four distinct current
rows, ordered by final line number: the rows containing the first and last endpoints of
the command's aggregate rendered edit extent, the immediately preceding surviving row,
and the immediately following surviving row. Missing neighbors are omitted. Coincident
endpoint or context rows are emitted once within that block. A row may appear in separate
blocks when it identifies the context of separate source commands.

The projector derives each aggregate extent from that command's effective editor splices
in rendered final content, then maps both endpoints through language-formatting offsets.
A collapsed deletion endpoint maps to its surviving containing row; its available
neighboring rows provide boundary anchors. Logical-line clamping does not invent a
trailing empty row for a final terminator. An empty surviving file reports row `1` with
the hash of empty content. When the active final file has no `refs` block, the report
retains the existing fallback of up to three rows from the start of that file without a
`refs` header, even when other surviving files have reference blocks.

Every row has `REQ-READ-001` identity over the complete current final logical line.
`TEXT` contains at most the first 64 Unicode code points of line content, without a line
terminator or added ellipsis. Leading spaces are escaped as `\x20`, leading tabs as `\t`, and
all controls use their Go quoted form so indentation is visible and each row stays on one
report line. The hash still covers the complete untruncated content.
The projection is bounded by four rows per effective command, plus the three-row fallback;
it does not retain another original or final content copy, routed-read history, a word
diff, or translated patch text.

A successful report's `LINE:HASH` rows are current references for their named final paths
and may be used directly in the next invocation. The projection does not guarantee that
it contains every possible later target. When the exact target needed next is absent, the
caller obtains it with a focused hread. Saved pre-edit rows remain stale and rejection
context does not authorize guessing or reconstructing a row. The report describes only
the completed invocation; no target or editing state persists into a later invocation.

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

Generic non-command failures emit concise diagnostics to stderr prefixed with `hpatch:`.
Command failures instead have the stable form:

```text
OP: command N[, path "PATH"], reason REASON: MESSAGE
```

The visible command line omits source line, a repeated operation field, and category.
Structured host rejection data retain command index, source line, operation, path, generated
position, and localized value row when applicable; hook data also retain category. Validation
orders failures by command index and then localized value row. It emits one visible command
line per originating command and path. A command with several distinct repair locations uses
the message `N distinct syntax failures`, followed by bounded repair context for every
location; structured host data contain one rejection entry per location. Duplicate parser
messages that resolve to the same command and physical value row, or to the same inline script
row, remain one visible location. Independently parseable syntax failures may be reported
together before evaluation. A heredoc failure is owned by its header and may additionally
report its attributable source span. Control bytes are escaped and embedded newlines are
folded so one command failure remains one logical line.
Failures return nonzero and emit no stdout or final-state report. Malformed row syntax
receives a syntax diagnostic.

A stale row reports the actual current-line candidate and up to two neighboring baseline rows.
It also reports every other baseline line whose hash matches the stale reference as a relocation
candidate, or states that the hash is absent. Range repair reports start and end independently.
No candidate is selected automatically. A missing literal occurrence reports the verified anchor
context. An edit conflict identifies the prior command and affected immutable-baseline lines. If
a command depends on content introduced by another command, the diagnostic directs the agent to
apply the prerequisite independently, reread, and submit a later invocation. A missing row or
failure without a verified baseline does not choose repair context. Repair context is
supplementary: it never changes exit status, stdout, mutation, or metrics classification.
When invalid generated source is localized to a fixed-heredoc mutation, each distinct rejection
identity includes the non-sensitive `value_line`. Transient root diagnostics describe every
bounded value-row context rather than mutation addresses. Routed diagnostics add the current
hashed `C...` handle for each attributable command and bounded `V...` handles around localized
body failures under `REQ-CORRECT-001`. Inline decoded multiline values and failures outside a
multiline replacement do not fabricate a value-row handle.

The public host result separates lifecycle `Outcome`, requested `Change`, routed `Attempt`,
actionable `Failures`, durable-safe `Rejections`, and `PatchSummary`. A valid no-op returns
`evaluated/already-satisfied`, sets `Change.AlreadySatisfied`, and has an empty patch. Failure
scope is `field-local`, `multi-command`, `new-script`, or `new-transaction`; suggestions contain
bounded existing repair context rather than inventing new validation rules.

Acceptance:

1. Normal success has empty stdout and one rendered final-state report on stderr after
   commit; translate success has patch-only stdout and one pending-state report on stderr
   after the patch is completely written. An already-satisfied translate succeeds with empty
   stdout and the rendered state report on stderr.
2. Active paths, bounded last-mutation ranges, per-command final-reference blocks, net file
   counts, Unicode columns, truncation, control escaping, moved files, deletions, and empty
   files produce the specified report without implying cross-invocation persistence.
3. One invocation editing multiple regions and files reports current final paths and rows
   for every effective content command in authored order. A later invocation can target an
   exact reported row without hread, while an unreported target requires a focused read and
   a saved pre-edit row still rejects as stale.
4. Changed Go files are formatted with the standard library before output, and invalid Go
   rejects the transaction without mutation; supported changed Python, JavaScript, and TypeScript files are syntax-checked and receive supported automatic indentation correction.
5. Malformed input, missing, stale, reversed, or incomplete targets, edit conflicts,
   unknown or future commands, invalid UTF-8, missing or non-regular files, path collisions,
   staging failure, translation failure, and cancellation produce no mutation, patch
   output, or final-state report.
6. Injected external filesystem commit and rollback failures are reported without false
   atomicity claims and without a successful final-state report.
7. Failure to write a fully rendered report after a successful external effect does not
   reverse that effect or record a complete report-input token estimate.
8. Stale rows, incomplete literal targets, and edit conflicts emit verified repair context;
   a missing row fails without guessing, and a failure with no active baseline emits its
   diagnostic alone.
9. Invalid Go localized inside a fixed `<<PATCH` value reports its physical body row in
   bounded repair context and structured host rejection identity without retaining body text.
10. One syntax-validation rejection includes every distinct actionable repair location from
    all changed files, groups visible diagnostics once per originating command and path,
    deduplicates parser cascades by repair row, and exposes enough current rejected-script rows
    for one atomic recovery payload to repair all locations.

## REQ-GUIDE-001 — Agent guidance

Top-level help owns the complete CLI, editing, validation, trust-boundary, report, and metrics
reference. `contrib/codex/file-editing-instructions.md` is the single persistent Codex workflow
source for all durable edit, shell, read, search, and inspection guidance and the source of the
HPATCH/2 section returned by tool help. Model-visible tool descriptions contain only concise
call-local contracts and request-specific schemas. The router does not use private tool
descriptions as prompt text and does not mutate Responses instructions.

`make install` renders the central source into Codex's configured `model_instructions_file`
and every instruction file selected by a personal agent TOML under the adjacent `agents`
directory. Relative agent values resolve from the declaring TOML. Existing settings, agent
TOMLs, and all customized content outside the owned guidance section remain byte-equivalent.
A file with current markers is refreshed idempotently; a legacy hpatch section or the pinned
stock Codex file-editing section is migrated once. Without a top-level setting, the installer
uses `CODEX_MODEL` or the lowest-priority bundled model, writes the default file, and adds the
setting. An unrecognized customized file fails instead of being overwritten.

The recovery template adjacent to the central source owns dynamic recovery prose. After each
actionable structured evaluator rejection, the router supplies a complete compact current
command manifest, marks attributable commands with correction scope, and adds localized `V...`
value-row context. A re-rejected recovery states that prior handles are stale and refreshes the
manifest from the latest evaluated script.

Persistent guidance teaches this workflow:

1. Submit a shell call as one free-form script without an outer wrapper. Use Bash by default or
   select another interpreter with a direct compact shebang. Keep program input on standard input,
   use exactly one `{.}` in `#!cmd=`, place request-specific outer arguments in `#!params=`, and
   use native session facilities for PTY-backed or long-running executions.
2. Inspect, edit, or rerun a retained shell script through its `@shell/` reference, and never mix
   retained and workspace paths in one hpatch script.
3. For an authorized edit, use hread as the initial source read when the named or likely owner is
   known. Use hgrep as the initial search when a known identifier or literal is likely to become
   a target. Use ordinary reads and searches for read-only work or while the owner is unknown;
   after discovery, hread only the smallest target-bearing range.
4. Run one hread command per file and batch related commands in one shell script. Combine known
   hgrep patterns and paths with repeated `-e`. Copy only current emitted references. A matching
   hgrep row needs no hread unless surrounding or nonmatching context is required.
5. Choose a line, inclusive range, or anchored literal target inside the mutation command.
6. Submit every known related edit in one atomic script. Split only when a later edit depends on
   validation or information unavailable before the current call. Keep unrelated large values
   in separate failure-domain calls.
7. Prefer the smallest semantic mutation and let formatters own formatting. Successful
   final-state `LINE:HASH` rows can be used directly in the next invocation.
8. Use nonempty `type` to replace, empty target-bearing `type` to delete, `type-` to insert
   before, and `type+` to insert after. Use inline values for short text and `<<PATCH` for
   multiline or escape-heavy values.
9. After a routed rejection, use `functions.hpatch_recover` with the current `C...` and `V...`
   handles. Submit every known independent correction in one atomic payload rather than
   resubmitting the complete rejected script. After re-rejection, discard all prior handles.
   The standalone CLI has no recovery mode.
10. Let hpatch format changed Go files and syntax-check supported changed Python, JavaScript, and
    TypeScript files.

Acceptance:

1. A model can choose and encode every HPATCH/2 operation from tool help without learning
   HPATCH/1 state concepts.
2. The installed prompt contains the central guidance exactly once and omits the pinned stock
   apply_patch, rg, and exec_command instructions.
3. A configured legacy or marked customized prompt retains content before and after the owned
   section, and repeated installation is idempotent.
4. A routed request's existing instructions remain byte-equivalent, including absence or null.
5. Dynamic rejected-script references and recovery prose appear only with actionable context.
6. Every actionable evaluator rejection includes a complete compact current command manifest,
   correction scope for attributable commands, and exact guidance for atomic multi-operation
   recovery; re-rejection explicitly invalidates prior handles.
7. A routed success can be followed by another hpatch call using an exact row from its report
   without an intervening hread; a saved pre-edit row still rejects as stale.
