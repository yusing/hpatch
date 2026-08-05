HREAD/2 reads one or more UTF-8 workspace files without modifying them.

Input contains up to 6 existing read specifications separated by newlines. Each specification
is a JSON string containing the workspace-relative path, optionally followed by an inclusive
one-based logical-line range:

```text
"editor.go"
"parser.go" 40:80
```

A single specification remains valid and returns only its existing `LINE:HASH TEXT` rows.
A batch preserves input order and precedes every item with `==> SPEC <==`. An item error is
reported beneath its header without hiding successful siblings.

Output rows are `LINE:HASH TEXT`. `LINE` is the positive one-based logical line. `HASH`
is four lowercase hex digits computed from the exact line content, including indentation
and excluding its terminator. Copy the complete `LINE:HASH` into an HPATCH/2 target.

An end past EOF returns through the final line. Invalid batch syntax rejects the call.
Reversed ranges and ranges starting past EOF reject that item. Reading and UTF-8 validation
are streamed and cancellable. A single item rejects before exceeding 16 MiB. A batch stays
within the same complete-call bound and ends with `hread: batch output limit reached; retry
remaining items in a narrower batch` when more output would exceed it.
