HREAD/2 reads one UTF-8 workspace file without modifying it.
When several independent files or ranges are already known, emit all of their `hread` calls as
parallel tool-call items in one assistant response; do not wait for one result before issuing the others.

Input is a JSON string containing the workspace-relative path, optionally followed by an
inclusive one-based logical-line range:

```text
"editor.go"
"editor.go" 40:80
```

Output rows are `LINE:HASH TEXT`. `LINE` is the positive one-based logical line. `HASH`
is four lowercase hex digits computed from the exact line content, including indentation
and excluding its terminator. Copy the complete `LINE:HASH` into an HPATCH/2 target.

An end past EOF returns through the final line. Invalid or reversed ranges and ranges
starting past EOF reject. Reading and UTF-8 validation are streamed and cancellable;
formatting rejects before output exceeds 16 MiB.
