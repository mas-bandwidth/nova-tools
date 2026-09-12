# Codex retained-token mapping — proposed source contract

The current task's metadata-only source check found a better source than repeated token_count snapshots: **top-level `token_usage_record` entries**, each with `response_id`, thread/session/turn IDs, `usage`, `turn_token_usage` and `thread_token_usage`. The recorded producer is **Codex Desktop 0.154.0-alpha.6.2**, independently of the installed CLI 0.153.4. This is a source-shape observation, not a September coverage claim.

Matched public producer tag `rust-v0.154.0-alpha.6.2` resolves to commit `b5bffd3ec4db487e7e3dec59663875b0ef7b72ca`. The [TokenUsageRecord definition](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/protocol/src/protocol.rs#L2237) describes one completed response. The [producer regression fixture](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/core/tests/suite/token_usage_rollout.rs#L29) checks records across a resume: response usage 120, 80, 30 produces thread totals 120, 200, 230, not 550 spend. A response with no usage does not manufacture a record.

Proposed preferred mapping:

- Select top-level `token_usage_record`, not an event_msg of that name. Use its **usage** object once per stable namespaced response_id; retain thread/session/turn IDs as provenance. The turn/thread usage objects are supporting snapshots, never additional spend. Conflicting copies of one response need explicit conflict handling. A fresh collector run or a copied path cannot create a new event ID.
- Keep the rollout observation timestamp with its meaning: the record's timestamp, not a claimed exact request-start timestamp. UTC daily reports can use completion/observation day with that stated accounting basis; missing timestamps stay unknown. Do not fabricate fine-grained dates from older lifetime snapshots.
- Preserve input_tokens, output_tokens and total_tokens exactly; check the supported mapping's input+output relationship. Preserve cached_input_tokens, cache_write_input_tokens and reasoning_output_tokens separately. Input includes cached input; output contains reasoning. Do not add those subsets again. The [Responses usage mapping](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/codex-api/src/sse/responses.rs#L126) directly copies provider input/output/total and details; the [API reference](https://platform.openai.com/docs/api-reference/batch/object?api-mode=responses) identifies cache and reasoning as input/output breakdowns.
- **Zero-detail caveat:** this producer converts absent input-details to a default object, missing cache-write to 0, and absent output-details to reasoning 0. A stored raw 0 in those fields therefore does not prove the provider measured zero. Preserve raw 0 and label its provenance; normalized detail is unknown unless source-presence evidence distinguishes measured zero. Positive detail counts can be retained as measured. Do not silently apply the raw-zero-means-known-zero rule to these defaulted fields.
- TokenUsageRecord carries no model field. Retain the matching turn_context's configured model as `model.id` with `model.basis=requested`; do not rename it an independently verified server model. `requested_model` is not a key of this wire. The adapter must name how it establishes actual model or leave it unknown, with the available ID preserved as model.id and model.basis=requested, and that report grouping clearly labelled. A later configured model must not relabel earlier responses.
- Bench and friend come from an explicit execution-origin binding for the source; the collection host does not establish historical origin. Repository defaults to unattributed for this multi-repo sitting unless an explicit reviewed accounting rule says otherwise.

Older source shapes need a separate versioned fallback mapping. The [context-fill implementation](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/protocol/src/protocol.rs#L2291) can replace total/last counters for a context window. Therefore token_count is not an unconditional append-only bill. Retain those snapshots but do not sum them or mix them with preferred per-response records. Unsupported shapes, unmeasured responses and absent histories are explicit coverage gaps.

Independent fixtures needed for the adapter: copied response IDs across paths/benches, conflicting duplicates, usage versus turn/thread totals, defaulted-zero details, configured-model changes, a context-fill token_count beside real response records, missing usage/timestamp, and an older snapshot-only session. The producer's existing 120/80/30 fixture is source evidence, not a substitute for an independent test of our own adapter.

No private prompts or raw transcript files were sent. This is source and mapping evidence for review, not an implemented adapter or a completed backfill.

## Wire literals under the mapping decisions

Every cell below is the source owner's decision on this PR (Stella, comment
[5647652764](https://github.com/mas-bandwidth/nova-tools/pull/142#issuecomment-5647652764)).
A cell her decision does not reach reads `UNDECIDED (Stella)` rather than a guess, and the
open cells are listed together at the end of this section. Where an earlier paragraph of
this document defers a question these tables decide, the tables are the decision.

The executable form is `testdata/tokens/codex/`: the sealed mapping manifest, the synthetic
source records, the expected observation envelopes and the shapes the wire must refuse. The
landed publisher boundary checks all of it in
`internal/records/mapping_fixtures_test.go` (`TestCodexRetainedMappingFixtures`).

### The observation envelope, key by key

| Wire key | Literal |
|---|---|
| `schema` | `nova.tokens.observation/2` |
| `source.kind` | `codex_desktop` |
| `source.namespace` | `nova.codex-desktop.responses` |
| `source.producer_version` | the producer version in the source record's own metadata, never the installed CLI's; null when the source carries none. The exact source key is `UNDECIDED (Stella)`; the fixture names it `producer_version`. |
| `source.session_id` | the native containing thread/session ID, retained as provenance only |
| `source.event_key` | `[response_id]`. A containing or forked thread never enters the event key. |
| `kind` | `request` |
| `revision` | `{native: null, supersedes: [], basis: none}`. No source ordering is inferred from an export update time. |
| `time.occurred_at` | the source rollout record's timestamp, offset and precision preserved; null when the record carries none |
| `time.basis` | `response_observation`; `unknown` when the timestamp is missing |
| `time.start`, `time.end` | null: a response observation carries no interval |
| `origin` | `{friend, bench, basis: owner_binding, binding_id}` with an explicit stable source-binding ID and the supplied original friend/bench; otherwise `{null, null, unknown, null}`. The collection host never supplies origin. |
| `model.id` | the matching `turn_context`'s configured model; null when there is none |
| `model.basis` | `requested`; `unknown` when the ID is absent. A later configured model never relabels earlier responses. `requested_model` and `model_basis` are never emitted as extra wire keys. |
| `repository` | `{id: null, basis: unattributed, policy_id: null, touched: []}` absent a separately evidenced policy |
| `raw_usage` | the closed six-field allowlist below; every field of it has an entry, absent ones included |
| `model_usage` | `[]` † |
| `mapping_id` | the content ID of the sealed mapping manifest that read this shape (`testdata/tokens/codex/mapping.json`), never a fixture placeholder |
| `receipt` | `response_id` and `turn_id` only, strings only |

† Not a cell of her comment. `TokenUsageRecord` carries no model field (her cited fact) and
the format reads an empty array as "no split supplied", so an empty array is the only
faithful mapping of a source that supplies no per-model split.

### `raw_usage`: the per-mapping allowlist

The allowlist is this mapping's own closed set of original source names. It is not the
landed `internal/records/testdata/allowlists.json` vocabulary, which is a fixture input to
the core tests rather than a global field vocabulary; raw names are never renamed to
resemble another fixture.

| Source field | `number_kind` | `unit` | `zero_semantics` | Role | Absent on the wire |
|---|---|---|---|---|---|
| `input_tokens` | `integer` | `tokens` | `measured` | base counter | `presence: absent`, `value: null`, `reason: not_supplied` |
| `output_tokens` | `integer` | `tokens` | `measured` | base counter | same |
| `total_tokens` | `integer` | `tokens` | `measured` | base counter | same |
| `cached_input_tokens` | `integer` | `tokens` | `default_may_mask_absence` | subset of input | same |
| `cache_write_input_tokens` | `integer` | `tokens` | `default_may_mask_absence` | subset of input | same |
| `reasoning_output_tokens` | `integer` | `tokens` | `default_may_mask_absence` | subset of output | same |

The three base counters are `measured` for this pinned `ResponseCompletedUsage` path only:
at `b5bffd3ec4db487e7e3dec59663875b0ef7b72ca`, `responses.rs` lines 128-150 has required
`i64` base counters and copies them directly. The three detail counters keep the defaulting
uncertainty this document already records. A present numeric key retains its original
lexeme with `reason: null`; a missing supported key is `absent`/`null`/`not_supplied`; an
explicit null or non-numeric value is a named unsupported source-shape outcome and never a
coerced zero — the bounded reason code for that outcome is `UNDECIDED (Stella)`, so no
observation is emitted for it here. Other optional producer metrics are outside this
supported set and are not silently promoted into it.

### Arithmetic, overlap and conflict

| Question | Rule |
|---|---|
| input + output vs total, all three known | a mismatch is a retained mapping conflict, excluded from normalized spend, never adjusted |
| impossible known subset relationship | the same conflict outcome |
| missing raw `total_tokens` | stays absent; a view may derive a total from both known base components, labelled derived, without modifying the raw field |
| unknown defaulted-zero detail | stays unknown and makes detail completeness false |
| `turn_token_usage`, `thread_token_usage` | supporting snapshots, never additional spend |
| fallback `token_count` snapshots | a separate, explicitly unsupported-for-spend mapping; never summed and never mixed into preferred response spend. Its `namespace`, `kind` and `event_key` are `UNDECIDED (Stella)`. |
| one response copied to another bench or path | the same observation: identical copies deduplicate |
| the same `response_id` with changed counters | a conflict, retained and excluded from spend; there is no newest-wins rule |
| a fresh collector run or copied path | creates no new event identity |

### Day allocation

A known point instant shards by its UTC day; anything else is `unallocated`. A report using
completion/observation day labels that convention and never claims exact call-day
allocation. This is the format's point-timestamp rule, not an interval delta dropped onto
its final day.

### Open cells

1. The bounded reason code for an explicit null or non-numeric counter (her decision names
   a "named unsupported source-shape outcome" without choosing the code).
2. The exact source metadata key that carries `producer_version`.
3. The fallback `token_count` snapshot mapping's `namespace`, `kind` and `event_key`.

### The fixtures

| File | Contents |
|---|---|
| `mapping.json` | the sealed `nova.tokens.mapping/2` manifest; its ID is the `mapping_id` of every expected envelope, and it names the digest of each source file |
| `source_rollout.jsonl` | four mappable response records (full, present-zero base counter, no timestamp/model, arithmetic mismatch), one non-integer counter, one explicit-null counter, and a `token_count` context-fill snapshot beside them |
| `source_rollout_copy.jsonl` | the first record copied byte for byte, and one changed copy of it |
| `expected_records.jsonl` | six sealed observation envelopes: four spend keys, one exact duplicate that deduplicates, one changed copy that conflicts |
| `refused_records.jsonl` | three shapes the wire must refuse, each with its rule and field: a non-integer counter, a receipt field outside the allowlist, and a missing entry of the closed allowlist |

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
