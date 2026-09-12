# Codex Desktop retained-token fixtures (synthetic)

Every value here is invented. No private transcript was read, no real source value appears,
and the unsupported source fields deliberately carry privacy sentinels so a test can prove
they reach no record and no diagnostic.

The mapping decisions these fixtures encode are tabulated in
[docs/MAPPING-TOKENS-CODEX.md](../../../docs/MAPPING-TOKENS-CODEX.md) under "Wire literals
under the mapping decisions"; the decisions themselves are the source owner's on PR #142.

| File | What it is |
|---|---|
| `mapping.json` | the sealed `nova.tokens.mapping/2` manifest. Its envelope ID is the `mapping_id` of every expected observation, and `fixture_digests` names the SHA-256 of each source file below. |
| `source_rollout.jsonl` | synthetic rollout lines shaped on the named `TokenUsageRecord` fields: four mappable response records, a non-integer counter, an explicit-null counter, and a `token_count` context-fill snapshot that belongs to a separate unsupported-for-spend mapping. |
| `source_rollout_copy.jsonl` | the first response record copied byte for byte, plus one changed copy of it. |
| `expected_records.jsonl` | the expected sealed `nova.tokens.observation/2` envelopes, stated independently of any adapter: there is no adapter. Six lines, five distinct observations, four spend keys. |
| `refused_records.jsonl` | shapes the wire must refuse, each with the rule and field the landed validator must name. |

`internal/records/mapping_fixtures_test.go` runs the landed publisher boundary over all of
it. A source shape whose wire outcome the owner has not decided produces no expected record;
it is named in the document's open cells instead.
