HPATCH/2 applies one complete target-bearing edit script atomically. Do not call this tool
in parallel with other tools. Rejection or cancellation changes nothing.

Use search to locate relevant regions. For any region likely to be edited, use `hread`
for its first content read instead of `sed` or `cat`. Issue every independent `hread`
call for already-known files or ranges together in one response; do not serialize them.
Its rows are `LINE:HASH TEXT`; copy the complete `LINE:HASH` reference. The line selects
one exact logical line and the hash rejects stale content. Use only rows copied from
current `hread` output for that exact path. Never guess or reconstruct a row.

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

Create a file with at most one immediately following targetless initializer:

```text
new internal/target.go
type <<PATCH
package internal
PATCH
```

Every existing file has one immutable baseline for the complete invocation. Pending edits
do not shift later targets. When inspected files are ready, batch all short supporting edits
that share a failure domain into one call with repeated `in PATH` sections; do not issue one
call per file. Keep unrelated large `<<PATCH` values in separate calls, with at most one
syntax-sensitive multiline Go declaration or function replacement per call; short supporting
edits for that same change may remain with it. For an existing Go declaration or function,
prefer one range `type` over assembling the same replacement through several insertions.
Content introduced by a mutation is not targetable in the same call. Before a later invocation
targets a file changed by a successful call, discard its saved references and `hread` only the
required region again; do not reread a file that needs no further edit.

Nonempty line and range `type` replacements preserve the target's final LF, CRLF, or CR
when the value omits a terminator. Explicit terminators are authoritative. An empty
target-bearing `type` value removes owned terminators. `type-` and `type+` insert
byte-exact values and do not synthesize newlines.

Overlapping replacements/deletions and insertions strictly inside them reject. Boundary
insertions are valid. Multiple insertions at the same boundary render in script order.

Changed Go files are parsed and formatted before success; do not run redundant `gofmt`.
Paths remain within the routed workspace root, and parents for `new` or `mv` must exist.
After rejection, use the router's indexed command or multiline-value-row correction only
when the rows still belong to the same baseline; reread stale rows instead of guessing.
