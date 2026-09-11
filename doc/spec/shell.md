# Installable free-form script tool

## REQ-SHELL-001 — Installable free-form script tool

The first working path in `doc/brief.md` § Outcome supplies the built-in declaration at
`plugins/shell.mjs`. The generated plugin bundle contributes an unconstrained custom tool named
`shell`, limits its UTF-8 input to the executor argv limit, and translates successful input
through the canonical exec carrier from `REQ-PLUGIN-001`. The repository `make install` target
regenerates that bundle and installs `mekugi` plus the fixed `shell` helper. It changes no Codex configuration,
instruction file, or configured shell declaration.

Before translating a built-in shell input, the router splits it into programs. After a program
body begins, a column-one interpreter selector or params directive starts a new program.
The canonical `#!params=` and tolerated params spellings are boundaries; a params-only header
selects default Bash. Each program has its own leading directive block. Duplicate params within
one block still reject. Later `#!cmd=` lines remain body data rather than boundaries.
Column-one boundary markers are reserved even inside language strings and heredocs; indented
markers remain body data. Splitting preserves every other body byte and line terminator.

Omitted params inherit the preceding complete object. A supplied object replaces that object,
including `{}` clearing inherited fields. Interpreters and command templates do not inherit.
All programs are parsed and translated before any carrier is emitted; invalid later programs
reject the entire batch without running its valid prefix. Batch programs require a nonempty
body. A retained reference remains a sole `#!script=` call, and its resolved input may be a batch.

Batches require Code Mode and are for noninteractive work. Agent guidance encourages batching
ready, independent, noninteractive programs and directs interactive programs to separate calls
so their prompts and native continuation handles remain available for input. The router prepares
separate native exec arguments for each program
before sending one ordered Code Mode carrier to Codex. Each native execution receives its own
params and separate shell state. The carrier awaits terminal native results, using the existing
continuation operation when needed, before starting the next program. Nonzero script exits do
not stop later programs. The result contains an ordered `results` array, each element preserving
one program's terminal native fields and concatenated output. A host error or refusal stops
remaining execution and propagates after publishing completed results and current partial output,
including any outstanding native continuation handle. No program is restarted or retried.
Native-only clients reject batches with a Code Mode requirement diagnostic before execution.
Batch retention selects the complete resolved batch, while replay restores the original call.
Eligible cat-write projection applies independently within each program.

The following interpreter and directive rules apply separately to each program.

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
or its quoting. For implicit default Bash without a command template, a body with at most one final line
terminator remains direct when it parses as one non-background, non-negated simple call whose
static command is neither a shell built-in, the reserved `commentary` command, nor a private
contribution and whose statement contains no command or process substitution. The direct carrier
removes that optional final line terminator and otherwise preserves the command text.

The `shell` transformation MUST NOT add flags to the rendered command. Only interpreter
arguments supplied by the input's selector may appear as interpreter flags. Router-owned
metadata, including commentary connection details and credentials, must travel through private
runtime plumbing, never through added command arguments or inline environment assignments.
Enabling commentary must not change whether an otherwise eligible command remains direct.
Shell commentary is thread-scoped. The worker discovers its private publisher through the
inherited `CODEX_THREAD_ID` and current thread runtime, without changing the interpreter argv.
Missing or unavailable commentary discovery must not prevent script execution.

After an optional interpreter shebang, a leading directive block can contain one `#!cmd=`
assignment and one `#!params=` assignment in either order. All canonical directives use
`#!key=value`. The tool trims ASCII spaces and tabs around each complete directive line. The
nonempty command value is a shell command template containing exactly one `{.}` placeholder.
The params value is a JSON object that cannot contain `cmd` because the script body supplies
`cmd`. A present `login` value must be exactly `false`. Within the leading directive block, the
tool tolerates `# !params JSON` and `#!params JSON` as alternate spellings and applies the same
params validation. A duplicate directive, malformed JSON, non-object JSON, unsupported
leading directive, params object containing `cmd`, or unsafe `login` value rejects.

The tool removes recognized directive lines and their complete line terminators from the body.
The router replaces `{.}` with the canonical independently quoted shell-helper command and argv.
The command template then runs through the normal exec carrier shell. Without an interpreter
shebang, the nested worker selects `bash`. Without an interpreter shebang or command template, an eligible simple external
Bash command remains direct, including when exec parameters are supplied; every other body uses
the worker command as the complete outer command. After the first body line, directive-like lines remain ordinary body data except for the reserved batch boundaries above.

When the worker carrier is selected, the executor starts the fixed helper once with the normalized
interpreter fields and exact body.
The helper reads the current thread runtime path and replaces itself with the authenticated
router worker, without a second Codex executor call. For Bash and sh basenames,
the worker parses the body with `mvdan/sh` using
`LangBash` or `LangPOSIX`, applies supported middle fields as shell options or parameters, and
executes the syntax in-process. Its exec handler receives expanded argv, invokes hcat, hgrep,
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
intermediate script file. Descriptor delivery is asynchronous so a streaming interpreter can
produce output before consuming the complete script without blocking output capture. On interpreter
output overflow, it bounds captured output and inherited-pipe cleanup, discards only an incomplete
trailing UTF-8 code point from a truncated output prefix,
and returns an explicit nonzero overflow result (also retaining malformed-UTF-8 diagnostics when
present). It asks the existing invocation process-group owner to terminate remaining descendants
on Unix. Successful background jobs and ordinary
cancellation keep their existing lifecycle; the interpreter does not enter a detached session.
Without `#!params=`, the worker inherits Codex's execution context.
With `#!params=`, Codex applies the accepted outer exec arguments before launching the worker.
The worker returns stdout, stderr, and exit status without copying the script body into either
output stream.

For the built-in Bash/sh tool, the router recognizes literal truncating writes of the form
`cat > PATH <<'EOF'` (either redirection order, single/double-quoted delimiter, and `<<-` tab
stripping). A simple sequence separated only by newlines or semicolons is lowered, in order,
to shell commands and native `apply_patch` calls in one Code Mode carrier. Heredoc contents are
parsed as data, never as statement separators. The native-tools carrier uses the executor's
`apply_patch` executable, as mekugi does. The original shell call, not the generated sequence,
is restored on provider replay; tool declarations, instructions, and the existing provider cache
prefix do not change for this projection.

Only empty or LF-terminated literal UTF-8 bodies are projected. The patch performs an unconditional
write, including overwriting an existing file, without reading an early baseline, formatting,
or source validation. Each write is applied only after its preceding commands finish. An
execution-time guard leaves missing parents, symlinks, and special-file targets to the original
cat command instead of giving `Add File` permission to create parents or replace special targets.
Ordinary command output remains ordered in the shell result; patch success and guard output do
not enter it. Host patch errors/refusals stop the carrier rather than retrying the write as cat.
Already captured ordinary output and retention metadata remain visible when a later tool fails;
the host error propagates without executing the remaining statements.

The complete script stays on its existing execution path if it contains conditionals, pipelines,
background jobs, compound statements, shell-state mutations (`cd`, assignments, functions,
options), or dynamic expansions. Append writes, file-copy forms, unquoted heredocs, and paths or
contents not representable without byte changes remain shell commands. Interpreter arguments,
command templates, PTYs, and exec parameters other than workdir, output budget, yield timing,
and false login/tty also keep the existing carrier. A known absolute workdir is required.

The ordinary single-program shell carrier forwards the complete native `exec_command` result defined by the owning Code
Mode contract rather than only its output field. A result containing the native continuation
handle remains yielded rather than terminal, and the same host-owned continuation operation
resumes that session. The router and shell plugin do not poll, resume, cancel, retry, replace, or
persist the session. They do not define a second result envelope or continuation protocol. Exact
result fields, yield timing, continuation arguments, and session lifetime remain owned by Codex's
executable tool definitions in that request.

For a split cat-write sequence, the enclosing Code Mode program waits for each command's
terminal result with the native `write_stdin` operation before starting the next step. The same
Code Mode cell may yield while this work is pending. No session is restarted or retried. It
concatenates command output in execution order and retains the last step's exit status and
terminal result fields, plus the existing retention metadata. Native patch success contributes
empty output and status zero. No generated patch or intermediate guard result is published to
the provider as a separate conversation item.

Eligible shell calls return `retained: true` and a thread-scoped `script_ref` shaped
`@shell/<artifact-id>`. `hcat` inspects that reference, mekugi edits it inside private
script storage, and a sole `#!script=@shell/<artifact-id>` reruns its current content.
References select regular UTF-8 script files, never arbitrary host paths or the runtime
launcher. Thread and artifact IDs must be single nonempty filename components, excluding
`.` and `..`, separators, and NUL; `.runtime` is reserved and cannot identify an artifact.
Missing, cyclic, traversing, and symlink-escaping references reject without execution.
Invalid retention IDs or existing artifact names yield `retained: false` without overwriting
files or changing execution of an otherwise valid shell call.

Thread runtime locators are flat `mekugi-runtime-<thread-id>` symlinks below the runtime
directory. The PATH-installed helper follows that name. Active retained scripts occupy sibling
`mekugi-scripts-<thread-id>` directories.
Private commentary descriptors are regular mode-0600 files beside the thread locators,
outside retained script storage. Discovery rejects symlinks, non-regular files, and descriptors
that do not match the worker selected by the current locator. Unexpected existing entries are
not overwritten, and missing or invalid descriptors disable commentary only.
One shared pinned parent capability anchors this namespace. Launcher preparation creates no
script storage and retains no per-thread directory handles. A retained-script directory is
created exclusively for each active storage lifetime; an unexpected existing directory or
symlink rejects retention rather than becoming application or cleanup authority. If the
initial capability open fails, preparation rolls back only the new empty directory entry;
nonempty or non-directory replacements are preserved, and a transient failure can be retried.

Artifacts expire after one hour. Live storage roots remain pinned while artifacts or router-side
read/edit leases exist. Expiry waits for those operations before deleting their artifacts.
Last expiry removes the owned script directory through its live capability and closes that
capability. A later retention starts with another exclusive directory creation; an idle session
never reopens a historical directory for editing or recursive cleanup. Reads, edits, expiry,
and cleanup remain confined when a storage pathname is replaced by an escaping symlink.
Launcher refresh remains independent of script-storage availability. Shutdown rejects new
leases, waits for existing operations, cancels expiry callbacks, cleans active owned storage,
removes only owned commentary descriptors, unlinks only flat locators still targeting this
router's worker, and closes the shared parent.
Replacement locator files or directories are never traversed or recursively removed. Reruns
retain the resolved script body while conversation replay preserves the original reference call.

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
   line terminator produces that direct native command without `shell bash`, with or without a
   params directive. Exec parameters remain on the outer carrier unchanged. Shell built-ins,
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
11. `make install` installs `mekugi` and the fixed `shell` helper without changing Codex
    configuration or instruction files. Startup and tool-snapshot changes do not rewrite that
    helper and create no hcat, hgrep, hsymbol, or inspect_file basename frontend.
12. `#!params={"workdir":"/tmp","tty":true}` before or after `#!cmd=` produces an exec carrier
    containing those fields and the router-supplied `cmd`. Tolerated leading params variants
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
    interpreter-wrapper or heredoc kind rather than stopping after the first. Warning insertion
    preserves the exact submitted command, carrier result, replay behavior, and metric classification.
    For default or explicitly selected Bash, the router parses the normalized body as Bash first.
    Valid Bash always retains shell semantics. A body that fails Bash parsing but parses as
    TypeScript (including JavaScript) is rejected with `shell-typescript-misuse` before execution,
    except for the established Code Mode recovery below.
    The result explains how to submit Code Mode helpers or choose an explicit script interpreter;
    rejected input is never automatically executed as Code Mode. Neither the script nor its
    command template runs. Headers and retained-script resolution use the normal translator;
    configured plugins, other interpreters, and bodies invalid in both languages retain their
    existing behavior. JSON, SSE, native, and Code Mode carriers deliver the same diagnostic,
    and replay retains the original shell call and its rejection result.
    With a Code Mode carrier available, the built-in shell recovers headerless JavaScript that
    fails Bash parsing, parses as JavaScript, and has syntax-tree references to the Code Mode
    runtime: a `tools` method call, `ALL_TOOLS`, or a direct `text`, `image`, `audio`, or
    `generatedImage` call. Formatting, comments, statement order, and awaiting style do not
    determine recovery. Strings, comments, and property names are not runtime references.
    A runtime name bound or assigned anywhere in the program is conservatively excluded from
    recovery evidence; local lookalikes must not be treated as Code Mode globals.
    Explicit interpreter selections, directives, and retained references never opt into recovery.
    Recovery preserves the exact program, adds `shell-code-mode-recovered` guidance followed
    by detected nested shell warnings, and replays the recovered Code Mode carrier. Guidance
    distinguishes shell commands, which belong directly in `functions.shell` without JavaScript
    wrappers, from other Code Mode helpers. Both direct Code Mode calls and recovered programs
    containing an unshadowed `tools.exec_command` call receive one `Use functions.shell` warning.
    Detection uses JavaScript syntax, not the result variable, formatting, or Promise batching;
    strings, comments, unrelated helpers, and locally bound `tools` are not evidence of misuse.
    Warning insertion preserves execution, results, leading pragmas, and directive prologues.
    If the program locally binds or assigns the `text` output helper, leave its source unchanged
    and append the warnings as a separate text part of the model-visible tool result. Original
    result text and multimodal parts remain intact. This projection is idempotent and retained
    across fresh-process resume and forks, independently of commentary configuration; diagnostics
    are not evaluated in the submitted program or substituted for its output.
    Native-only
    requests and other misplaced JavaScript/TypeScript use the rejection behavior above.
17. Retain, read, edit, and rerun preserve the script body and original model-visible call.
    Unsafe thread IDs reject before runtime creation; unsafe artifact IDs cannot redirect
    retention, reads, edits, expiry, or cleanup. A retained script cannot read or overwrite
    the runtime launcher, another thread's scripts, or workspace files through a reference.
18. `foo; cat > out <<'EOF'` followed by a literal body, delimiter, and `bar` runs foo, the
    user-visible native patch, and bar in that order. Newline-only separators behave identically.
    Multiple writes to one file observe execution order, not a pre-execution filesystem snapshot.
    Quoted dollar signs, semicolons, blank lines, and final LF bytes remain literal file content.
19. Complex shell constructs remain unsplit. A yielded prefix finishes before any patch or suffix
    begins. JSON and SSE projections restore the exact original shell call and unchanged result
    on replay, and native/compact provider cache diagnostics retain an appended prefix.
20. With commentary enabled, `mktemp -d -t mekugi-shell.XXXXXXXXXX` remains the direct command.
    Wrapped Bash and POSIX scripts retain only their normalized interpreter fields and quoted
    body; transformation adds no flags, connection details, credentials, or inline environment
    assignments. Thread-scoped commentary discovery preserves script output and exit status,
    and completion of one worker leaves concurrent workers' commentary available.

21. One input containing a params-prefixed Bash body, a Python selector and body with omitted
    params, and another params-prefixed Bash body yields one Code Mode carrier with three ordered
    executions. Python inherits the first params object; the third program uses only its newly
    supplied object. Multiple implicit Bash programs require no `#!bash`. Per-program bodies
    preserve CR, LF, CRLF, whitespace, and absent final terminators.
22. A yielded program reaches terminal state before the next starts. A nonzero exit remains
    visible in its result and does not prevent later programs. Complete native result fields and
    output remain associated with their program. Host exceptions preserve the completed prefix
    and current partial output without running the suffix.
23. Malformed or unsafe later headers, empty batch programs, and native-only batches reject
    before any program executes. JSON and SSE carry one replayable call, and retained batch
    reruns reapply splitting and params inheritance to the current retained source.
