HPATCH/2 applies one complete target-bearing edit script atomically. Do not call this tool
in parallel with other tools. Rejection or cancellation changes nothing.
Reason carefully about the complete script and make sure it obeys the grammar before calling this tool.

Commands:

```text
in PATH
new PATH
mv PATH
rm
type TARGET VALUE
type- TARGET VALUE
type+ TARGET VALUE
```

Targets:

```text
LINE:HASH                         complete logical line
LINE:HASH..LINE:HASH              inclusive complete-line range
LINE:HASH "TEXT" [N]              first N exact matches from that row through EOF
```

`type` replaces. An empty target-bearing `type` value deletes every target span, including
terminators owned by line and range targets. `type-` inserts before while preserving the
target; `type+` inserts after while preserving it. A text target defaults to one match;
every requested non-overlapping match must exist or the script rejects.

Use inline JSON-compatible strings for short or single-line values. Include `\n` when a
before/after insertion must form a complete new line:

```text
in parser.go
type- 37:8c2f "// parseCommand parses one physical script line.\n"
```

Use the fixed `<<PATCH` frame for multiline or escape-heavy values:

```text
in service.go
type 20:2ff7..28:d10b <<PATCH
func calculateResult(input Input) (Result, error) {
	return computeFreshResult(input), nil
}
PATCH
```

An unindented heredoc body line that begins with `type `, `type- `, or `type+ ` and
then contains only `<<PATCH` or ends with ` <<PATCH` is reserved as a nested opener. Close the
current frame first; use an inline value or indent literal HPATCH examples.

Create a file with at most one immediately following targetless initializer:

```text
new internal/target.go
type <<PATCH
package internal
PATCH
```

Every existing file has one immutable baseline for the complete invocation. Pending edits
do not shift later targets. When inspected files are ready, submit every known related edit
in one atomic script, including related multiline declarations and repeated `in PATH`
sections. Split only when a later edit depends on validation or information unavailable
before the current call. Keep unrelated large `<<PATCH` values in separate failure-domain
calls. Prefer the smallest mutation that expresses the semantic change. When a formatter
owns formatting, alignment, or indentation, do not replace surrounding lines merely to
reproduce its output; let the formatter apply those changes. For example, add one struct
field with one insertion rather than replacing the declaration. Preserve required
indentation prefixes in indentation-sensitive languages such as Python.
Content introduced by a mutation is not targetable in the same call. Successful final-state
`LINE:HASH` rows are current references for their named final paths and may be used directly
in the next invocation.

Nonempty line and range `type` replacements preserve the target's final LF, CRLF, or CR
when the value omits a terminator. Explicit terminators are authoritative. An empty
target-bearing `type` value removes owned terminators. `type-` and `type+` insert
byte-exact values and do not synthesize newlines.

Overlapping replacements/deletions and insertions strictly inside them reject. Boundary
insertions are valid. Multiple insertions at the same boundary render in script order.

Changed Go files are parsed and formatted before success; do not run redundant `gofmt`.
Relative paths use the selected base directory when available; without one, relative paths reject; parents for `new` or `mv` must exist.
After rejection, use the router's indexed command or multiline-value-row correction only
when the rows still belong to the same baseline; reread stale rows instead of guessing.
