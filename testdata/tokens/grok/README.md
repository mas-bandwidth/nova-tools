# Grok turn-export retained-token fixtures (synthetic)

Every value here is invented. Real numeric observations stay in the private coordination
records; nothing was copied from a private export. The unsupported source fields
deliberately carry privacy sentinels so a test can prove they reach no record and no
diagnostic.

The mapping decisions these fixtures encode are tabulated in
[docs/MAPPING-TOKENS-GROK.md](../../../docs/MAPPING-TOKENS-GROK.md) under "Wire literals
under the mapping decisions"; the decisions themselves are the source owner's on PR #142.

| File | What it is |
|---|---|
| `mapping.json` | the sealed `nova.tokens.mapping/2` manifest. Its envelope ID is the `mapping_id` of every expected observation, and `fixture_digests` names the SHA-256 of each source file below. |
| `source_export.json` | one export with all four declared top-level keys and five turns: single model, mixed model, missing fields with no `endedAt`, all-zero counters, and one turn carrying an explicit null, a wrong-typed sentinel value and a negative count in both the turn and its `modelUsage` entry. Its `session` totals stay visible as evidence owed to a separate aggregate mapping. |
| `source_export_copy.json` | the first turn copied byte for byte to another bench. |
| `source_export_changed.json` | the same session and turn number with changed counters. |
| `expected_records.jsonl` | the expected sealed `nova.tokens.observation/2` envelopes, stated independently of any adapter. Seven lines, six distinct observations, five spend keys. |
| `refused_records.jsonl` | shapes the wire must refuse, each with the rule and field the landed validator must name. |

`internal/records/mapping_fixtures_test.go` runs the landed record validator over all of
it. Turn identity stability is unverified, so the manifest declares normalized spend
unsupported for this key and the test asserts that declaration rather than a spend total. The
session aggregate and any future request-grain mapping are owed separately: neither is
covered here, and neither may fill a missing turn.
