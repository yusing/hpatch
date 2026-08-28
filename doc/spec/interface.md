# Interface contract

## REQ-READ-001 — Shell-routed verified-row reader

In hpatch router mode, the model receives `hpatch` and `shell` as standalone custom tools.
All persistent hread, hgrep, hsymbol, inspect_file, shell-execution, and HPATCH workflow guidance comes
from `contrib/codex/file-editing-instructions.md`. The router injects a protocol-specific projection
of that source into the top-level Responses `instructions` value in memory and never changes an
instruction file. Native model protocol omits the leading CTP/2 section and stops after the ordinary
guidance and tool rewrite. CTP/2 injects the complete source, preserves the selected top-level or
first textual developer-message carrier, and transforms eligible strings under `REQ-CTP-001`.
Hread, hgrep, hsymbol, and inspect_file remain private executor contributions inside the
authenticated shell worker; their custom-tool specifications are not sent to the model, direct
model calls to their names are not routed, and no executable frontend is installed for them.

The private `hread` command accepts exactly one file:

```text
hread PATH [START:END]
```

The shell owns quoting and argument separation. A path containing whitespace is therefore one
ordinary quoted shell argument. `START:END`, when present, is an inclusive logical-line range
whose positive one-based base-ten endpoints must be ordered. The start line must exist. An end
past EOF returns through the final line. One `hread` invocation never accepts a second path or a
newline-delimited batch. The model batches related reads as separate hread commands in one
shell script.

The shell carrier invokes the fixed `shell` helper from the executor's trusted `PATH`. The
router stores the current authenticated shell worker at
`$HPATCH_RUNTIME_DIR/hpatch-$CODEX_THREAD_ID/.runtime`; the helper reads that path and replaces
itself with the worker. Its `mvdan/sh` Bash and POSIX evaluators intercept the exact command
names `hread`, `hgrep`, `hsymbol`, and `inspect_file` after ordinary shell expansion, then call the matching
immutable snapshot implementation directly. These private names are not filesystem entries and
do not use `PATH`. A deployment that isolates router and executor filesystems must expose the
thread runtime path, router executable, and authenticated snapshot at the same
absolute paths, separately from the user workspace.

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

Verified-row commands count exact formatted current stdout with the GPT-5 tokenizer. They admit
rows through 15,000 tokens. One next complete row may raise the result to at most 15,500 tokens;
admitting that row seals the result. EOF at that point is complete. A later row, or any row that
would exceed 15,500 tokens, is omitted together with every later row. Omission preserves already
admitted complete rows on stdout, writes an incomplete-result diagnostic to stderr, and returns
nonzero. It never cuts a row.

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
   cancellation. Token-limited output retains only admitted complete rows, and current and stock
   results retain the same source rows without a second read.
5. Success and failure reach Codex through the model-visible shell carrier. Replay retains
   the original shell call and output; it never synthesizes a model-visible hread call or
   includes the shell call in editable rejected-script recovery history.
6. Router startup validates hread inside the immutable built-in snapshot without installing a
   frontend. Passthrough mode loads and exposes none of these replacement surfaces.

## REQ-GREP-001 — Shell-routed verified-row search

The private `hgrep` command is available through the model-visible shell tool. The shell owns
quoting, redirection, pipelines, and command composition; hgrep receives the resulting ordinary
argv. It accepts familiar ripgrep pattern, matching, file, glob, type, ignore, context, and
resource-selection arguments. With no explicit path, search defaults to the shell process's
current directory. GNU grep's `-R` is accepted and removed because traversal is already
recursive by default.

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
diagnostics. Output contains only complete rows and uses the shared verified-row token admission
rule in `REQ-READ-001`. On the first omitted distinct result, hgrep terminates and reaps ripgrep.

For input metrics, hgrep produces its current and stock results from the same ripgrep event
stream. The stock result preserves each JSON-quoted `PATH`, `TEXT`, LF, result order, and
diagnostic while omitting the `LINE:HASH ` portion. The comparison does not run ripgrep twice.

Acceptance:

1. A regular-expression search with an explicit path and glob emits JSON-quoted paths,
   positive line numbers, four-digit hashes, and exact complete matching lines that can be
   copied directly into an hpatch target.
2. Shell quoting determines literal arguments, and ordinary redirection or pipelines operate
   as shell syntax rather than becoming hgrep argv. A conflicting hgrep output or transformed
   input mode still rejects before ripgrep starts. The `-R` compatibility flag does not reach
   ripgrep or produce a warning.
3. Requested before/after context emits complete verified rows beside matches. Repeated match
   or context events on one row emit that row once; no matches return successful empty stdout.
   Token admission occurs after this deduplication, and an incomplete result retains paired
   current and stock rows, writes its diagnostic to stderr, and returns nonzero.
4. The model-visible shell call and output are replayed unchanged. No standalone hgrep call is
   exposed, routed, or admitted to hpatch recovery history.
5. Router startup validates hgrep inside the immutable built-in snapshot without installing a
   frontend. Passthrough mode loads and exposes none of these replacement surfaces.

## REQ-SYMBOL-001 — Shell-routed semantic symbol lookup

The private `hsymbol` command is available only through the model-visible shell tool:

```text
hsymbol def PATH LINE:HASH SYMBOL [N]
hsymbol refs PATH LINE:HASH SYMBOL [N]
```

The canonical workspace is `realpath(process.cwd())`. `PATH` may be relative or absolute, but
its canonical target must remain within that workspace and be a regular UTF-8 supported source
file. Supported sources are Go `.go`; Python `.py` and `.pyi`; JSON `.json`; and the stable TypeScript 7
formats `.ts`, `.tsx`, `.d.ts`, `.mts`, `.d.mts`, `.cts`, `.d.cts`, `.js`, `.jsx`, `.mjs`, and
`.cjs`.
`LINE:HASH` identifies one current logical line under `REQ-READ-001`. Hsymbol verifies the line
and hash before starting a resolver and never searches for another matching hash.

`SYMBOL` selects an exact language token on the verified line. Go accepts non-keyword identifiers;
JavaScript and TypeScript accept their identifier, property, private-name, type-name, and JSX-name
tokens; Python accepts identifier and property tokens; JSON accepts a decoded property-name or
string token. Comments, larger identifiers, and unrelated literal text do not count. `N`, when
present, is a positive base-ten occurrence without leading zeroes. When `N` is absent, exactly
one matching token must exist; multiple matches fail as ambiguous before the resolver starts.

Each invocation starts exactly one semantic query at the selected token. Go uses one
`gopls definition -json` or `gopls references -d` query at a UTF-8 byte offset. JavaScript,
TypeScript, and JSON start `tsc --lsp --stdio`; Python starts `pyright-langserver --stdio`. The LSP
client initializes the canonical workspace, opens the verified source snapshot, negotiates UTF-16
positions, sends one `textDocument/definition` or `textDocument/references` request, and reaps the
server after the response. References request `includeDeclaration: true`. There is no text-search
fallback. A missing resolver, invalid arguments, stale rows, invalid selectors, malformed protocol
result, or failed semantic query returns concise stderr and nonzero status without useful stdout.

Successful stdout contains first-seen complete verified rows:

```text
"PATH":LINE:HASH TEXT
```

`PATH` is the JSON-quoted path from the canonical workspace root to the canonical result file,
without a leading `./`. Each result file is canonical, in-workspace, regular, UTF-8, and owned by
the selected resolver; other returned locations are omitted and counted by reason on stderr.
References are deduplicated by canonical path and logical line. Empty `refs` is successful.
A `def` without an editable workspace location is nonzero. An incomplete token-limited result is
not resumable and does not establish a complete definition or reference set. Location skip counts
still cover the complete resolver result. `def` emits every editable definition returned by the
resolver in first-seen order and deduplicates canonical result rows.

Definition expansion occurs only when the resolver's definition selection exactly matches the
declared name of a complete inspect_file outline entry. Supported package or module declarations,
functions, classes, types, variables, and direct methods emit the entry's inclusive logical-line
range. JSON, imports, fields, parameters, locals, and files with uncertain parsing emit only the
definition line. Hsymbol uses the shared verified-row token admission rule in `REQ-READ-001` and
never emits a partial row.

The stock result is stdout and stderr from the same semantic query. For LSP resolvers, stdout is
the JSON serialization of the definition or references result without initialization and shutdown
traffic. Metrics do not run a second query. Shell replay retains the original shell call and
output. Hsymbol remains private, is not routed as a standalone model-visible tool, and never enters
editable rejected-script recovery.

Acceptance:

1. A verified use-site token resolves through one language-appropriate semantic query, and emitted
   hashes equal hread for the same current lines.
2. Every listed source format is accepted. Omitting `N` selects one unique exact language token and
   rejects an ambiguous line before the resolver starts; comments, unrelated literal text, and
   larger identifiers do not affect the count.
3. `def` expands only supported exact outline declarations; every other valid definition emits its
   one current logical line. Multiple definitions retain resolver order and deduplicate rows.
4. `refs` includes declarations, preserves first-seen order, deduplicates one canonical path and
   line, reports skipped locations, and accepts an empty result.
5. Relative and absolute in-workspace paths work. Lexical escapes, escaping symlinks, stale rows,
   missing resolvers, malformed protocol results, and uneditable definitions fail without useful
   stdout.
6. Router startup validates hsymbol inside the immutable built-in snapshot without adding a
   model-visible tool or executable frontend. Passthrough mode loads no private command, and
   shell history containing hsymbol is not recovery
   ancestry.

## REQ-INSPECT-001 — Shell-routed structural file inspection

The private `inspect_file PATH` command is available only through the model-visible shell tool.
It accepts exactly one shell-separated workspace-relative path and no options. The canonical
workspace is `process.cwd()`. Absolute paths, lexical escapes, `@shell` paths, and symlinks whose
canonical targets escape that workspace fail. In-workspace symlinks are allowed, and the final
target must be a regular file.

Extension matching is exact and case-sensitive. Code formats are `.go`, `.py`, `.pyi`, and every stable
TypeScript 7 source extension: `.ts`, `.tsx`, `.d.ts`, `.mts`, `.d.mts`, `.cts`, `.d.cts`, `.js`,
`.jsx`, `.mjs`, and `.cjs`. `.md` and `.json` remain structural formats. These formats use pinned
parsers; TypeScript and JSX select the corresponding JavaScript parser dialects, while Markdown
uses Lezer for headings and YAML for a closed initial frontmatter block. Every other extension returns
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
4. Router startup validates `inspect_file` inside the immutable built-in snapshot without an
   executable frontend and exposes or routes only hpatch, shell, and configured model-visible
   contributions. Eligible request instructions use the central guidance while unrelated
   content remains unchanged; CTP/2 follows `REQ-CTP-001`.

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
Configured executor-backed names must also differ from shell keywords and built-ins. This rule
ensures that their basename carrier selects an executable frontend instead of shell-owned
behavior.

Before opening its listener or installing any configured contributed-tool wrapper, the router
loads every discovered declaration and validates the complete registry. It reports all detected plugin
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
quoting, and result forwarding. Its canonical Bash quoting keeps the worker command on one
physical line, escaping embedded line terminators while reconstructing each exact argv value.
The optional exec command template contains exactly one `{.}`
placeholder, which the router replaces with the complete quoted worker command. For configured
tools this is their frontend command. For built-in shell it is normally the fixed
`shell <interpreter> <program>` helper command; without a shebang, directive, or template, one
physical line containing one static external Bash command instead remains the complete outer
command. An optional JSON parameter object cannot contain `cmd`. The router supplies `cmd` from
the selected command. If the parameter object contains `login`, its value must be exactly `false`.

An exec translator may also return one nonempty stock command for output metrics. The router
applies the same optional command template and JSON parameters, then renders the stock command
through the canonical exec wrapper. This stock carrier is metric evidence only: the response,
history, replay, and execution paths retain the validated worker carrier. Without a stock
command, output metrics use that worker carrier as before.

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
Startup removes an authenticated frontend for one of the retired built-in names when it was left
by a crashed pre-revamp router, without removing an unrelated file or link.

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
incorrectly targeted, or unusable configured-tool symlinks fail startup before the listener
opens. When configured frontends exist, the router holds one exclusive frontend lock for its
process lifetime and a concurrent router fails startup. A built-in-only registry takes no
frontend lock. After a crash releases a configured frontend lock, a later router can replace
authenticated prior frontends even when the prior process snapshot remains.

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
3. One invalid declaration or configured-tool symlink prevents the listener from opening;
   independent startup mismatches are reported together and no valid subset is exposed.
4. Duplicate tool names across plugins or built-ins fail startup, and the registry does not
   change until process restart.
5. A plugin may translate to any compatible Code Mode tool call available in the current
   request; an unavailable or wrong-kind carrier rejects before upstream execution.
6. The exec wrapper renders the canonical outer exec shape and independently quotes every argv
   value. An optional template contains exactly one `{.}`, which expands to the complete worker
   command. The plugin declaration does not contain or generate the outer carrier shape.
7. Invoking a configured executor-backed tool resolves its stable basename frontend through the
   authenticated snapshot wrapper to `hpatch-router`, verifies the pinned registry, dispatches
   by `argv[0]`, and delivers the declared argv under Codex's cwd, sandbox, and permissions.
8. JSON and SSE responses preserve call identity while replacing a contributed call with its
   validated carrier. While the complete streaming input is buffered for validation, each withheld
   input delta becomes a content-free native `response.in_progress` event so downstream SSE remains
   active without exposing untranslated content. Replay restores the exact original contributed
   call after verifying the retained carrier.
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
regenerates that bundle and installs `hpatch-router` plus the fixed `shell` helper. It changes no Codex configuration,
instruction file, or configured shell declaration.

The tool treats the first logical line as a shebang when that line, after trimming only its
leading and trailing ASCII spaces and tabs, starts with `#!`. It removes `#!`, trims the
remaining selector, and separates the selector at ASCII spaces or tabs. A bare executable name
is valid. A direct executable path remains unchanged. A leading `env` or `/usr/bin/env` and an
optional following `-S` are removed. A selector whose case-insensitive basename is `bash` or
`bash.exe` selects `mvdan/sh` Bash evaluation; `sh` or `sh.exe` selects its POSIX evaluation.
This basename rule also applies to direct paths such as `/usr/bin/bash` and `/bin/sh`. Every
other bare selector resolves through the inherited `PATH`.
An empty selector, an `env` selector without an executable, a NUL byte, or too many or oversized
argv values rejects before execution. Without a shebang, the selected interpreter is `bash`.

When a shebang is present, the script body is every input byte after the complete first-line
terminator. The tool removes only the shebang line and its terminator. It preserves all leading
and trailing body whitespace, including an absent or final line terminator. Without a shebang,
the complete input is the body. The translated argv contains each normalized interpreter field
followed by the exact body as its final value. The resulting Codex exec carrier therefore shows
`shell python3 <quoted-body>` on one physical command line; the model does not author that command
or its quoting. For implicit default Bash without a directive, a body with at most one final line
terminator remains direct when it parses as one non-background, non-negated simple call whose
static command is neither a shell built-in nor a private contribution and whose statement contains
no command or process substitution. The direct carrier removes that optional final line terminator
and otherwise preserves the command text.

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
The router replaces `{.}` with the canonical independently quoted shell-helper command and argv.
The command template then runs through the normal exec carrier shell. Without an interpreter
shebang, the nested worker selects `bash`. Without either directive, an eligible simple external
Bash command remains direct; every other body uses the worker command as the complete outer
command. After the first body line, directive-like lines remain ordinary body data.

When the worker carrier is selected, the executor starts the fixed helper once with the normalized
interpreter fields and exact body.
The helper reads the current thread runtime path and replaces itself with the authenticated
router worker, without a second Codex executor call. For Bash and sh basenames,
the worker parses the body with `mvdan/sh` using
`LangBash` or `LangPOSIX`, applies supported middle fields as shell options or parameters, and
executes the syntax in-process. Its exec handler receives expanded argv, invokes hread, hgrep,
hsymbol, and inspect_file directly from the authenticated snapshot, and delegates every other
external command to the inherited environment. Private command stdout, stderr, status,
redirections, pipelines, cwd, exported environment, and cancellation remain part of the same
shell evaluation; no private command launches another router worker. Each non-terminal fallback
external command owns a cancellable process group so its descendants cannot retain shell streams
past cancellation or the output limit. Every external command in a PTY-backed shell remains in
the worker's foreground process group and uses a bounded inherited-pipe wait on cancellation,
preserving terminal input for direct commands and piped stages that read `/dev/tty`.

Other interpreters retain the plugin executor path. It passes middle fields as interpreter
arguments, supplies the final exact body through an anonymous script descriptor such as
`/dev/fd/3`, and leaves standard input available as program data. Neither path stores an
intermediate script file. Without `#!params=`, the worker inherits Codex's execution context.
With `#!params=`, Codex applies the accepted outer exec arguments before launching the worker.
The worker returns stdout, stderr, and exit status without copying the script body into either
output stream.

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
canonical Code Mode exec shape. Execution and replay retain the selected direct-command or
shell-helper carrier.

Acceptance:

1. A free-form call containing `#!/usr/bin/env python3` translates to an exec carrier whose
   visible command is one physical `shell python3 <body>` line with embedded body line terminators
   escaped; execution resolves the current thread-bound runtime and runs `python3` with that exact
   body as its anonymous script source.
2. `#!python3`, `#! python3`, and `#!/usr/bin/env python3` select `python3`. A directly supplied
   path such as `#!/opt/python/bin/python3` remains unchanged.
3. `#!/usr/bin/env -S python3 -u` runs `python3` with `-u` and the exact body as its anonymous
   script source.
4. `#!cmd=curl -fsSL URL | {.} | jq` without an interpreter shebang expands `{.}` to the
   independently quoted fixed helper selecting Bash. The curl response becomes Bash
   standard input while the exact remaining body remains the script source.
5. When `#!python3` precedes that command directive, `{.}` expands to the independently quoted
   fixed helper selecting Python. The command-template input becomes Python standard input.
6. A missing, empty, or repeated `{.}` placeholder rejects before execution. A command directive
   in any later body line remains ordinary body text.
7. Input without a shebang or command directive selects Bash semantics. One physical line
   containing the static external command `rtk shadowtree test . -run='^$'` and one optional final
   line terminator produces that direct native command without `shell bash`; shell built-ins,
   private commands, nested command or process substitutions, composed statements, and malformed
   syntax retain the fixed helper. Explicit `bash` and `/usr/bin/bash` selectors have the same
   `mvdan/sh` Bash semantics; `sh` and `/bin/sh` have the same POSIX semantics and reject Bash-only
   syntax.
8. Python indentation and all other body-leading or body-trailing whitespace remain byte-exact
   after recognized directive removal.
9. The worker inherits cwd, environment, and standard input. Its stdout, stderr, and
   nonzero status are returned without script-source duplication or an intermediate script file.
   Cancellation and output overflow terminate non-terminal fallback-command descendants that
   retain inherited streams. PTY-backed external commands, including piped stages that read
   `/dev/tty`, accept interactive input without a background-process-group stop.
10. Malformed selectors and input that cannot fit the bounded exec argv return a concise
    diagnostic without starting an interpreter.
11. `make install` installs `hpatch-router` and the fixed `shell` helper without changing Codex
    configuration or instruction files. Startup and tool-snapshot changes do not rewrite that
    helper and create no hread, hgrep, hsymbol, or inspect_file basename frontend. An authenticated
    pre-revamp frontend for one of those private names is removed during upgrade; unrelated paths
    remain unchanged.
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
    without calling the continuation operation or starting the worker again. No router session
    record or plugin-defined continuation surface is created.
16. For one built-in shell input, the router emits one warning for every distinct detected
    interpreter-wrapper or heredoc kind rather than stopping after the first. Recovered Code Mode
    JavaScript emits its recovery warning first and then every detected nested shell warning.
    Warning insertion preserves the exact submitted command, carrier result, replay behavior, and
    metric classification.

## REQ-METRICS-001 — Persistent token, command, target, and failure metrics

Each host API invocation returns evaluator counters in the metrics fields of `HostTranslation`; obtaining that result does not persist them. The host supplies the visible
hpatch payload, comparison carrier, rendered report visibility, session attribution, and other
transport evidence to `RecordHostMetrics`, which is the only root persistence boundary. The router
calls `RecordHostMetrics` after the routed outcome is known. Basic `Apply` and `Translate` neither
return `HostTranslation` nor automatically persist metrics.

A successfully recorded nonempty change set contributes paired estimates for two semantically
equivalent tool calls. A recorded failed invocation contributes only its host-supplied generated
`hpatch` call estimate to the ineffective-output counter; it contributes nothing to the effective
`hpatch` counter. A failed routed invocation is represented downstream by a Code Mode carrier that returns its
diagnostic and repair context. Its comparison baseline is the fixed direct-call program
carrying `*** Begin Patch\n*** End Patch\n`; that tokenized semantic baseline contributes
to the failed translated counter. The diagnostic carrier itself never counts as translated
hpatch output. The complete failed hpatch call remains in the ineffective-output counter and
reduces the overall output savings. Metrics reads and unsupported host calls do not contribute metrics.

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

A final-state report contributes its exact rendered text to the state-report input-token counter
only when the host supplies evidence that the report became model-visible. This is model-input
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
supplies no session or definition leaves these counters at zero, and the structured metrics presentation states which inputs were measured so a zero is not read as a free tool.

A failed or cancelled invocation emits no report and contributes zero report-input tokens. A partial or failed routed report emission does not count as a complete emitted report. For each completed contributed
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

Each terminal Responses lifecycle log attributes the observed provider input, cached input,
uncached input, output, and reasoning counts to one logical router request. It never logs the
cache key, authorization material, prompt body, instructions, tool definitions, or input history.
Requests without terminal usage omit token-count fields rather than reporting zero usage.

The router's in-memory metrics snapshot also attributes successful and rejected hpatch
translations and rejected-call diagnostic input tokens to the request session. Each session
retains the latest 32 evaluator rejection identities: command index, physical source line,
operation, target kind when known, stable reason, affected path when known, the physical
multiline value row when localized, and the generated line and column reported by language
syntax validation when applicable. For a rejected line or range target that the router could
parse before evaluation, the identity also carries `none`, `exact`, `contains`, `contained`, or
`overlap` for its inclusive row-coordinate span relative to confirmed same-path prior replacement
targets. The relation is derived without retaining row hashes or target content. A command with
several distinct repair locations retains one
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

Each session retains the latest 128 completed Responses request observations. An observation carries
only its router request sequence, terminal lifecycle outcome, total and upstream duration, whether
provider usage was observed, and the provider input, uncached-input, output, and reasoning-token
counts. A separate dropped-observation counter exposes retention truncation. Request bodies,
response bodies, credentials, and provider identifiers are never retained by this telemetry.

With CTP/2 enabled, aggregate and per-session metrics record considered, active, and missing-carrier
requests and sum native and compact tokens and UTF-8 bytes for active requests and decoded assistant
text. They also sum encoded strings, visible-line references, content-local dictionary definitions
and framing bytes, encode and decode operations and nanoseconds, and response-decode failures. Each
session retains the latest 128 input observations and 128 assistant-output observations with their
request sequence, representation sizes, framing counts, activation decision, and encode timing.
Independent dropped counters expose truncation. These observations retain sizes and decisions only,
never dictionary values, locators, or text. Streaming decode timing counts each transformed upstream
event, while assistant-output observations still count each logical terminal text exactly once under
`REQ-CTP-001`.

Validated compaction requests bypass CTP/2 and therefore add no considered-request observation.

`RecordHostMetrics` persists classification only after the host supplies the terminal outcome and
visible carrier evidence. For router translation it records a paired effective estimate after the
complete patch is available; for host application it records one only after the staged changes
commit. The router records report-input tokens only after the complete final-state report is
emitted. Parse, evaluation, translation, carrier, and commit failures record only the supplied
hpatch estimate as ineffective. Successful no-op host results contribute evaluator command counts
and, when model-visible, a report estimate without paired effective token estimates. Failure to
render an equivalent patch after a successful host commit records no paired token classification,
but retains the supplied evaluator counters and completed visible-report metrics.

Every supported command reached by evaluation contributes one invocation. A supported
operation rejected by syntax parsing contributes one invocation and one error when its
operation and attempted variant are structurally recognizable. An operation whose path
resolution or execution fails contributes one error after its invocation. Unknown or
future operations and failures outside command processing are not attributed to a
supported command. Successfully evaluated commands retain their invocation counts when a
later output or filesystem-commit boundary fails. Supported command counters are:

```text
in  new  mv  rm  type  add
```

Every structurally recognized explicit target attempt increments one target counter:

```text
line  range  text-single  text-multiple
```

Targetless `type VALUE` initialization has no target counter. Anchored and unanchored text
targets use the same counters. A text target with omitted count or count one is
`text-single`; an explicit count intended to exceed one is
`text-multiple`, including an invalid multiple count. Unknown commands are syntax failures
but do not receive supported-command or target attribution.

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
`hpatch/metrics.lock`; structured metrics reads hold a shared lock. The current-version metrics format uses
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

The router dashboard and `GET /api/metrics` expose the persisted aggregate as structured output.
They include one stable output-token row per plugin and tool with activity, optional adjacent
failed-translation rows, an all-tools row, installed-definition reconciliation, and the hpatch
failed semantic baseline. A separate recovery table has stable `white-space error`,
`indentation shift`, and `luna misuse` rows.

The same surfaces expose current-versus-stock input-token rows, final-state report and failure
diagnostic overhead, displaced native-definition credits, installed-definition totals, net added
input, command invocation and error rates, target counters, terminal reasons, and nonzero
command-and-reason pairs. Percentages are rounded to one decimal place and are zero when their
denominator is zero. With no metrics file or only an obsolete record, all totals and percentages
are zero. A metrics read does not create or rewrite the file. Tokenization, locking, persistence,
or presentation failure remains auxiliary and cannot change the requested effect.

Acceptance:

1. Host variants expose evaluator counters in `HostTranslation` without persistence. Repeated
   router calls to `RecordHostMetrics` persist cumulative paired hpatch estimates and completed
   visible-report input estimates; failed recorded invocations persist only ineffective hpatch
   estimates and zero report-input tokens.
2. Every successfully translated contributed-tool call persists a plugin-and-tool output row whose
   emitted count uses the exact model-visible call shape. Its translated count uses the validated
   stock carrier when supplied and otherwise the validated execution carrier. A stock carrier does
   not change execution, history, replay, or runtime-failure classification.
3. Every completed executor result persists current and stock input estimates for its plugin and
   tool. An omitted stock result produces equal estimates and zero reduction without a second
   execution. A zero-token stock result reports `n/a`.
4. Structured router metrics report stable per-plugin and per-tool output rows, optional adjacent failed rows, and one
   all-tools output row. They report a separate input table with current, stock, reduction, and one
   all-tools row.
5. The input-overhead table has no plugin child rows. Net added input includes the signed difference
   between current and stock tool-result estimates.
6. The six supported hpatch command counters and four target counters reconcile with
   aggregate command attempts and errors.
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
11. Concurrent writers lose no records, concurrent structured metrics reads never observe a partial
   aggregate, and an interrupted or damaged latest state falls back to the preceding valid
   aggregate.
12. A valid mismatched `HPATCH` version resets totals when no current state exists; malformed
    data does not count as a version mismatch, and current state takes precedence.
13. `RecordHostMetrics` failure warns without changing the success or failure of the requested
    edit, translated carrier, executor result, or final-state report; omitting the call leaves
    persistence unchanged.
14. Router snapshots attribute successful and rejected hpatch translations, diagnostic token
    totals, at most the latest 128 recovery-aware attempt identities, and at most the latest
    32 structured evaluator rejection identities, including parseable same-path row-span relation
    to confirmed prior replacement targets, to their request sessions without persisting scripts,
    row hashes, replacement text, diagnostics, repair context, or new per-session records in
    `metrics.bin`; per-session text-byte limits may retain fewer identities.

## REQ-SCRIPT-001 — HPATCH/2 script grammar

Outside a heredoc body, blank lines are ignored and every other physical line begins exactly
one command:

```text
in PATH
new PATH
mv PATH
rm
type TARGET VALUE
add DESTINATION VALUE
type VALUE
```

The final form is new-file initialization and is valid only under `REQ-FILE-001`.

Targets are:

```text
ROW                         complete logical line
ROW..ROW                    inclusive complete-line range
ROW "TEXT" [COUNT]          anchored exact literal occurrence(s)
"TEXT" [COUNT]              whole-baseline exact literal occurrence(s)

ROW   := LINE:HASH
LINE  := positive one-based decimal logical line
HASH  := exactly four lowercase hexadecimal digits
COUNT := positive decimal integer; default 1
```

An add destination is a single `ROW`, an anchored or unanchored text target, or the literal
`EOF`. `add` does not accept a range. `EOF` is a destination sentinel rather than a target
and contributes no target metric.

No whitespace is permitted inside `ROW..ROW`. A line target owns the complete logical
line, including its terminator when one exists. A range owns all
complete logical lines between its endpoints, inclusively.

A text target either verifies its anchor row and starts at that row's column 1 or, without a
row, starts at byte zero. It searches exact literal content forward through EOF. `TEXT` is
nonempty. Its quoted source remains on one physical command line, but JSON-escaped LF (`\n`
or an equivalent `\u000A` escape) decodes into the exact target literal and may make one match
span logical lines or include a trailing LF. Literal horizontal tab is also accepted. Raw
physical newlines, CR in every representation, and every other C0 control are forbidden.
Matching is left-to-right and resumes after each complete match. The target contains the first
`COUNT` non-overlapping matches and rejects if fewer exist.

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
add 37:8c2f "// parseCommand parses one physical script line.\n"
type 12:a1b2 "needle" "replacement"
type 12:a1b2 "needle" 3 "replacement"
type "known current text" "replacement"
add EOF <<PATCH
appended text
PATCH
```

Paths are nonempty and consume the remainder of their command line. For root-scoped library evaluation through `Translate` or `TranslateForHost`, relative paths resolve from cwd, absolute paths must remain beneath the canonical root, lexical and symlink escapes fail, and translation emits root-relative paths. Router host evaluation through `TranslateForHostAt` instead uses an optional canonical metadata directory without filesystem confinement. With a directory, relative operands resolve from it; without one, relative operands reject and absolute operands remain valid. Router process cwd is never an implicit base. Emitted patch paths retain cleaned host identities for Codex to authorize.
Trailing operands, malformed rows, forbidden controls, missing values, and unknown
commands are invalid.

Acceptance:

1. Every accepted nonblank command is one of the six public commands.
2. Line, range, anchored text, and unanchored text targets parse without a separate selection command, and inline
   replacement values remain distinguishable from a text target's quoted literal.
3. Anchored and unanchored text targets accept JSON-escaped LF and exact multiline or
   trailing-LF matches while raw physical newlines, CR, empty literals, and other forbidden
   controls reject.
4. JSON-compatible values and the fixed `<<PATCH` heredoc reproduce their exact decoded
   payloads without parsing body lines as commands.
5. Invalid rows, ranges, counts, strings, heredocs, operands, and commands fail before
   filesystem mutation, patch output, or final-state reporting.
6. File and mutation commands may be interleaved while all targets retain the immutable
   baseline meaning defined by `REQ-SELECT-001`.
7. For root-scoped evaluation with root `/workspace` and cwd `bin/worktree`, path `main.go` denotes `/workspace/bin/worktree/main.go` and translates as `bin/worktree/main.go`.

## REQ-CORRECT-001 — Rejected-script recovery

The router exposes a separate model-visible `functions.hpatch_recover` custom tool with an
independent embedded Lark grammar. Recovery is unavailable from root public APIs, root
`tool_grammar.lark`, and ordinary `functions.hpatch`. A payload beginning with `type` or
`add` is therefore always an ordinary complete HPATCH/2 script.

Each rejected-script command has a `C<number>:<hash>` handle covering its complete attributable
command frame. Recovery has exactly one form per line: `C<number>:<hash> TARGET`. `TARGET` uses
the ordinary HPATCH/2 row, range, anchored-literal, or unanchored-literal target syntax and must
denote a different target from the retained command. Recovery
has no operation keyword and cannot change operations, values, heredoc bodies, command count or
order, or file context. Target parsing and script rebuilding preserve the public target literal's
exact decoded bytes, including escaped LF, and enforce the same empty, CR, and control
exclusions.

The router owns recovery grammar, parsing, handle resolution, ancestry, worktree isolation,
dispatch, replay, diagnostics, and reevaluation. Every command handle resolves against the latest
visible evaluated rejected script as one immutable baseline. Each handled command may appear at
most once in a payload. The router changes only its target, rebuilds the complete script through
the root `EditText` primitive, then evaluates that script normally.

A malformed, stale, unchanged, conflicting, incomplete, cross-worktree, or otherwise invalid recovery
changes neither workspace state nor retained rejected ancestry. Proxy-rejected attempts keep
the last evaluated script as the next baseline. A re-rejected recovery becomes the next
baseline, and replay restores the exact `functions.hpatch_recover` payload while retaining its
rebuilt script for later recovery. Non-hpatch plugin and shell failures never enter this
ancestry. Input truncation removes calls the conversation no longer shows.

When every structured rejection is `row-stale`, the routed diagnostic lists only the rejected
target-bearing commands and their current `C...` handles. Recovery guidance directs the model to
submit one `C... TARGET` line per listed command in one atomic payload. Every non-target or mixed
failure instead directs the model to submit one complete corrected ordinary HPATCH/2 script. A
handle from an older baseline is stale. A re-rejection explicitly states that no workspace file
changed, target corrections survive only in the new rejected-script baseline, and every earlier
handle is invalid. Correlation IDs remain stable and attempt
numbers increase across evaluated and proxy-rejected calls. Per-attempt telemetry preserves
the emitted tool identity and outcome. Persistent metrics settle the correlated hpatch/recovery chain
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
the outcome hook, not a second per-command error hook. Root error hooks remain separate from routed outcome hooks.

Acceptance:

1. `functions.hpatch_recover` has a dedicated grammar, is router-only, and accepts only `C... TARGET` lines.
2. Every command handle resolves against one immutable latest evaluated rejected script, and a command appears at most once per payload.
3. A successful rebuild is reevaluated as one complete ordinary HPATCH/2 script.
4. Re-rejection advances the baseline, emits refreshed target-command handles, and invalidates every prior handle; proxy rejection leaves the baseline unchanged.
5. Recovery cannot cross sessions or selected worktrees, and unrelated tools cannot become bases.
6. Replay restores `hpatch_recover` identity and the exact emitted short payload.
7. Ordinary mutation-leading hpatch scripts are never detected as recovery.
8. Per-attempt telemetry remains individual, while persistent metrics count every chain payload and one
   final or failed comparator.
9. One payload can correct multiple distinct command targets atomically without changing any other command field.
10. A target correction can retarget an anchored or unanchored mutation to exact multiline
    text with escaped LF; rebuilding preserves the target bytes and public control exclusions.
11. A target correction that denotes the retained target rejects before root reevaluation,
    including an explicit default occurrence count, an equivalent quoted escape spelling, or a
    range whose two endpoints are the retained single row; the retained baseline and handles remain usable.

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
empty. No target-bearing mutation is valid on a new file because no invocation baseline exists
for it. Further or dependent content changes require a successful invocation. A later invocation
may use exact authored current text as an unanchored literal target; a fresh read is required only
when exact current content is unknown or ambiguous.

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
in memory until the complete invocation crosses the apply or translation boundary.

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
baseline. A row first compares its four-digit hash with the exact content at its one-based
logical-line hint. If they differ or the hint is out of bounds, hpatch scans the same immutable
baseline and resolves the row only when exactly one line has that hash. No match is
`row-missing` when the hint is out of bounds and `row-stale` otherwise. Multiple matches are
`row-stale`; hpatch never chooses among duplicate content. The 16-bit hash retains an accepted
approximately 1-in-65,536 random false-acceptance residual for a candidate line.

If baseline resolution fails after earlier commands have pending edits, the evaluator may treat
the line number as a pending-content coordinate. This succeeds only when the pending line has the
supplied hash and its complete span maps back to exactly one unchanged immutable-baseline line.
Insertions at that baseline line's boundaries may shift the coordinate; an insertion or
replacement inside the line makes it ineligible. Content introduced or modified by a pending edit
does not become targetable. The resolved target remains the mapped baseline line, so conflict and
transaction semantics do not acquire a second editing baseline.

Both endpoints of a range must verify independently and remain ordered. An anchored text target
searches the verified baseline suffix. If its row is missing or stale, the row is redundant only
when the complete immutable baseline contains exactly the requested number of non-overlapping
literal matches; that exact set is selected. Extra or missing global matches preserve the row
failure. An unanchored text target searches the complete baseline, exactly as defined by
`REQ-SCRIPT-001`.
Pending edits never alter the baseline identity, literal search, matches, or target positions.
They may supply only the verified coordinate fallback above. Content introduced or modified by
any command is not targetable in that script. Dependent edits require successful application and
a later invocation. Exact authored current text may be used as an unanchored literal target
without hread; other introduced content requires fresh references.

Independently detectable row-missing, row-stale, occurrence-missing, and target-order failures
are collected across later commands whose active baselines can still be evaluated safely. The
transaction remains atomic. Dependency-sensitive lifecycle, conflict, and language failures
still stop at their authoritative boundary.

Resolution produces one nonempty baseline span for a line or range and one or more
nonempty spans for a text target. A mutation over multiple spans validates and registers
all of them or none.

Acceptance:

1. A copied hread row verifies complete content, including indentation, at its line hint or at
   one uniquely matching relocated row.
2. Missing and changed rows reject without choosing an unverified substitute. Duplicate baseline
   rows reject unless the supplied pending coordinate maps to one unchanged baseline row.
3. Inclusive ranges resolve both endpoints independently and reject reversed resolved order.
4. Text targets select the requested first N non-overlapping matches, including matches that
   span logical lines or end in LF, from the verified
   anchor or byte zero through EOF and reject incomplete multiplicity. A missing or stale anchor
   is ignored only when the literal's complete-baseline multiplicity equals N exactly.
5. Independent targets retain their original meaning after pending edits; introduced or modified
   content cannot be addressed within the same invocation. A later invocation may target exact
   known current text without a row. A post-edit coordinate may identify one
   unchanged baseline row shifted by earlier boundary insertions. A whole-file move preserves the
   moved file's existing baseline under its new logical path.

## REQ-EDIT-001 — Target-bearing mutations

`type TARGET VALUE` replaces every target span with the decoded value. An empty target-bearing
value deletes every target span, including a terminator owned by a complete-line or range
target. `add DESTINATION VALUE` inserts the value immediately before every line or text
destination span and preserves the destination. `add EOF VALUE` inserts once at the immutable
baseline EOF. A command with multiple text matches is atomic: resolution or conflict at any
match records none of its mutations.

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

1. Replacement, deletion, insertion before a line or text destination, and EOF append
   produce the specified result directly from their targets or destination.
2. Multi-match text mutation applies the same action to every requested match or none.
3. Disjoint edits are script-order independent except for deliberate insertions at the
   same boundary, which retain script order.
4. Overlapping destructive spans and insertions strictly inside them reject atomically;
   boundary insertions remain valid.
5. LF, CRLF, and standalone-CR complete-line replacement preserve the owned terminator
   for a nonempty value unless the value supplies one; an unterminated final line stays
   unterminated, while an empty value deletes any owned terminator.

## REQ-OUTPUT-001 — Output, final state, and failure behavior

Every root entry point accepts one complete input and evaluates the entire script before an
external filesystem commit or translated patch is returned. Basic `Apply` returns only an error;
basic `Translate` returns only patch bytes and an error. `ApplyForHost`, `ApplyForHostRoot`,
`TranslateForHost`, and `TranslateForHostAt` return `HostTranslation`, which carries the rendered
report, final state, diagnostics, patch summary, target aliases, and evaluator metrics. Before
finalization, every changed file whose final path ends in `.go`
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
An unchanged apply change set performs no filesystem operation and succeeds. An unchanged basic
translation returns an empty patch. A host variant additionally reports the already-satisfied
final state in `HostTranslation`.

Translation output contains file actions in deterministic first-touch order:

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
equal remove/add of the empty line representation for an empty file. Translation is fully rendered before it is returned.

After evaluation succeeds, host variants carry one fully rendered final-state report in
`HostTranslation`. Apply host variants return it only after commit succeeds; translation host
variants return it with the complete patch; routed `functions.hpatch` emits it through the
restored carrier. Basic `Apply` and `Translate` do not return the report. Its line forms are:

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
and may be used directly in the next invocation. An earlier row whose content is unchanged may
also be reused: its line is a hint and its hash relocates only when unique. The projection does
not guarantee every possible later target; when the exact target needed next is absent or
ambiguous, the caller obtains it with a focused hread. A row or range endpoint is never guessed
or reconstructed. An in-process successful host result also carries one structured target alias
for every effective nonempty `type` command whose authored target is a row or inclusive row range.
The alias maps that exact target and final path to the final rendered replacement extent after
language formatting. Deletions, insertions, text-occurrence targets, targetless initialization,
and ineffective commands produce no alias. Root APIs retain no target or editing state between invocations.

In routed mode, the router retains those aliases within the same session and workspace only after
a replayed carrier output exactly confirms the successful report. Before translating a later
script, it follows the aliases in retained call order. A failed, missing, or altered carrier
output confirms nothing. For rejected parseable line and inclusive-range commands, the same
rewrite boundary classifies only the emitted row-coordinate span relative to confirmed same-path
alias targets as `none`, `exact`, `contains`, `contained`, or `overlap`; it does not change target
rewriting or evaluation.

For host variants, the complete report is rendered before commit or patch return. Apply host
variants return it only after the external effect succeeds; router emission is auxiliary and
cannot retroactively change or roll back a successful effect. Basic `Apply` and `Translate`
discard the host-only report and structured state at their public boundary.

Root application stages new contents in same-directory temporary files before starting the commit. Parse, validation, read, and evaluation failures leave the initial tree unchanged.
A staging failure attempts to remove all temporary artifacts; cleanup failure returns
nonzero and identifies every artifact it could not remove. Commit-time filesystem failures
trigger rollback attempts using staged backups. Ordinary filesystems cannot provide a
portable crash-atomic transaction over multiple paths: termination, machine failure, or
rollback failure during commit can leave a partial change set. Such a failure must return
nonzero and name the affected paths; it must never report success or claim rollback
succeeded when it did not. Existing file permission bits are preserved; files created by
`new` use mode `0644`.

OpenAI `apply_patch` is a logical-line format and cannot preserve CRLF or standalone-CR
bytes when its output is applied by the tool. Translation therefore returns LF-only patch text and normalizes line endings only in its displayed before/after lines. It does not modify source files. Root application continues to preserve existing line endings outside explicitly inserted strings. Applying translated output to a non-LF file may normalize
that file to LF; this is a declared format limitation, not byte equivalence.

Basic `Apply` and `Translate` return errors for failures. Host variants place generic diagnostics
and structured failure data in `HostTranslation`; rendered generic diagnostics use the `hpatch:`
prefix. Command failures have the stable rendered form:

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
Failures return no completed patch. Basic entry points return an error; host variants return
`HostTranslation` diagnostics without a successful final-state report. Malformed row syntax
receives a syntax diagnostic.

A stale row reports the actual current-line candidate and up to two neighboring baseline rows.
It also reports every baseline line whose hash makes the stale reference ambiguous, or states
that the hash is absent. A unique relocated hash resolves during evaluation and does not produce
a diagnostic. Range repair reports start and end independently. When both requested coordinates
are in bounds and ordered, it also renders one explicitly unverified current-coordinate range
candidate in exact target syntax with its inclusive span length; normal endpoint verification
remains authoritative. A missing literal occurrence
reports the verified anchor context. An edit conflict identifies the prior command and affected
immutable-baseline lines. If
a command depends on content introduced by another command, the diagnostic directs the agent to
apply the prerequisite independently, reread, and submit a later invocation. A missing row or
failure without a verified baseline does not choose repair context. Repair context is
supplementary: it never changes the host outcome, mutation, returned patch, or metrics classification.
When invalid generated source is localized to a fixed-heredoc mutation, each distinct rejection
identity includes the non-sensitive `value_line`. Transient root diagnostics describe every
bounded value-row context rather than mutation addresses. Routed target-only recovery diagnostics
add current hashed `C...` handles only when every rejection is `row-stale`; other failures expose
no recovery handle under `REQ-CORRECT-001`.

The public host result separates lifecycle `Outcome`, requested `Change`, routed `Attempt`,
actionable `Failures`, durable-safe `Rejections`, and `PatchSummary`. A valid no-op returns
`evaluated/already-satisfied`, sets `Change.AlreadySatisfied`, and has an empty patch. Failure
scope is `field-local`, `multi-command`, `new-script`, or `new-transaction`; suggestions contain
bounded existing repair context rather than inventing new validation rules.

Acceptance:

1. Basic `Apply` returns only an error after commit and basic `Translate` returns only the complete
   patch bytes or an error without mutation. Their host variants return `HostTranslation`, including
   the rendered final- or pending-state report. An already-satisfied translation succeeds with an
   empty patch; the host variant also returns the rendered already-satisfied state.
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
   staging failure, translation failure, and cancellation produce no mutation, returned patch, or final-state report.
6. Injected external filesystem commit and rollback failures are reported without false
   atomicity claims and without a successful final-state report.
7. Failure to emit a fully rendered routed report after a successful external effect does not reverse that effect or record a complete report-input token estimate.
8. Stale rows, incomplete literal targets, and edit conflicts emit verified repair context;
   a missing row fails without guessing, and a failure with no active baseline emits its
   diagnostic alone.
9. Invalid Go localized inside a fixed `<<PATCH` value reports its physical body row in
   bounded repair context and structured host rejection identity without retaining body text.
10. One syntax-validation rejection includes every distinct actionable repair location from
    all changed files, groups visible diagnostics once per originating command and path,
    deduplicates parser cascades by repair row, and exposes enough current rejected-script rows
    for one atomic recovery payload to repair all locations.

## REQ-MENTOR-001 — Spawned-subagent Mentor Handoff

Mentor Handoff is an opt-in Hpatch-mode model schedule. Passthrough mode rejects the option. Its
first incremental form recognizes only an AgentControl thread spawn carrying exactly one
`x-openai-subagent: collab_spawn` header and valid Codex turn metadata whose `subagent_kind` is
`thread_spawn`. The router forwards the subagent header unchanged. It does not infer a subagent from
thread lineage, thread-source classification, a new routing session, a fork, compaction, or
instructions. Requests outside this exact boundary remain unchanged.

For a recognized child request whose configured model is exactly `gpt-5.6-luna` or
`gpt-5.6-terra`, the router replaces only the top-level request model with `gpt-5.6-sol` and the
reasoning effort with `high`, preserving other reasoning members, input history, tools, metadata,
and request fields. This happens before Hpatch projection, CTP preparation, provider serialization,
and actual-model metrics attribution. Codex continues to construct later requests from its session
settings; the router never rewrites response model metadata.

The router retains one bounded schedule per child thread. Completed provider responses contribute
one count for each `custom_tool_call` or `function_call` output item and each assistant `message`
output item. Streaming counts `response.output_item.done`; a terminal response output is only a
fallback when no done item was observed. Provider-reported `input_tokens` contribute on completed
and failed delivery paths whenever usage was observed. Tool calls and messages contribute only for
a completed terminal response.

After the mentor reaches three tool calls, it remains active for one more completed response so it
consumes the third call's result. A failed response in that position retains the mentor unless the
input budget is reached. The handoff completes after that result-consuming response, after two
assistant messages, or when the latest request reports at least 50,000 input tokens. A request's
input count already includes its inherited conversation history, so counts from separate requests
are never summed. The completed request may overshoot the token limit.
The next request from that child uses the model and reasoning supplied by Codex without a compatibility
rewrite. At most 256 child schedules are retained for the router lifetime; capacity
rejects another new eligible child before provider forwarding so a completed schedule is never
silently forgotten and restarted. State and progress logs retain counts and identifiers only, not
prompt or response content. Metrics charge every request to the model actually sent upstream.

## REQ-GUIDE-001 — Agent guidance

`contrib/codex/file-editing-instructions.md` is the single Codex source for CTP/2 representation
rules and all durable edit, shell, read, search, and inspection guidance.
`doc/spec/interface.md` owns the normative engine and router contract. Model-visible tool descriptions contain only concise
call-local contracts and request-specific schemas. The router does not use private tool
descriptions as prompt text. Native model protocol injects the central source without its leading
CTP/2 section and stops after the ordinary guidance rewrite; CTP/2 injects the complete source and
then transforms only eligible model-visible strings under `REQ-CTP-001`.

For each eligible turn carrying a non-null Responses `instructions` string, the router refreshes
one current marked hpatch section or replaces the pinned stock Codex file-editing section and its
displaced rg and exec-command lines. It preserves all unrelated instruction content. At startup,
the router reads `$CODEX_HOME/config.toml`, falling back to `~/.codex/config.toml`, only to
snapshot whether the top-level `model_instructions_file` key is set. A configured custom prompt
without either recognized section receives the central guidance by append; without that setting,
the request fails before upstream forwarding as an unsupported upstream instruction change.
Missing and null `instructions` values remain unchanged. This request-local behavior covers
session start, post-compaction, subagent start, and subagent post-compaction instruction delivery;
an inherited side conversation refreshes the marked section already in its prompt. Neither
`make install`, `make uninstall`, nor the router creates, changes, or removes an instruction file.

The recovery template adjacent to the central source owns dynamic recovery prose. After each
wholly row-stale evaluator rejection, the router supplies only the current handles and summaries
for rejected target-bearing commands. Other evaluator rejections direct the model to one complete
ordinary script. A re-rejected recovery states that prior handles are stale and refreshes the
listed commands from the latest evaluated script.

Persistent guidance teaches this workflow:

1. Submit a shell call as one free-form script without an outer wrapper. Use Bash by default or
   select another interpreter with a direct compact shebang. Keep program input on standard input,
   use exactly one `{.}` in `#!cmd=`, place request-specific outer arguments in `#!params=`, and
   use native session facilities for PTY-backed or long-running executions.
2. Inspect, edit, or rerun a retained shell script through its `@shell/` reference, and never mix
   retained and workspace paths in one hpatch script.
3. Acquire target-bearing context once before editing. When a known identifier or literal is
   likely to become a target, use hgrep first with
   repeated fixed-string patterns, adding bounded context options when surrounding code is needed.
   Every emitted match or context row is target-bearing. When the owner is known but the location
   is not, use inspect_file for structure or hgrep for a symbol, then hread only the smallest range
   needed to obtain the target. Use hsymbol refs for exact Go references and hsymbol def for an
   editable Go declaration after obtaining a verified selector row. Avoid whole-file hread unless
   the complete file is necessary.
4. Run one hread command per file and batch only already-known reads in one shell script. Copy
   only current emitted references. Do not follow target-bearing hgrep output with hread unless
   nonmatching context outside the requested bounds is needed.
5. Choose a line, inclusive range, or anchored literal target inside the mutation command.
6. Submit every known related edit in one atomic script. Split only when a later edit depends on
   validation or information unavailable before the current call. Keep unrelated large values
   in separate failure-domain calls.
7. Prefer the smallest mutation and let hpatch formatting own formatting. After success, do not
   hread, hgrep, hsymbol, or run `git diff` on a changed file or a directory containing one merely to
   inspect, verify, or locate a follow-up target. Reuse the exact authored value, unchanged rows,
   and any
   exact pre-edit row or range covered by a confirmed routed `reuse` mapping. Use a returned
   final-state row or exact unanchored current text for other changed content; acquire only a
   target that none of these forms identifies. Use a fixed heredoc for regular expressions and
   other escape-heavy source.
8. Use nonempty `type` to replace and empty target-bearing `type` to delete. Use `add` to
   insert before a line or text destination and `add EOF` to append. Use inline values for
   short text and `<<PATCH` for multiline or escape-heavy values.
9. After a wholly row-stale routed rejection, use `functions.hpatch_recover` with one current
   `C... TARGET` line per listed command. Submit every listed target correction in one atomic
   payload. Use one complete ordinary script for non-target or mixed corrections. After
   re-rejection, discard all prior handles. Ordinary `functions.hpatch` and root APIs have no
   recovery mode.
10. Let hpatch format changed Go files and syntax-check supported changed Python, JavaScript, and
    TypeScript files.

Acceptance:

1. A model can choose and encode every HPATCH/2 operation from the persistent guidance.
2. The forwarded prompt contains the selected central guidance exactly once and omits the pinned
   stock apply_patch, rg, and exec_command instructions. Native omits the CTP/2 section; CTP/2 retains
   it.
3. A marked prompt retains content before and after the owned section and refreshes idempotently;
   a configured custom prompt without a recognized section retains its content before the append.
4. Missing and null request instructions remain byte-equivalent. An unconfigured, unrecognized
   non-null instruction string fails before forwarding. CTP/2 never creates or encodes its selected
   instruction carrier, and `ctp1` fails before router startup.
5. Dynamic rejected-script references and recovery prose appear only with actionable context.
6. A wholly row-stale evaluator rejection lists only the rejected target-bearing command handles
   and exact guidance for one atomic target-correction payload. Other failures direct one complete
   ordinary script; re-rejection explicitly invalidates prior handles.
7. A routed success can be followed by another hpatch call using an exact row from its report
   without an intervening hread; a saved pre-edit row still rejects as stale.
