# Retained token validators: mapping/2 and coverage/2

Addendum to [PROPOSAL-TOKENS-FORMAT.md](PROPOSAL-TOKENS-FORMAT.md) and [PROPOSAL-TOKENS-RECORDS.md](PROPOSAL-TOKENS-RECORDS.md), in
FORMAT's register. It closes the two validators SPEC-TOKENS.md rule 29 names as missing: once implemented, a `--batch` naming them
validates instead of exiting 2 `refusing to guess`. This addendum is the specification of those validators, not the implementation — it
names the two bodies and their rules; it does not make today's dispatcher (which knows only `observation/2`) validate them. The envelope,
canonical content ID, duplicate-key / invalid-UTF-8 / raw-number refusal, `Refusal` shape and the `nova.tokens.observation/2` validator are
the model and are unchanged; only the two new bodies are specified. mapping/2 below derives from the two sealed manifests
`testdata/tokens/{codex,grok}/mapping.json`, whose bytes are preserved; coverage/2 is newly specified here and derives from no fixture.
Emma's records/Antigravity review of this finalized shape is the group's agreed checkpoint before implementation.

Legend: `str` non-empty string, `str?` string-or-null, `label` `[a-z0-9][a-z0-9-]{0,31}`, `field_key`
`[a-z0-9][a-z0-9_-]{0,63}` (a native metadata field key; underscore and hyphen legal), `ns`
`[a-z0-9][a-z0-9._-]{0,63}`, `cid` `sha256:<64 lowercase hex>`, `int` the lexeme `0|[1-9][0-9]*` always written
as a JSON string (no raw number), `ts` RFC 3339 with explicit offset or `Z`. Every object is closed: unknown key
`unknown_field`, missing key `missing_field`, wrong type `wrong_type`. No `interface{}`, no raw object, no raw
JSON number, no executable expression, no plugin loading. Policy strings are retained verbatim inside their typed slot and never parsed, normalized or executed.

## A. mapping/2

Envelope ID is the body's canonical SHA-256 (unchanged). The body is exactly these 12 members, all required
(present even when null):

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

**`field_rules`** maps each numeric source field name (`str`) to a closed rule object of exactly 8 members:
`number_kind` ∈ `{integer, decimal}`; `zero_semantics` ∈ `{measured, default_may_mask_absence, unknown}`
(required — absent => `mapping_missing_zero_semantics`); `unit` `[a-z0-9][a-z0-9_-]{0,31}`; `spend_role` ∈
`{base_counter, subset_detail, non_spend}`; `absent_presence` ∈ `{present, absent, unavailable}`;
`absent_reason` ∈ ReasonCodes; `invalid_presence` ∈ `{present, absent, unavailable}`; `invalid_reason` ∈
ReasonCodes. A present zero under `default_may_mask_absence` or `unknown` stays unmeasured (existing `Normalize`); `field_rules` names every numeric field, and `number_kind` is never inferred.

**The five rule objects are closed, typed, per reviewed adapter.** The adapter discriminator is the existing
`identity_rule.source_kind` ∈ `{codex_desktop, grok}` — the two SourceKinds with enumerated shapes — and there is
no new top-level adapter member. A member value may be only `bool`, `str`, `null`, `[str]`, or a nested object
whose keys are enumerated below. Policy strings are retained verbatim inside that shape; the validator checks type and, where stated, enum membership and list sort only. Any key outside the adapter's set, or any other value type, is `mapping_rule_shape` — an explicit refusal, never an open object and never a bypass.

- `identity_rule` (both): `source_kind` ∈ SourceKinds; `namespace` `ns`; `event_key` `[str]` non-empty,
  positional (NOT sorted); `observation_kind` ∈ `{request,turn,snapshot,aggregate}`; `normalized_spend_supported`
  `bool`; `producer_version_from` `str`; `session_id` `str`; `receipt_fields` `[field_key]` sorted unique;
  `receipt_value_type` = `"string"`. codex adds `containing_or_forked_session_in_event_key` `bool`. grok adds
  `collection_timestamp_substitution` `bool`, `containing_session_substitution` `bool`, `unsupported_reason` `str`.
- `revision_rule` (identical in both): `basis` ∈ `{source_order, operator_correction, none}`; `native` `str?`;
  `supersedes` `[cid]` sorted unique; `identical_copy` `str`; `changed_same_key` `str`;
  `inferred_order_from_export_update_time` `bool`; `newest_wins` `bool`.
- `time_rule` (both): `basis` ∈ timeBases; `occurred_at` `str`; `offsets` `str`; `day_allocation` `str`; `start`
  `str?`; `end` `str?`; `missing_timestamp` `{basis ∈ timeBases, occurred_at str?}` (null in both sealed
  manifests). grok adds `cross_midnight` `str`.
- `model_rule` — every nested `{basis, id}` object is `{basis ∈ modelBases (str), id str?}`, `id` null-legal where
  a manifest names an absent/ambiguous model and non-null where it names one. codex: `basis` ∈ modelBases; `id`
  `str`; `absent` `{basis, id null}`; `later_model_relabels_earlier_responses` `bool`; `forbidden_wire_keys`
  `[str]` sorted unique; `model_usage` `[]`. grok: `single_reported_id` `{basis, id}`; `absent_id`
  `{basis, id null}`; `multiple_model_ids` `{basis, id null}`; `split_normalized` `bool`;
  `aggregate_is_counting_candidate` `bool`; `detail_retained_not_counted_again` `bool`; `model_usage_fields`
  `[str]` positional (NOT sorted); `model_usage_sorted_by` `str`; `forbidden_wire_keys` `[str]` sorted unique.
- `overlap_rule` codex: `counting_source` `str`; `arithmetic_mismatch` `str`; `missing_raw_total` `str`;
  `thread_token_usage` `str`; `turn_token_usage` `str`; `token_count_snapshots` `str`;
  `summed_or_mixed_with_preferred` `bool`; `report_must_name_unhandled_token_count_coverage` `bool`;
  `owed_coverage_tasks` `[str]` positional (NOT sorted). grok: `counting_grain` `str`; `arithmetic_violation`
  `str`; `evidenced_overlapping_grain` `str`; `request_grain_mapping` `str`; `session_totals` `str`;
  `forbidden_unions` `[str]` positional (NOT sorted); `checksum_requires_evidenced_complete_matching_coverage`
  `bool`; `session_totals_fill_missing_turn_spend` `bool`; `owed_coverage_tasks` `[str]` positional (NOT sorted).

`timeBases`, `modelBases`, `SourceKinds` and `ReasonCodes` are the existing shared definitions in
`internal/records/allowlist.go`, not new per-mapping sets: `timeBases` `{response_observation, turn_completion,
measured_interval, source_aggregate, unknown}` and `modelBases` `{provider_reported, harness_reported, requested,
mixed, unknown}` are the unexported `set(...)` values in that file's `var` block (beside `observationKinds`,
`revisionBases`, `originBases`, `repoBases`, `presences`, `numberKinds`); `SourceKinds` and `ReasonCodes` are the exported slices in the same file.

`receipt_fields` uses the `field_key` grammar, which admits the underscore names both sealed manifests carry
(`response_id`, `turn_id` for codex; `turn_number` for grok); the label grammar would refuse them, so receipt
fields are field keys, never labels. Both unchanged manifests are intended to validate under this grammar once implemented, and
`go test ./internal/records/...` passes with the fixtures unchanged.

**`implementation_id`.** Null is legal for a proposal. The final non-null form is
`<adapter>@<version> build=<build-id>`, bounded: `<adapter>` is a `label`; `<version>` is `[a-z0-9._-]{1,64}`;
`build=<build-id>` where `<build-id>` is `[A-Za-z0-9._-]{1,64}` — an assigned identity (adapter label, version
label, the binary's compiled-in build id), NOT a digest of the adapter source and NOT a digest of any fixture it
names, so there is no code↔fixture hash cycle. Structural validity of that string is one check; whether the named
adapter/version is a supported, built, registry-adopted implementation is a separate, publisher-side check.
Adoption/normalization refuses a non-null id naming an unsupported or unbuilt implementation; historical proposals (null) are preserved, never rewritten.

## B. coverage/2

The body is exactly these 14 members, all required:

| member | type / value |
|---|---|
| `schema` | `str` = `"nova.tokens.coverage/2"` |
| `scope_id` | `ns` — a declared metadata scope, never a private path |
| `source_ids` | `[ns]` sorted unique, non-empty; each a source-scope binding id, see below |
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

Unknown friend/bench is **null in the body**; `_` is only the ledger-path spelling of null and fails the label
grammar, so it can never be a body value and no real label aliases the unknown partition. `interval.start`/`end`
are required RFC 3339 with start < end and an exclusive end (`interval_incomplete` / `interval_order`); a live
source cannot become `complete_within_scope` merely because the shape is valid. `collected_at` is collection
provenance, distinct from the inspected interval and the spend day.

**Source-ID domain and `reasons`.** `source_ids` and `reasons.source` share one source-ID domain: a source-scope
binding id written in the `ns` grammar — the id that binds a declared source to its identity/scope, never a
private path. A non-null `reasons.source` MUST be a member of `source_ids` (`coverage_reason_source`); null means
the reason is scope-wide, not tied to one source. `reasons` is an array of exactly-two-member objects
`{code, source}`: `code` ∈ the closed set `{source_unavailable, unsupported_rows, unknown_fields,
partial_interval, conflict}`; `source` `ns?` (null when scope-wide). The array is sorted by `(code, source)` and
duplicates are refused (`not_sorted` / `duplicate_element`). Compare strings by UTF-8 bytes; for equal codes, null source sorts before every string source. Fixed codes with bounded metadata, never a free-form note.

**`shards`**: each shard-ref is a closed object carrying exactly `shard_id`, `record_count`, and a disjoint tagged
inventory — exactly one of two branches, never both and never neither (`shard_reference`):

- `shard_id` `cid` — the shard file's byte SHA-256, which is also its ledger path name;
- `record_count` `int` (string lexeme) ≥ 1 — the exact number of observation envelopes;
- `inline_ids` `[cid]` sorted unique — the exact envelope IDs, in order (the `inline_ids` branch), or
- `inventory_file` `cid` — the byte digest of a referenced inventory file containing the same sorted-unique
  observation-ID list (the `inventory_file` branch).

An inventory file is the RFC8785 canonical JSON array of `cid` strings followed by exactly one LF, at
`inventories/<inventory-sha256-hex>.json`. Its byte digest includes that LF. It contains at least one ID, sorted
unique by UTF-8 bytes, and contains no envelope or record bodies. This is the same list carried by `inline_ids`.
`shards` is sorted unique by `shard_id`; duplicate references are refused (`duplicate_element`) rather than counted twice.

Structural equation (in-memory, `shard_reference`): on the `inline_ids` branch, `record_count ==
len(inline_ids)`; on the `inventory_file` branch the count and ID set live in the referenced file, so the records
validator checks the shape and digest grammar only, and the publisher resolves the file — opens it, verifies its
byte SHA-256 == `inventory_file`, resolves and hashes the referenced JSONL shard on either branch, checks its record count,
and checks that its sorted ID set equals the inline or file inventory — outside
the no-I/O records package. This is an inventory CONTRACT (shape + equation), not an inventory service; nothing here opens a shard or an inventory file.

**`counts`**: five flat counts, kept for now, as exact nonnegative `int` STRING lexemes. Each is a count the
collection actually observed or produced, never an assertion of zero for unmeasured source activity:
`source_candidates` (source candidate records inspected), `records_emitted` (observation envelopes emitted into
shards), `observations` (distinct observation IDs after dedup), `conflicts` (unresolved same-key conflicts
excluded from spend), `gaps` (named coverage gaps, == `len(reasons)`). Each of the five is produced by a named
collection step and is therefore always measurable, so none needs a nullable slot; a source whose activity is
unknown is expressed by a `reason` (`source_unavailable`) and a `status` of `partial`/`unavailable`, never by
zeroing a count. Where results differ per source, coverage is scoped per source through `reasons.source` naming
the specific `source_ids` member, not by replacing the flat counts. Provable: `gaps == len(reasons)` (structural);
`records_emitted == Σ record_count` and `observations == |∪ observation_ids|` are pipeline-provable (publisher/view), not structural.

## C. Validator contract (mirrors observation/2)

- Constants `SchemaMapping` / `SchemaCoverage` beside `SchemaObservation`; dispatch switches on `body.schema`.
- `Envelope` gains `Mapping *Mapping` and `Coverage *Coverage` (one non-nil); `ValidateEnvelope` unchanged.
- Plain-data types `Mapping{...}`, `Coverage{...}`; sub-shapes `MappingFieldRule`, `ShardRef`, `CoverageInterval`, `CoverageReason`.
- `validateMapping` / `validateCoverage` use `exactKeys`, `enumField`, `stringField`/`nullableString`/`nullableLabel`, `contentIDField`, `sortedUnique`, `timestamp`, integer lexeme.
- `SealMapping` / `SealCoverage` seal then strict-read-back through `ValidateEnvelope` (no skip set).
- `Refusal{Rule, Field, Note}` unchanged; one bounded line, field name only, never the value.
- New rules join `AllRules` (each owes a fixture): `mapping_missing_zero_semantics`, `mapping_rule_shape`, `shard_reference`, `coverage_reason_source`. The rule-29 dispatch gap closes once this is implemented — a batch naming `mapping/2` or `coverage/2` validates, no exit-2 guess — and until then the dispatcher still refuses them; this addendum does not flip it.
- No file I/O, no path resolution, no code loading; shard/inventory/mapping resolution stays with the publisher.
- Both sealed manifests `testdata/tokens/{codex,grok}/mapping.json` remain byte-identical and validate; no fixture bytes change.

## D. Fixtures (positive | adversarial, one per rule)

mapping/2:
- `mapping_valid_codex` / `mapping_valid_grok` | the two sealed manifests | schema + 12-member list + field_rules accept
- `mapping_schema_wrong` | `nova.tokens.mapping/1` | `unknown_schema`
- `mapping_missing_member` / `mapping_unknown_member` | 11 of 12 members / a 13th member | `missing_field` / `unknown_field`
- `mapping_zero_semantics_missing` / `_unknown` | a field rule without `zero_semantics` / `"defaulted"` | `mapping_missing_zero_semantics` / `unknown_enum`
- `mapping_rule_shape_unknown` | source_kind with no shape, or a nested key not in its set | `mapping_rule_shape`
- `mapping_impl_id_unbuilt` | non-null id naming an unbuilt adapter | adoption refusal (publisher)

coverage/2 positive (complete, every member):

```json
{"schema":"nova.tokens.coverage/2","scope_id":"nova.codex-desktop.responses","source_ids":["nova.codex-desktop.responses"],
"interval":{"start":"2026-09-12T00:00:00Z","end":"2026-09-13T00:00:00Z"},"status":"complete_within_scope","reasons":[],
"collected_at":"2026-09-13T01:00:00Z","collector_build":"codex-desktop@0.154.0 build=abc123","collector_friend":null,"collection_bench":null,
"mapping_ids":["sha256:34a1189b7cf58e352f2dd4da8ea4e1076894a933bb9e3b1391082b3ccc2a2c7d"],
"shards":[{"shard_id":"sha256:1111111111111111111111111111111111111111111111111111111111111111","record_count":"2",
"inline_ids":["sha256:2222222222222222222222222222222222222222222222222222222222222222","sha256:3333333333333333333333333333333333333333333333333333333333333333"]}],
"predecessors":[],"counts":{"source_candidates":"2","records_emitted":"2","observations":"2","conflicts":"0","gaps":"0"}}
```

coverage/2 adversarial:

| fixture | what it holds | rule |
|---|---|---|
| `coverage_valid` | full 14-member body, one inline shard ref, sorted-unique IDs, int-string counts | accept |
| `coverage_schema_wrong` | `nova.tokens.coverage/1` | `unknown_schema` |
| `coverage_missing_member` / `_unknown_member` | 13 of 14 members / a 15th member | `missing_field` / `unknown_field` |
| `coverage_interval_null` / `_order` | `interval.start` or `end` null / `end` ≤ `start` | `interval_incomplete` / `interval_order` |
| `coverage_status_unknown` | status `"complete"` | `unknown_enum` |
| `coverage_source_ids_unsorted` / `_duplicate` / `_path` | out of order / repeated / a private path | `not_sorted` / `duplicate_element` / `namespace_syntax` |
| `coverage_reason_source_not_in_scope` | `reasons.source` names an id not in `source_ids` | `coverage_reason_source` |
| `coverage_reason_source_label` | a syntactically valid friend label absent from `source_ids` (labels also fit `ns`) | `coverage_reason_source` |
| `coverage_reasons_unsorted` / `_duplicate` | not sorted by (code, source) / a repeated `{code, source}` | `not_sorted` / `duplicate_element` |
| `coverage_reason_code_unknown` | `code` outside the five | `unknown_enum` |
| `coverage_friend_underscore` | `collector_friend` `"_"` | `label_syntax` (null is the only unknown) |
| `coverage_counts_integer` | a count as a raw number / `"007"` | `raw_json_number` / `integer_lexeme` |
| `coverage_shard_missing_member` | shard ref missing `record_count` | `shard_reference` |
| `coverage_shard_count_mismatch` | `record_count != len(inline_ids)` | `shard_reference` |
| `coverage_shard_both` / `_neither` | both `inline_ids` and `inventory_file` / neither | `shard_reference` |
| `coverage_shard_inline_unsorted` | `inline_ids` out of order | `not_sorted` |
| `coverage_shard_inventory_syntax` | `inventory_file` not a `cid` | `content_id_syntax` |
| `coverage_shards_unsorted` / `_duplicate` | shard refs out of digest order / repeated digest | `not_sorted` / `duplicate_element` |
| `coverage_inventory_file_encoding` | noncanonical JSON array, missing or extra LF, or unsorted/duplicate IDs | publisher referential, `reason=incomplete` |
| `coverage_shard_corrupt` | shard bytes differ from `shard_id` | publisher referential, `reason=incomplete` |

## E. Decided

- Decisions 1–5 and the five consistency repairs above: Stella (2026-09-13), after reading the complete addendum and both sealed fixtures.
- Review of this finalized shape before implementation: Emma (records/Antigravity) — the group's agreed review checkpoint.
- No collector or publisher implementation is bundled into this proposal.
