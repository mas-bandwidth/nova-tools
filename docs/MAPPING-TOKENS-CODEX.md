# Codex retained-token mapping — proposed source contract

The current task's metadata-only source check found a better source than repeated token_count snapshots: **top-level `token_usage_record` entries**, each with `response_id`, thread/session/turn IDs, `usage`, `turn_token_usage` and `thread_token_usage`. The recorded producer is **Codex Desktop0.154.0-alpha.6.2**, independently of the installed CLI0.153.4. This is a source-shape observation, not a September coverage claim.

Matched public producer tag `rust-v0.154.0-alpha.6.2` resolves to commit `b5bffd3ec4db487e7e3dec59663875b0ef7b72ca`. The [TokenUsageRecord definition](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/protocol/src/protocol.rs#L2237) describes one completed response. The [producer regression fixture](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/core/tests/suite/token_usage_rollout.rs#L29) checks records across a resume: response usage120,80,30 produces thread totals120,200,230, not550 spend. A response with no usage does not manufacture a record.

Proposed preferred mapping:

- Select top-level `token_usage_record`, not an event_msg of that name. Use its **usage** object once per stable namespaced response_id; retain thread/session/turn IDs as provenance. The turn/thread usage objects are supporting snapshots, never additional spend. Conflicting copies of one response need explicit conflict handling. A fresh collector run or a copied path cannot create a new event ID.
- Keep the rollout observation timestamp with its meaning: the record's timestamp, not a claimed exact request-start timestamp. UTC daily reports can use completion/observation day with that stated accounting basis; missing timestamps stay unknown. Do not fabricate fine-grained dates from older lifetime snapshots.
- Preserve input_tokens, output_tokens and total_tokens exactly; check the supported mapping's input+output relationship. Preserve cached_input_tokens, cache_write_input_tokens and reasoning_output_tokens separately. Input includes cached input; output contains reasoning. Do not add those subsets again. The [Responses usage mapping](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/codex-api/src/sse/responses.rs#L126) directly copies provider input/output/total and details; the [API reference](https://platform.openai.com/docs/api-reference/batch/object?api-mode=responses) identifies cache and reasoning as input/output breakdowns.
- **Zero-detail caveat:** this producer converts absent input-details to a default object, missing cache-write to0, and absent output-details to reasoning0. A stored raw0 in those fields therefore does not prove the provider measured zero. Preserve raw0 and label its provenance; normalized detail is unknown unless source-presence evidence distinguishes measured zero. Positive detail counts can be retained as measured. Do not silently apply the raw-zero-means-known-zero rule to these defaulted fields.
- TokenUsageRecord carries no model field. Retain the matching turn_context's configured model as `requested_model` with that provenance; do not rename it an independently verified server model. The adapter must name how it establishes actual model or leave it unknown, with the available ID preserved as model.id and model.basis=requested, and that report grouping clearly labelled. A later configured model must not relabel earlier responses.
- Bench and friend come from an explicit execution-origin binding for the source; the collection host does not establish historical origin. Repository defaults to unattributed for this multi-repo sitting unless an explicit reviewed accounting rule says otherwise.

Older source shapes need a separate versioned fallback mapping. The [context-fill implementation](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/protocol/src/protocol.rs#L2291) can replace total/last counters for a context window. Therefore token_count is not an unconditional append-only bill. Retain those snapshots but do not sum them or mix them with preferred per-response records. Unsupported shapes, unmeasured responses and absent histories are explicit coverage gaps.

Independent fixtures needed for the adapter: copied response IDs across paths/benches, conflicting duplicates, usage versus turn/thread totals, defaulted-zero details, configured-model changes, a context-fill token_count beside real response records, missing usage/timestamp, and an older snapshot-only session. The producer's existing120/80/30 fixture is source evidence, not a substitute for an independent test of our own adapter.

No private prompts or raw transcript files were sent. This is source and mapping evidence for review, not an implemented adapter or a completed backfill.

## Wire boundary and remaining acceptance

This source contract specializes [retained records](PROPOSAL-TOKENS-RECORDS.md)
and the [version 2 wire](PROPOSAL-TOKENS-FORMAT.md). It is a proposal for built-in
adapter behavior, not an executable manifest or an installed collector. The adapter
must produce every supported raw field, including explicit absent/unavailable entries,
without a floating-point round trip. Native numeric turn identifiers are preserved as
exact decimal strings. Source metadata and raw counters enter only their named wire
locations; prompts, arbitrary source objects and private filenames never do.

The implementation must return a closed extraction allowlist, synthetic source fixtures
with independently stated expected observations, a sealed mapping manifest and its
implementation ID. Unknown source fields are excluded, not dynamically admitted into an
allowlist derived from the input. Conflicting spend-key observations stay retained and
excluded from totals until an evidenced revision resolves them. Changing the source path,
collector run or collection bench cannot authorize choosing a winner.

Acceptance requires independent decoder and privacy-sentinel tests plus a real
collector/publisher/view join. Preserve raw evidence even where normalization remains
unknown, and report the specific unsupported shape or unresolved identity/semantic gap.
Neither this document nor a successful fixture implies production September coverage.
