# Shell-routed verified-row search

## REQ-GREP-001 — Shell-routed verified-row search

The private `hgrep` command is available through the model-visible shell tool. The shell owns
quoting, redirection, pipelines, and command composition; hgrep receives the resulting ordinary
argv. It accepts familiar ripgrep pattern, matching, file, glob, type, ignore, context, and
resource-selection arguments. With no explicit path, search defaults to the shell process's
current directory. GNU grep's `-R` is accepted and removed because traversal is already
recursive by default.

The worker invokes installed `rg` with internal `--json --no-config` arguments. It rejects
model-supplied output, multiline, preprocessor, compressed-input, informational, and other
modes that cannot identify one complete editable source row. Ripgrep retains ownership of
regex parsing, ignore rules, traversal, matching, and search diagnostics; hgrep provides no
fallback search engine.

The worker consumes ripgrep's structured match and context events and emits each first-seen
logical row once in ripgrep result order:

```text
"PATH":LINE:HASH TEXT
```

`PATH` is JSON-quoted. `LINE:HASH TEXT` has exactly the identity and complete UTF-8 semantics
of `REQ-READ-001`. Match highlighting, match-only fragments, replacement output, trimming,
and line truncation cannot change it. Multiple matches on one path and line produce one row.
Ripgrep's no-match exit status is a successful empty result. Execution, filesystem, encoding,
cancellation, invalid-pattern, and missing-executable failures return concise nonzero
diagnostics. Output contains only complete rows and uses the shared verified-row token admission
rule in `REQ-READ-001`. On the first omitted distinct result, hgrep terminates and reaps ripgrep.

For input metrics, hgrep produces its current and stock results from the same ripgrep event
stream. The stock result preserves each JSON-quoted `PATH`, `TEXT`, LF, result order, and
diagnostic while omitting the `LINE:HASH ` portion. The comparison does not run ripgrep twice.

Acceptance:

1. A regular-expression search with an explicit path and glob emits JSON-quoted paths,
   positive line numbers, four-digit hashes, and exact complete matching lines that can be
   copied directly into an hpatch target.
2. Shell quoting determines literal arguments, and ordinary redirection or pipelines operate
   as shell syntax rather than becoming hgrep argv. A conflicting hgrep output or transformed
   input mode still rejects before ripgrep starts. The `-R` compatibility flag does not reach
   ripgrep or produce a warning.
3. Requested before/after context emits complete verified rows beside matches. Repeated match
   or context events on one row emit that row once; no matches return successful empty stdout.
   Token admission occurs after this deduplication, and an incomplete result retains paired
   current and stock rows, writes its diagnostic to stderr, and returns nonzero.
4. The model-visible shell call and output are replayed unchanged. No standalone hgrep call is
   exposed, routed, or admitted to hpatch recovery history.
5. Router startup validates hgrep inside the immutable built-in snapshot without installing a
   frontend. Passthrough mode loads and exposes none of these replacement surfaces.
