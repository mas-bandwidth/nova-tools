# Codex Desktop retained-token fixtures (synthetic)

Every value here is invented. No private transcript was read, no real source value appears,
and the unsupported source fields deliberately carry privacy sentinels so a test can prove
they reach no record and no diagnostic.

The mapping decisions these fixtures encode are tabulated in
[docs/MAPPING-TOKENS-CODEX.md](../../MAPPING-TOKENS-CODEX.md) under "Wire literals
under the mapping decisions"; the decisions themselves are the source owner's on PR #142.

The fixture files themselves stay executable data and live in
[`testdata/tokens/codex/`](../../../testdata/tokens/codex); every file named below is in
that directory.

| File | What it is |
|---|---|
| `mapping.json` | the sealed `nova.tokens.mapping/2` manifest. Its envelope ID is the `mapping_id` of every expected observation, and `fixture_digests` names the SHA-256 of each source file below. |
| `source_rollout.jsonl` | synthetic rollout lines shaped on the named `TokenUsageRecord` fields: four cleanly mappable response records, four with an invalid supported counter (explicit null, a wrong-typed value holding a privacy sentinel, a negative count, a non-integer count), and a `token_count` context-fill snapshot that is owed to a separate mapping and mapped to nothing here. |
| `source_rollout_copy.jsonl` | the first response record copied byte for byte, plus one changed copy of it. |
| `expected_records.jsonl` | the expected sealed `nova.tokens.observation/2` envelopes, stated independently of any adapter: there is no adapter. Ten lines, nine distinct observations, eight spend keys, four of them carrying an unavailable counter and its completeness gap. |
| `refused_records.jsonl` | shapes the wire must refuse, each with the rule and field the landed validator must name. |

`internal/records/mapping_fixtures_test.go` runs the landed record validator over all of
it. An invalid supported counter is `unavailable`/`parse_failed` with the rest of its
observation intact; a coerced pass-through of the same value still refuses under the
unchanged core rules, which the refused fixtures assert. The `token_count` shape is owed to
its own mapping and is covered by nothing here.
