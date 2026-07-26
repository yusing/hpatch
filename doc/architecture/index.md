---
pjdoc:
  version: 1
  kind: architecture
  scope: root
  status: approved
  revision: "5"
  files:
    []
---
# hpatch architecture contract

## AC-CORE-001: Virtual workspace and selector state

One engine owns script parsing, structured command variants, logical path resolution,
first-touch order, per-file immutable baselines, baseline cursor or selection state,
recorded edits, conflict validation, final editor state, and net file actions for
`HP-SCRIPT-001`, `HP-FILE-001`, `HP-SELECT-001`, and `HP-EDIT-001`. It evaluates every
command against one in-memory virtual workspace. Normal and translate modes consume the
same completed result; neither mode reimplements command semantics.

The parser retains absolute and signed relative line references structurally, the
optional `tsel` count, and block-selector operation identity. It attaches recognized
command and variant metadata plus a stable reason to syntax failures. Relative line
references are resolved only by the editor owner at execution, against one baseline
cursor snapshot; the parser and metrics owner never infer cursor positions. The process
or library boundary resolves `HPATCH_DISABLE_RELATIVE_LINES=1` once and passes the
feature setting into parsing and help generation.

The editor owner resolves `bsel` against the full baseline and `bsel_next` against its
explicit selection- or cursor-derived scope. One block matcher owns exact occurrence
counting, horizontal-whitespace fallback, original-byte range mapping, start-relative
end search, and exact versus recovered outcome metadata. One occurrence matcher owns
single and contiguous multiple `tsel` spans. All selectors resolve against immutable
baseline content. The editor rejects selectors over already modified spans, intersecting
edit spans, insertions inside spans, and duplicate insertion positions before it
materializes ordered disjoint edits into final content.

The engine obtains original files only through the workspace boundary, never writes files
or process output, and retains baseline identity across moves. Its completed result
contains ordered net changes, structured command metrics, the final active logical path,
and cursor or selection state sufficient for rendered-state reporting.

## AC-STATE-001: Rendered final-state projection

One state projector owned beside the editor maps the final baseline cursor or selection
through the ordered edits into rendered post-edit offsets. It owns boundary affinity:
`type` is after inserted or replacement text, `del` is at the deletion join, and `dup` is
after the inserted copy. It converts rendered offsets into one-based Unicode line and
column positions and extracts the bounded three-line window from the same rendered
content. It reports active moved paths, empty new files, active selections, and absent
active files without consulting the committed filesystem.

One pure formatter converts that projection into the `HP-OUTPUT-001` report, truncates
each preview to 64 Unicode code points, and escapes controls before any external effect.
The projection and formatting path is shared by normal and translate modes. It does not
persist editor state or create a resume mechanism.

## AC-BOUNDARY-001: Filesystem and output boundary

The CLI boundary owns arguments, stdin, workspace selection, environment-derived feature
configuration, diagnostics, stdout, stderr, and exit status for `HP-CLI-001` and
`HP-OUTPUT-001`. The workspace boundary owns a pinned `*os.Root`, a root-relative cwd,
root-scoped filesystem reads, staging, commit, and rollback. Relative script paths resolve
from cwd; absolute script paths become root-relative identities only when within root.
Lexical and symlink escapes fail. Initial inputs cross into the engine only after a
regular-file check and strict UTF-8 decoding. Informational forms are resolved before
stdin, working-directory, configuration-directory, metrics, or filesystem access.

The standalone CLI canonicalizes root and cwd, opens the root once, and keeps that
capability open for the invocation. Library callers pass the already-authorized root and
a root-relative cwd. Absolute operands are matched against the canonical root name;
equivalent aliases are not resolved outside the capability. Translation and normal
commit consume the same identities, so cwd affects relative operands without changing
the workspace boundary.

Normal mode validates and formats the state report, stages the complete engine result,
commits it, and only then emits the report to stderr. Translate mode validates and formats
the same pending-state report, completely renders and writes the patch to stdout, and only
then emits the report. No failure before the external effect emits a final-state report.
A report-write failure after a successful effect is best-effort and cannot be represented
as rollback. The transaction coordinator owns backups, ordered operations, rollback
attempts, and honest reporting of commit or rollback failure.

## AC-METRICS-001: Metrics classification and persistence

One metrics classifier consumes structured parser and evaluator events rather than
re-parsing scripts or diagnostics. It owns effective, ineffective, direct-patch, and
fully emitted report token estimates; aggregate command counters; absolute and relative
selector variants; single and multiple `tsel` variants; separate `bsel` and `bsel_next`
counters; exact and whitespace-recovered block outcomes; and stable terminal reason
counters. The report formatter's exact emitted string is the only source for report-input
token counting. Price ratios are presentation-time calculations and are never persisted.

The metrics store owns tokenizer use, overflow checks, interprocess locking, alternating
checksummed fixed-size slots, generation selection, current-version decoding, obsolete
version reset, and page-cache writeback policy. Classification occurs only after terminal
outcome and report emission are known. Metrics failure remains a warning and cannot
change the requested edit, translated patch, state report, or exit status.

## AC-TRANSLATE-001: Patch rendering

One translation renderer owns all OpenAI `apply_patch` syntax. It receives the engine's
ordered net change set and emits one envelope containing the required `Add File`,
`Update File`, `Move to`, and `Delete File` actions. It finishes the complete string
before stdout is written so evaluation or rendering failures cannot expose a partial
patch. Every emitted path is relative to the workspace root, independent of cwd. It
owns the minimal nonempty verification hunk required by OpenAI `apply_patch` when a move
has no content change, and the renderer-only LF normalization required by the
line-oriented output format. For changed content it expands context until every bare
hunk's old-side sequence is unique, failing instead of emitting an ambiguous patch. The
engine's normal-mode contents remain unchanged.

## AC-COMPARE-001: Independent comparison cases

The comparison artifact may call the engine as test support, but every equivalent
`apply_patch` input is independently authored scenario data. A clearly test-only patch
applier verifies both representations reach the same final path-to-content map before
token counts are reported. Neither the applier nor the comparison is part of the
installed `hpatch` CLI.
