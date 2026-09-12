# Journal

## REQ-JOURNAL-001 — Per-thread milestone journals

Mekugi mode owns one durable milestone journal per stable thread. Passthrough is unchanged.
Items have router-assigned IDs (`j1`, `j2`, ...), nonblank UTF-8 text, canonical author,
router sequence creation/update values, `report_now`, and `reported` state. A thread has at most
256 items and 256 threads are retained. Item text and the per-response live progress budget
are 16 KiB. Terminal flushes have a separate bound sized for all 256 items, including labels
and the author heading; child root copies have an independent terminal budget of the same size
plus the bounded child prefix. Capacity exhaustion rejects new journal state, not unrelated calls.

An ordinary fork copies the source's latest journal at its first accepted normal Responses
request in the selected workspace, then evolves independently. It does not reconstruct the
historical fork point. Routing-session remaps and resumes keep the stable thread's journal.

Eligible non-strict ordinary function tools receive an optional `journal` array. Each mutation is
`add`, `edit`, or `delete`; additions and edits require nonblank text, edits and deletes require
an existing ID, and a malformed array rejects the host call before execution. The array is applied
in order atomically, then removed from executed arguments while the original call remains available
through replay. `report_now` emits a router-owned user-only message and successful delivery marks
that revision reported. Deletes are silent unless retracting an already-reported ID.

Mekugi mode also exposes `functions.journal` with one operation: `list`, `add`, `edit`, or
`delete`. List is read-only and may address only a proven ancestor or descendant journal. Unknown
or conflicted ancestry fails closed. Mutations return router-assigned IDs. The dedicated tool includes `journal_ids` for any batched field mutations, independently of its main operation result. Journal calls are
router state operations and do not invoke an executor.

At a successful terminal response with no client-dispatched calls, the router emits a deterministic
flush containing only unreported items, skips it when empty, then emits token metrics. Provider
final-answer text is omitted. A child additionally emits
`Journal flushed: N new, M already shown` so collaboration result selection is nonempty. Failed,
incomplete, and interrupted responses neither flush nor discard already-streamed provider output.
Router-owned messages use generated IDs and are removed from later provider input by exact ID.

Mekugi mode forces `tools.update_plan.enabled=false` and removes `update_plan` declarations from
the request catalog, including nested additional-tool namespaces. Stock Planning/Tasks conflicts
are rewritten to journal guidance. Passthrough retains the stock tool and prompt.

### Runtime authoring

Code Mode reserves `await journal({op, id?, text?, report_now?})`, also accepting
a mutation array. The parser preserves strings, comments, properties, and unrelated
identifiers, and leaves unparseable source unchanged for the executor to diagnose.

Bash and POSIX reserve `journal add TEXT`, `journal edit ID TEXT`, and
`journal delete ID`, optionally followed by `--report-now`. Expanded operands remain
individual argv values. A successful publication writes no script output. Invalid
mutations and unavailable publishers return errors rather than silently losing records.
There is no `commentary` alias. Other interpreters have no journal builtin.

Both forms reuse authenticated broker routes and private thread-bound discovery.
Shell routes use inherited `CODEX_THREAD_ID`; do not add publisher flags or inline
environment assignments to scripts. Concurrent workers share a thread route without
guessing original call attribution. Code Mode retains its per-call capability until
completion or expiry. Closing an already handed-off response does not cancel its publisher.

### Delivery failures

Required terminal journal messages must have retained replay provenance before a
successful terminal can discard provider final text. If required retention fails,
the response fails instead of silently completing without the flush or child summary.
Unacknowledged revisions remain available for a later flush.

Final-answer buffering remains bounded. Exceeding the response-buffer limit fails the response
and drains already-buffered provider output rather than leaking a successful provider final.

### Acceptance

1. Mekugi catalogs omit Tasks/`update_plan` and project journals only onto eligible ordinary
   function schemas. Strict tools, provider-owned additional tools, collaboration, and
   user-messaging schemas stay exact; passthrough is unchanged.
2. Batched mutations apply atomically before execution, return assigned IDs, and disappear
   from host arguments. Replay restores the original call without executing it again.
3. CRUD, independent ordinary forks, resume, capacity rejection, and retained reporting
   revisions survive router restart in the selected workspace.
4. Cross-thread listing allows only proven ancestors and descendants, never siblings.
   It is read-only and does not copy entries or subscribe to their delivery.
5. Immediate notices are acknowledged after successful emission. Silent edits become
   flush-eligible again; deleting a previously shown ID with report_now emits a retraction.
6. Successful eligible terminals show only unreported journal revisions, then eligible
   token metrics, followed by the child summary when applicable. They suppress provider
   final text; failures and interruptions do not terminal-flush.
7. The native Codex spawn fixture proves that journal results survive client normalization
   and that the parent receives the child's synthetic summary.
8. Debug evidence separates applied mutations, runtime wiring, live rendering, and
   terminal flushing without recording journal bodies or private publication credentials.
