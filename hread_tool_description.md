Use `hread` as replacement of `cat` or `sed`. Read up to 6 workspace paths, optionally
followed by an inclusive `START:END` line range. Use a bare path when it has no whitespace;
otherwise use a JSON-quoted path. Unlike plain file output, every complete line is returned
as `LINE:HASH TEXT`; copy the current `LINE:HASH` directly into an HPATCH/2 target.
