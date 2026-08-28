# Independent comparison cases

## CTR-COMPARE-001 — Independent comparison cases

The comparison artifact may call the engine as test support, but every equivalent
`apply_patch` input is independently authored scenario data. A clearly test-only patch
applier verifies both representations reach the same final path-to-content map before
token counts are reported. Neither the applier nor the comparison is part of an installed runtime surface.
