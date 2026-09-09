# Context-compaction boundary

## CTR-COMPACTION-001 — Router-owned pruning and local envelopes

The router's context-compaction HTTP boundary precedes ordinary Responses
projection and upstream transport. It owns local standalone and V2 completion,
restoration of its own input envelopes, and bounded request decoding. Codex owns
configuration resolution, trigger timing, retained client-side context, and
installation of the returned compaction result.

Command-aware reducers own only evidence selection. They do not execute commands,
interpret opaque provider state, infer task completion, or choose a fixed context
budget. Unknown forms retain their evidence.

The envelope owner authenticates and encrypts the retained native item array.
Its persistent key belongs to the Hpatch configuration directory, not a thread,
temporary plugin runtime, provider credential, or capture stream. Cross-process
locking serializes first creation. Compression is an internal envelope-storage
detail, not the semantic compaction mechanism or a token-usage measurement.

Restoration aligns the client's carried prefix against the retained native
timeline backwards by stable item/call identity or canonical JSON. Supported client truncation
must match a unique authenticated original. Fresh canonical context is never
content-deduplicated into an older instruction, and unmatched current context
is retained in relative order; the post-envelope suffix is appended unchanged.
Restored items enter the existing Hpatch and CTP boundaries as native history.
Neither those boundaries nor the provider interpret router-owned ciphertext.

No provider transport is available to the local compaction handler. Errors leave
Codex without a replacement window rather than silently losing context or issuing
a model summary. Capture observes local HTTP traffic but local compaction does not
fabricate provider token usage.
