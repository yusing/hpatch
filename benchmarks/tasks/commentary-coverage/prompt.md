Create a tiny Go library while exercising every task-reachable Hpatch commentary path in the order
below. Create exactly `go.mod` and `coverage.go`; do not add other files. The module and root package
must both be named `commentarycoverage`, use Go 1.26, and have no third-party dependencies.

Complete these steps in order. The tool choices and commentary values are part of the benchmark:

1. Use one `functions.hpatch` call to create both files. Define an unexported string constant named
   `status` with value `draft`, and export `func Status() string` returning that constant.
2. Use `functions.shell` with its Bash evaluator. In that shell program, publish exactly
   `coverage:bash` with the reserved `commentary` command, then change only the constant value from
   `draft` to `current` with `sed`.
3. Submit a `functions.hpatch` edit whose target is the now-stale complete constant declaration with
   value `draft`, replacing it with the same declaration whose value is `ready`. This call must be
   rejected as stale. Use the returned current command handle in one `functions.hpatch_recover` call
   to correct the target to the complete declaration with value `current`; preserve the replacement
   value `ready`.
4. When `functions.report_issue` is available, invoke it once after recovery. Give the report the
   title `Commentary coverage recovery` and state that the intentional stale-target recovery
   completed. Continue regardless of whether that optional tool is present.
5. Use `functions.shell` with a compact `#!sh` selector. Publish exactly `coverage:posix` with the
   reserved `commentary` command, run `gofmt -w coverage.go`, and run `go test ./...`.
6. Call the provider-owned `functions.exec_command` tool once. Run a command that prints exactly
   `coverage:exec-complete`. This tool uses router default commentary and does not accept authored
   commentary.
7. Call the custom Code Mode `exec` tool with executable JavaScript. Publish exactly
   `coverage:code-mode` through `await commentary(...)`, then emit `coverage:code-mode-complete`
   with `text(...)`.

The final workspace is complete only when `Status()` returns `ready`, package validation passes, and
every required operation above has completed. Then make the final assistant response exactly:

```text
verification: exhaustive commentary coverage passed
```

Return no code fence or other text, with no leading or trailing line feed.
