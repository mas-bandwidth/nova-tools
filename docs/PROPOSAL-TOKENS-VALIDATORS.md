# Retained token validators: mapping/2 and coverage/2

Addendum to [PROPOSAL-TOKENS-FORMAT.md](PROPOSAL-TOKENS-FORMAT.md) and
[PROPOSAL-TOKENS-RECORDS.md](PROPOSAL-TOKENS-RECORDS.md), in FORMAT's register. It closes the
two validators SPEC-TOKENS.md rule 29 names as missing, so a `--batch` naming them no longer
exits 2 `refusing to guess`. The envelope, canonical content ID, duplicate-key / invalid-UTF-8 /
raw-number refusal, `Refusal` shape and the `nova.tokens.observation/2` validator are the model
and are unchanged; only the two new bodies are specified. Everything below is derived from the
two sealed manifests `testdata/tokens/{codex,grok}/mapping.json`, whose bytes are preserved.

Legend: `str` non-empty string, `str?` string-or-null, `label` `[a-z0-9][a-z0-9-]{0,31}`,
`ns` `[a-z0-9][a-z0-9._-]{0,63}`, `cid` `sha256:<64 lowercase hex>`, `int` `0|[1-9][0-9]*`,
`ts` RFC 3339 with explicit offset or `Z`. Every object is closed: unknown key `unknown_field`,
missing key `missing_field`, wrong type `wrong_type`. No `interface{}`, no raw object, no raw
JSON number, no executable expression, no plugin loading. Policy strings are retained verbatim
inside their typed slot and never parsed, normalized or executed.

## A. mapping/2

Envelope ID is the body's canonical SHA-256 (unchanged). The body is exactly these 12 members,
all required (present even when null):

| member | type / value |
|---|---|
| `schema` | `str` = `"nova.tokens.mapping/2"` |
| `name` | `label` (never a path) |
| `revision` | `str`, `[a-z0-9._-]{1,64}` |
| `source_shapes` | `[str]` sorted unique; each element a dotted shape id (`ns`) |
| `field_rules` | object map, see below |
| `identity_rule` | object, see below |
| `revision_rule` | object, see below |
| `time_rule` | object, see below |
| `model_rule` | object, see below |
| `overlap_rule` | object, see below |
| `fixture_digests` | object map: `str` filename -> `cid` |
| `implementation_id` | `str?`; null = proposal (identity below) |

**`field_rules`** maps each numeric source field name (`str`) to a closed rule object of exactly
8 members: `number_kind` ∈ `{integer, decimal}`; `zero_semantics` ∈
`{measured, default_may_mask_absence, unknown}` (required — absent => `mapping_missing_zero_semantics`);
`unit` `[a-z0-9][a-z0-9_-]{0,31}`; `spend_role` ∈ `{base_counter, subset_detail, non_spend}`;
`absent_presence` ∈ `{present, absent, unavailable}`; `absent_reason` ∈ ReasonCodes;
`invalid_presence` ∈ `{present, absent, unavailable}`; `invalid_reason` ∈ ReasonCodes.
A present zero under `default_may_mask_absence` or `unknown` stays unmeasured (existing
`Normalize`); `field_rules` names every numeric field, and `number_kind` is never inferred.

**The five rule objects are closed, typed, per reviewed adapter.** The adapter is
`identity_rule.source_kind` ∈ `{codex_desktop, grok}` — the only two with enumerated shapes.
A member value may be only `bool`, `str`, `null`, `[str]`, or a nested object whose keys are
enumerated below. Policy strings (`session_id`, `producer_version_from`, `occurred_at`, the
conflict-disposition strings, `owed_coverage_tasks` entries, …) are retained verbatim inside
that shape; the validator checks type and, where stated, enum membership and list sort only.
Any key outside the adapter's set, or any other value type, is `mapping_rule_shape` — an
explicit refusal, never an open object and never a bypass.

- `identity_rule` (both): `source_kind` ∈ SourceKinds; `namespace` `ns`; `event_key` `[str]`
  non-empty, positional (NOT sorted); `observation_kind` ∈ `{request,turn,snapshot,aggregate}`;
  `normalized_spend_supported` `bool`; `producer_version_from` `str`; `session_id` `str`;
  `receipt_fields` `[str]` sorted unique labels; `receipt_value_type` = `"string"`.
  codex adds `containing_or_forked_session_in_event_key` `bool`.
  grok adds `collection_timestamp_substitution` `bool`, `containing_session_substitution`
  `bool`, `unsupported_reason` `str`.
- `revision_rule` (identical in both): `basis` ∈ `{source_order, operator_correction, none}`;
  `native` `str?`; `supersedes` `[cid]` sorted unique; `identical_copy` `str`;
  `changed_same_key` `str`; `inferred_order_from_export_update_time` `bool`; `newest_wins` `bool`.
- `time_rule` (both): `basis` ∈ timeBases; `occurred_at` `str`; `offsets` `str`;
  `day_allocation` `str`; `start` `str?`; `end` `str?`; `missing_timestamp`
  `{basis ∈ timeBases, occurred_at null}`. grok adds `cross_midnight` `str`.
- `model_rule` codex: `basis` ∈ modelBases; `id` `str`; `absent` `{basis, id null}`;
  `later_model_relabels_earlier_responses` `bool`; `forbidden_wire_keys` `[str]` sorted unique;
  `model_usage` `[]`. grok: `single_reported_id` `{basis, id}`; `absent_id` `{basis, id}`;
  `multiple_model_ids` `{basis, id}`; `split_normalized` `bool`;
  `aggregate_is_counting_candidate` `bool`; `detail_retained_not_counted_again` `bool`;
  `model_usage_fields` `[str]`; `model_usage_sorted_by` `str`; `forbidden_wire_keys` `[str]`
  sorted unique.
- `overlap_rule` codex: `counting_source` `str`; `arithmetic_mismatch` `str`;
  `missing_raw_total` `str`; `thread_token_usage` `str`; `turn_token_usage` `str`;
  `token_count_snapshots` `str`; `summed_or_mixed_with_preferred` `bool`;
  `report_must_name_unhandled_token_count_coverage` `bool`; `owed_coverage_tasks` `[str]`.
  grok: `counting_grain` `str`; `arithmetic_violation` `str`;
  `evidenced_overlapping_grain` `str`; `request_grain_mapping` `str`; `session_totals` `str`;
  `forbidden_unions` `[str]`; `checksum_requires_evidenced_complete_matching_coverage` `bool`;
  `session_totals_fill_missing_turn_spend` `bool`; `owed_coverage_tasks` `[str]`.

**`implementation_id`.** Null is legal for a proposal. The final non-null form is
`<adapter>@<version> build=<build-id>` — an assigned identity (adapter label, version label,
the binary's compiled-in build id), NOT a digest of the adapter source and NOT a digest of any
fixture it names, so there is no code↔fixture hash cycle. Adoption/normalization refuses a
non-null id naming an unsupported or unbuilt implementation. Historical proposals (null) are
preserved, never rewritten into non-null.

## B. coverage/2

The body is exactly these 14 members, all required:

| member | type / value |
|---|---|
| `schema` | `str` = `"nova.tokens.coverage/2"` |
| `scope_id` | `ns` — a declared metadata scope, never a private path |
| `source_ids` | `[str]` sorted unique, each a source-scope binding id (`ns`); non-empty |
| `interval` | `{start: ts, end: ts}` both REQUIRED, start < end, exclusive end |
| `status` | ∈ `{complete_within_scope, partial, unavailable}` |
| `reasons` | `[{code, source}]`, see below |
| `collected_at` | `ts`, REQUIRED collection provenance (distinct from interval and spend day) |
| `collector_build` | `str`, the collector binary's build id |
| `collector_friend` | `label?` (null when unknown) |
| `collection_bench` | `label?` (null when unknown) |
| `mapping_ids` | `[cid]` sorted unique |
| `shards` | `[shard-ref]`, see below |
| `predecessors` | `[cid]` sorted unique |
| `counts` | object of `int` strings, see below |

Unknown friend/bench is **null in the body**; `_` is only the ledger-path spelling of null and
fails the label grammar, so it can never be a body value and no real label aliases the unknown
partition. `interval.start`/`end` are required RFC 3339 with start < end and an exclusive end
(`interval_incomplete` / `interval_order`); a live source cannot become `complete_within_scope`
merely because the shape is valid. `collected_at` is collection provenance, distinct from the
inspected interval and the spend day.

**`reasons`** is an array of `{code, source}`: `code` ∈ the closed set `{source_unavailable,
unsupported_rows, unknown_fields, partial_interval, conflict}`, `source` `label?` naming which
source or gap (null when scope-wide). Fixed codes with bounded metadata, never free-form
diagnostics.

**`shards`**: each shard-ref is a closed object of exactly 3 members — `shard_id` `cid` (the
shard file's byte SHA-256, which is also its ledger path name); `record_count` `int` ≥ 1 (the
exact number of observation envelopes in the shard); `observation_ids` `[cid]` sorted unique
(the exact envelope IDs, in order). The structural equation `record_count ==
len(observation_ids)` is provable in-memory and enforced (`shard_reference`). The records
validator checks this shape and equation and resolves nothing; the publisher resolves the shard
file — opens it, verifies byte SHA-256 == `shard_id`, the JSONL record count, and that the
sorted ID set equals `observation_ids` — outside the no-I/O records package. Inventory ID
uniqueness is `sortedUnique` over `observation_ids`.

**`counts`**: exact nonnegative INTEGER decimal strings. The complete set is `source_candidates`
(source candidate records inspected), `records_emitted` (observation envelopes emitted into
shards), `observations` (distinct observation IDs after dedup), `conflicts` (unresolved same-key
conflicts excluded from spend), `gaps` (named coverage gaps). Each value is `int`. Provable
equations: `gaps == len(reasons)` (structural); `records_emitted == Σ record_count` and
`observations == |∪ observation_ids|` are pipeline-provable (publisher/view), not structural.

## C. Validator contract (mirrors observation/2)

- Constants `SchemaMapping` / `SchemaCoverage` beside `SchemaObservation`; dispatch switches on `body.schema`.
- `Envelope` gains `Mapping *Mapping` and `Coverage *Coverage` (one non-nil); `ValidateEnvelope` unchanged.
- Plain-data types `Mapping{...}`, `Coverage{...}`; sub-shapes `MappingFieldRule`, `ShardRef`, `CoverageInterval`, `CoverageReason`.
- `validateMapping` / `validateCoverage` use `exactKeys`, `enumField`, `stringField`/`nullableString`/`nullableLabel`, `contentIDField`, `sortedUnique`, `timestamp`, integer lexeme.
- `SealMapping` / `SealCoverage` seal then strict-read-back through `ValidateEnvelope` (no skip set).
- `Refusal{Rule, Field, Note}` unchanged; one bounded line, field name only, never the value.
- New rules join `AllRules` (each owes a fixture): `mapping_missing_zero_semantics`, `mapping_rule_shape`, `shard_reference`.
- The rule-29 dispatch gap closes: a batch naming `mapping/2` or `coverage/2` validates, no exit-2 guess.
- No file I/O, no path resolution, no code loading; shard/mapping resolution stays with the publisher.
- Both sealed manifests `testdata/tokens/{codex,grok}/mapping.json` remain byte-identical and validate.

## D. Fixtures (positive | adversarial, one per rule)

- `mapping_valid_codex` / `mapping_valid_grok` | the two sealed manifests | schema + 12-member list + field_rules accept
- `mapping_schema_wrong` | schema `nova.tokens.mapping/1` | `unknown_schema`
- `mapping_missing_member` | 11 of 12 members | `missing_field`
- `mapping_unknown_member` | 13th member | `unknown_field`
- `mapping_zero_semantics_missing` | a field rule without `zero_semantics` | `mapping_missing_zero_semantics`
- `mapping_zero_semantics_unknown` | `zero_semantics` `"defaulted"` | `unknown_enum`
- `mapping_rule_shape_unknown` | source_kind with no shape, or a nested key not in its set | `mapping_rule_shape`
- `mapping_impl_id_unbuilt` | non-null id naming an unbuilt adapter | adoption refusal (publisher)
- `coverage_valid` | full 14-member body, sorted-unique IDs, `int` counts, one complete shard ref | coverage/2 accept
- `coverage_schema_wrong` | `nova.tokens.coverage/1` | `unknown_schema`
- `coverage_missing_member` | 13 of 14 | `missing_field`
- `coverage_unknown_member` | 15th | `unknown_field`
- `coverage_interval_null` | `interval.start` or `end` null | `interval_incomplete`
- `coverage_interval_order` | `end` <= `start` | `interval_order`
- `coverage_status_unknown` | status `"complete"` | `unknown_enum`
- `coverage_source_ids_unsorted` / `_duplicate` | out of order / repeated | `not_sorted` / `duplicate_element`
- `coverage_friend_underscore` | `collector_friend` `"_"` | `label_syntax` (null is the only unknown)
- `coverage_counts_integer` | a count as a raw number / `"007"` | `raw_json_number` / `integer_lexeme`
- `coverage_shard_missing_member` | shard ref missing `record_count` | `shard_reference`
- `coverage_shard_count_mismatch` | `record_count != len(observation_ids)` | `shard_reference`
- `coverage_shard_ids_unsorted` | `observation_ids` out of order | `not_sorted`
- `coverage_shard_corrupt` | shard bytes differ from `shard_id` | publisher referential, `reason=incomplete`

## E. Decisions that need Stella or Rowan

1. Nested-rule closure key: key per-adapter shapes on `identity_rule.source_kind`, or add a
   top-level `adapter` discriminator field? (source_kind / adapter field)
2. Shard inventory wire: inline sorted `observation_ids` only, or a disjoint tagged choice
   (`inline_ids` | `inventory_file`+digest)? (inline-only / tagged choice)
3. `implementation_id` final form: `<adapter>@<version> build=<build-id>`, or a bare registry id
   `<adapter>@<version>` with no build id? (with build id / registry id only)
4. `counts` member set: the five flat names above, or nested per-source counts (`by_source`) for
   stronger per-source visibility? (flat / per-source)
5. `reasons` metadata: `{code, source}` only, or `{code, source, note}` with a bounded note
   vocabulary? (two-member / three-member)
