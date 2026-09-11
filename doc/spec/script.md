# HPATCH/2 script grammar

## REQ-SCRIPT-001 — HPATCH/2 script grammar

Outside a multiline value body, blank lines are ignored and every other physical line begins exactly
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

`VALUE` is a JSON-compatible quoted string, a raw heredoc headed by `<<PATCH` or
`<<PATCH-`, or a line-framed text block headed by `<<TEXT` or `<<TEXT-`.
Raw heredocs close with `PATCH`; text blocks close with `TEXT`.
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

No escape, interpolation, dedent, or delimiter substitution occurs. For `<<PATCH`, payload
bytes begin after the header terminator and end before the closing delimiter. Every body
line contributes its physical terminator. `<<PATCH-` removes exactly the final body line's
terminator (LF or CRLF) from that same payload. An empty body remains empty; one empty body
line also decodes to empty in this mode. Other bytes, including trailing spaces, interior
terminators, and earlier blank lines, remain unchanged. Validation and the body-size limit
apply to the original body before removing its final terminator.

The mode is independent of the operation and target. Whole-line replacement still applies
the terminator-preservation rule in `REQ-EDIT-001` after decoding the value. Literal targets
own only their exact matched bytes; neither mode consumes adjacent baseline whitespace.

The header, body, and delimiter are one command attributed to the header. An exact `PATCH`
payload line uses a line-framed text block instead. Unterminated or oversized heredocs fail
as one bounded header-owned syntax error.

### Line-framed text blocks

A text block requires one leading `|` on every physical payload line. Decoding removes
exactly that first bar and preserves the rest, including existing leading bars, tabs,
spaces, Unicode, and physical LF/CRLF terminators. An empty payload line is `|`.
The unprefixed line `TEXT` closes the block. No payload content is reserved:
`|PATCH`, `|TEXT`, and `|type <<TEXT` are ordinary data, as are examples of this
representation itself.

```text
type "old example" <<TEXT-
|type <<PATCH
|replacement
|PATCH
|type <<TEXT-
||text
|TEXT
TEXT
```

`<<TEXT` keeps every decoded body terminator; `<<TEXT-` removes exactly the final
body terminator, with the same empty-body and target-ownership semantics as raw
heredocs. UTF-8 validation and the 1 MiB body limit apply to decoded payload bytes
before removing that terminator; transport bars do not count toward the body limit.
Existing whole-script transport limits still apply.

A body line missing its bar, other than the exact closing delimiter, fails at the
header with the offending physical line number. The malformed frame owns the
remaining input, preventing payload-shaped commands from being reinterpreted.
Missing closes, invalid UTF-8, and oversized bodies reject the entire script
before mutation, just as raw heredoc failures do.

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
4. JSON-compatible values, raw heredocs, and line-framed text blocks reproduce their
   exact decoded payloads without parsing body lines as commands. Minus modes remove
   exactly one final body terminator independently of target shape, preserving all
   other bytes. Text blocks support literal closing delimiters, opener lines, and
   examples of their own framing; a missing payload bar rejects atomically.
5. Invalid rows, ranges, counts, strings, heredocs, operands, and commands fail before
   filesystem mutation, patch output, or final-state reporting.
6. File and mutation commands may be interleaved while all targets retain the immutable
   baseline meaning defined by `REQ-SELECT-001`.
7. For root-scoped evaluation with root `/workspace` and cwd `bin/worktree`, path `main.go` denotes `/workspace/bin/worktree/main.go` and translates as `bin/worktree/main.go`.
