# Token comparison contract

## HP-COMPARE-001: Token comparisons

The comparison program emulates at least these equivalent edit scenarios:

1. Replace a small expression on a long line.
2. Delete the last literal occurrence on a line.
3. Duplicate a multi-line implementation block.
4. Apply sequential edits whose later coordinates observe earlier changes.
5. Create a file with consecutive typing.
6. Edit and move a file, and delete another file, in one script.

Each scenario contains an initial UTF-8 tree, an hpatch script, and an independently
authored equivalent `apply_patch` input. The program evaluates both forms and refuses
to report a comparison unless their resulting paths and file contents are identical.

After equivalence is proven, it obtains the tokenizer through the Go tokenizer
library's GPT-5 model mapping and reports, for each scenario and in total:

- hpatch input tokens;
- handwritten `apply_patch` input tokens;
- absolute token difference; and
- percentage reduction, where positive values mean hpatch uses fewer tokens.

The report names the encoding returned for GPT-5. The comparison is an executable
project artifact, not another `hpatch` CLI mode.
