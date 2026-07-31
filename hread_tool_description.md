HREAD/1 reads one UTF-8 workspace file without modifying it.

Input is a JSON string containing the workspace-relative path, optionally followed by an
inclusive one-based logical-line range:

```
"editor.go"
"editor.go" 40:80
```

Output rows are `HHHH: TEXT`, where `HHHH` is a stable four-digit lowercase hash of the
complete logical-line content. Copy the hash into hpatch selectors. Bounded reads still use
numeric input ranges, but their output remains hash-only. Invalid, reversed, or out-of-bounds
ranges reject instead of clamping. Reading and UTF-8 validation are streamed and cancellable;
formatting rejects before output exceeds 16 MiB.
