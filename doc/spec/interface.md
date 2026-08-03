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
`hpatch`. Its grammar-constrained free-form input is a JSON string containing `PATH`,
optionally followed by a space and `START:END` for an inclusive bounded logical-line
range, for example `"editor.go" 40:80`. Line endpoints are positive one-based base-ten
integers and must be ordered. The start line must exist. An end line past EOF returns
through the final line, while invalid syntax, a missing endpoint, a reversed range, or a
start line past EOF fails.

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
from the exec process's current directory; absolute paths retain their ordinary
filesystem meaning. Codex, not the router or hread, owns the sandbox and filesystem
permissions. The worker accepts only regular UTF-8 files and never mutates them. It emits
each requested logical line as:

```text
LINE:HASH TEXT
```

`LINE` is the positive one-based logical line number. `TEXT` is exact logical-line
content without its terminator. `HASH` is lowercase hexadecimal for the first two bytes
of SHA-256 over that exact content, including leading spaces and tabs. A trailing file
terminator does not create an additional empty line. The router does not execute the
read, duplicate its filesystem rules, or encode its output in an `apply_patch`-shaped
carrier.

Acceptance:

1. Whole-file and bounded reads emit exact UTF-8 content in source order with deterministic
   `LINE:HASH` references that can be copied directly into mutation targets. Equal lines
   at different positions have distinct row references, and indentation changes the hash.
2. A missing, inaccessible, non-regular, non-UTF-8, malformed-range, reversed-range, or
   start-past-EOF input returns a read diagnostic without mutation; an end past EOF
   succeeds through the final line.
3. The router restores a replayed `hread` call to the original custom-tool shape just as
   it restores routed hpatch calls, without treating reads as editable correction history.
4. Reading and whole-file UTF-8 validation use bounded streaming storage, observe
   cancellation during processing, and reject before formatted output exceeds 16 MiB.
5. Success returns the worker's exact stdout through a successful Code Mode exec call;
   read failures return concise stderr and nonzero shell status without incrementing
   hpatch edit-failure counters.
6. Turns and retained-session eviction do not create or own wrapper state. Router
   shutdown removes the process wrapper.

## REQ-METRICS-001 — Persistent token, command, target, and failure metrics

Every recognized normal or translate invocation is classified after its terminal outcome.
A successful nonempty change set that parses, evaluates, translates, and completes its
requested output or mutation contributes paired estimates for two semantically equivalent
tool calls. A failed invocation contributes only its generated `hpatch` call estimate to
the ineffective-output counter; it contributes nothing to the effective `hpatch` counter.
A failed routed invocation is represented downstream by an exec carrier that returns its
diagnostic and repair context. Its comparison baseline is the fixed direct-call program
carrying `*** Begin Patch\n*** End Patch\n`; that tokenized semantic baseline contributes
to the failed `apply_patch` output counter. The diagnostic carrier itself never counts as
`apply_patch` output. The complete failed hpatch call remains in the ineffective-output
counter and reduces the overall output savings. `gain`, informational commands, and
unsupported argument forms do not contribute metrics.

Both effective and ineffective hpatch estimates count the `functions.hpatch` tool name
followed by the editing payload the model emitted. When a correction names commands of a
rejected script, the shorter correction is charged while the rebuilt complete script is
used only for evaluation. The successful direct side counts the `functions.exec` tool name
and a fixed free-form program that passes the complete translated patch envelope,
serialized as one string argument, to `tools.apply_patch`, then returns that nested tool's
result. The router-only marker and hpatch final-state report are excluded from this semantic
baseline. All estimates use the tokenizer library's GPT-5 model mapping. Script and patch
text remain data and cannot alter the fixed direct-call program.

A final-state report successfully emitted by normal or translate mode contributes its
exact rendered text to a separate estimated state-report input-token counter. This is
model-input overhead because the tool result becomes subsequent model context; it is not
added to either model-output counter.

The host tool definition is also model input. The router obtains the session identity, the
serialized hpatch and hread custom grammar objects installed in the routed request, and
the displaced native patch definition directly from that request. The first classified
request of a session counts those definitions once;
subsequent requests in the same session add nothing because the resent definition is
served from the provider's prompt cache. A host that supplies no session or definition
text leaves these counters at zero, and gain states which inputs were measured so a zero
is not read as a free tool. A failed or cancelled invocation emits no report
and contributes zero report-input tokens. A partial or failed report write does not count
as a complete emitted report. Other tool results, provider-hidden protocol and reasoning
tokens, assistant commentary, and server-generated identifiers remain excluded. These
are reproducible estimates rather than authoritative API usage.

Routed hread results do not add a synthetic gain estimate and do not subtract a
hypothetical raw `cat` result. The router's end-to-end Responses and per-session usage
totals are authoritative for model input consumed after the exact exec result is
returned.

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
in  new  mv  rm  type  type-  type+  del
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
directory returned by Go's `os.UserConfigDir`. Updates hold an exclusive interprocess
lock at `hpatch/metrics.lock`; gain reads hold a shared lock. The metrics file contains
two alternating current-version fixed-size slots holding token, command, target,
error-reason counters, a persistence generation, and a checksum. A reader uses the
valid current-format slot with the greatest persistence generation, so an interrupted
write to the inactive slot leaves the preceding aggregate available. The file does not
grow after its current-version slots are created. Per-counter and aggregate overflow fails.

Only the latest metrics magic is decoded. A complete, checksummed slot whose eight-byte
magic starts with `HPATCH` but does not equal the current version resets the reported
totals to zero. A malformed slot, including a mismatched version with an invalid checksum,
does not qualify for reset. When a current-format slot is also valid, its totals take
precedence over mismatched-version slots. Other invalid data fails rather than producing
a misleading report. Metrics writes use normal operating-system page-cache writeback and
do not request a per-invocation filesystem sync; sudden power loss may lose increments
that the operating system had not yet flushed.

`hpatch gain` first writes an output-token table comparing successful calls, failed
calls, and all calls. The successful row compares effective hpatch output with the
generated `apply_patch` output and reports its reduction. The failed row compares
ineffective hpatch output with the fixed direct-call program carrying the empty patch
semantic baseline. The downstream diagnostic carrier is excluded. The all-calls row
reports both totals and the overall output-token reduction.

Effective-only reduction compares effective hpatch output against generated `apply_patch`
output. Overall reduction is `(effective_apply_patch + failed_apply_patch - effective_hpatch
- ineffective_hpatch) / (effective_apply_patch + failed_apply_patch) * 100`. It is zero
when the `apply_patch` denominator is zero. No retry payload is inferred.

Gain then writes a separate input-token table for final-state reports, failure diagnostics,
and tool definitions. Hpatch and `apply_patch` values remain separate:
the report does not subtract definitions, convert input to output, or calculate a combined
input/output percentage. Unmeasured `apply_patch` input sources are labeled `not measured`.

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
   estimates and fully emitted report-input estimates; failed invocations persist only
   ineffective hpatch estimates and zero report-input tokens.
2. Gain reports output and input token classes in separate tables, calculates reductions
   only between output-token quantities, and performs no input/output price conversion.
3. The eight supported command counters and four target counters reconcile with aggregate
   command attempts and errors. No selector, clipboard, editor-generation, or script-level
   commit counter remains.
4. Every definition-bearing request increments the definition-request counter, while the
   serialized installed hpatch and hread grammar objects and the baseline definition
   accumulate only once per distinct session. An absent session or definition leaves
   definition counters zero and reports which inputs were measured.
5. Failed hpatch invocations contribute their complete output to the ineffective counter;
   the failed `apply_patch` counter receives the fixed direct-call program carrying the
   empty patch envelope, while the downstream diagnostic carrier is excluded.
6. A correction is charged as the shorter payload the model emitted for both effective and
   ineffective invocations while evaluation uses the rebuilt complete script.
7. Scripts and patches containing quotes or program-like text remain data and cannot
   alter the direct-call program used for counting.
8. Concurrent writers lose no records, concurrent gain reads never observe a partial
   record, and a damaged inactive slot falls back to the preceding valid aggregate.
9. A valid mismatched `HPATCH` version resets totals when no current slot exists;
   malformed data does not count as a version mismatch, and a current slot takes
   precedence over mismatched versions.
10. Metrics collection failure warns without changing the success or failure of the
    requested edit, translated output, or final-state report.
11. Router snapshots attribute successful and rejected hpatch translations, diagnostic token
    totals, at most the latest 128 correction-aware attempt identities, and at most the latest 32 structured
    evaluator rejection identities to their request sessions without persisting scripts,
    replacement text, diagnostics, repair context, or new per-session records in `metrics.bin`;
    per-session text-byte limits may retain fewer identities.

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
del TARGET
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

1. Every accepted nonblank command is one of the eight public commands; `tsel`, `rsel`,
   `copy`, `cut`, `paste`, and script-level `commit` are syntax errors.
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

`type TARGET VALUE` replaces every target span with the decoded value. `type- TARGET
VALUE` inserts the value immediately before every span and preserves the target.
`type+ TARGET VALUE` inserts immediately after every span and preserves the target.
`del TARGET` replaces every span with empty content. A command with multiple text matches
is atomic: resolution or conflict at any match records none of its mutations.

Replacements and deletions must have disjoint baseline interiors. An insertion strictly
inside a replacement or deletion conflicts. Insertions exactly at either boundary are
permitted. Multiple insertions at the same baseline boundary are permitted and render in
script command order. Conflicts identify the prior command and affected baseline range;
they reject the complete script before filesystem mutation or patch output.

For a complete-line or range replacement whose target owns a final LF, CRLF, or
standalone-CR terminator, `type` preserves that exact final terminator when the replacement
does not end in a terminator. A replacement-supplied final terminator is authoritative
and is not doubled. No terminator is synthesized for an unterminated selected final line.
`del` removes owned terminators. Inserted values are otherwise byte-exact decoded UTF-8.
Existing line endings outside explicit inserted or replaced text remain unchanged.

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
   unless the value supplies one; an unterminated final line stays unterminated.

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
After a rejection, the router supplies compact indexed correction syntax only when a
retained rejected script is actually correctable.

Both references teach this workflow:

1. Use search to locate relevant regions, then use hread for the first content read of a
   region likely to be edited; issue independent hread calls together and copy complete
   `LINE:HASH` references only from current output for that exact path.
2. Choose a line, inclusive range, or anchored literal target inside the mutation command.
3. Batch short, disjoint edits across inspected files when they are expected to validate or
   fail together. Keep unrelated large `<<PATCH` values in separate failure-domain calls,
   with at most one syntax-sensitive multiline Go declaration or function replacement per
   call; short supporting edits for that same change may remain with it.
4. For an existing Go declaration or function, prefer one range `type` instead of assembling
   the same replacement through several insertions. After success touches a file, discard its
   saved references and hread it again before another edit.
5. Use `type` to replace, `type-` to insert before, `type+` to insert after, and `del` to
   delete; do not construct a separate selection or clipboard program.
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
