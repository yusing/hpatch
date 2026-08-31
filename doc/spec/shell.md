# Installable free-form script tool

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
`cmd`. A present `login` value must be exactly `false`. Within the leading directive block, the
tool tolerates `# !params JSON` and `#!params JSON` as alternate spellings and applies the same
params validation. A duplicate directive, malformed JSON, non-object JSON, unsupported
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
    helper and create no hread, hgrep, hsymbol, or inspect_file basename frontend.
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
    interpreter-wrapper or heredoc kind rather than stopping after the first. Recovered Code Mode
    JavaScript emits its recovery warning first and then every detected nested shell warning.
    Warning insertion preserves the exact submitted command, carrier result, replay behavior, and
    metric classification.
