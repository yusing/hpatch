# Agent issue reports

## REQ-DIAGNOSE-001 — Agent issue reports

When hpatch mode starts with the inherited environment variable `HPATCH_DIAGNOSE` exactly
equal to `1`, the immutable built-in registry contributes a model-visible unconstrained custom
tool named `report_issue`. Any other value, including an unset variable, omits that contribution.
Passthrough mode remains unchanged because it does not construct the registry.

The tool accepts agent-authored Markdown for problems encountered while using hpatch and its
related tools. The router snapshots `hooks.diagnose` from the existing `settings.json` hook
configuration at startup and invokes those commands directly when the model calls `report_issue`.
It does not install an executable wrapper or frontend and does not route the report through the
executor plugin worker.

Each command is rendered against an event whose `Body` is the exact Markdown and whose `Title`
is the current Codex task title, using the same session-title lookup as other routed hooks; when
no title is available, `Title` is `hpatch diagnostic`. `format_markdown` returns that same body,
and `shellquote` retains its existing behavior. All configured diagnose commands share the
existing 10-second error-hook timeout. A missing or empty diagnose list is a successful no-op.
Successful dispatch returns `Issue reported.` through the existing Code Mode result carrier;
settings or registry initialization failures prevent startup. Rendering, execution,
cancellation, or timeout failures return an `hpatch: warning:` in the tool result and do not
fail or interrupt response routing.

Acceptance:

1. Exactly `HPATCH_DIAGNOSE=1` in hpatch mode exposes the free-form `report_issue` specification;
   all other values and passthrough mode expose none of it.
2. One report reaches each configured `hooks.diagnose` command byte-for-byte through `.Body` and
   `format_markdown`, exposes the task title through `.Title`, and does not run `hooks.error`.
3. No configured diagnose hook succeeds without side effects, while hook failures remain
   observable as tool-result warnings without failing the translated response.
4. `report_issue` has no executable wrapper, stable frontend, or plugin-worker implementation.
