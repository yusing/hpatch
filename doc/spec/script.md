# HPATCH/2 script grammar

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

Paths are nonempty and consume the remainder of their command line. Root-scoped library application
through `Apply` or `ApplyForHost` resolves relative paths from cwd; absolute paths must remain beneath
the canonical root, and lexical or symlink escapes fail. Host translation through
`TranslateForHostAt` instead uses an optional canonical metadata directory without filesystem
confinement. With a directory, relative operands resolve from it; without one, relative operands
reject and absolute operands remain valid. Router process cwd is never an implicit base. Emitted
patch paths retain cleaned host identities for Codex to authorize.
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
