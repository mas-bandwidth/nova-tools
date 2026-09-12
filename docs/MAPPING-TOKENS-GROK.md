# Grok retained-token mapping — proposed source contract

Based on owner-supplied export structure and invariant checks; Stella has not read Johnny's private session. Real numeric observations remain in the private coordination records; the public fixture below is synthetic. Producer reported earlier: `grok usage` v1.0.30. This is a review packet, not a deployed adapter.

**Source and grain.** Top-level keys are sessionId, updatedAt, session and turns. Each turn has turnNumber and endedAt; its usage is already an aggregate of model calls (`modelCalls`). Preserve each turn at that available grain. Do not invent per-call usage or a start time, and do not count the session totals in addition to its turn rows.

**Identity and revision.** Candidate spend key: `[source.namespace, [original_session_id, turn_number_as_string]]`, using the original native sessionId and exact decimal turn string. The event key is an array, never concatenated text. Copying the export to another bench does not change it. The export's updatedAt is collection/update evidence, not a new event key and not the turn's event time. Identical repeated turns count once. Changed numeric content for the same turn needs source revision/finality evidence or explicit supersession; newest filename/Git commit is not enough. Partial turns without endedAt are retained with partial coverage and are not assigned the export's date.

**Time.** Parse endedAt including its offset and retain it; the report's daily basis is turn completion in UTC. A long turn can contain calls from an earlier day. Completion-day attribution is a labelled accounting convention, not measured call-day allocation. A report requiring exact per-call dates must identify this gap. Best-effort September collection may still use these source turn records with that stated basis.

**Raw fields.** Preserve inputTokens, outputTokens, cachedReadTokens, cacheCreationTokens, reasoningTokens, totalTokens, modelCalls, costUsdTicks and turnCount, with original names and observed presence. Keep primaryModelId and the source's modelUsage detail without collapsing it. modelUsage is a map from model ID to the eight numeric usage/call/cost fields, without turnCount. The observed one-key case matches primaryModelId and turn totals; a mixed-model case is not yet verified. Unknown extra fields are not automatically copied into the shared ledger.

**Counting.** Johnny checked the supplied turn records and session aggregate: totalTokens=inputTokens+outputTokens, cachedReadTokens<=inputTokens, reasoningTokens<=outputTokens. This supports an inclusive-input/inclusive-output mapping for that observed shape; label its evidence basis as observed invariants until the producer contract independently confirms it. Retain totalTokens and both components, and never add cache or reasoning on top. Values violating the supported relation produce a mapping conflict rather than silent adjustment. Costs remain raw ticks with an unverified unit; no dollars are calculated. Where the export establishes that session totals and all retained turn rows cover the same complete interval, compare them as a checksum: mismatch is a mapping conflict, never a second spend row. A partial export cannot claim that completeness or use its session total to fill missing turn usage.

**Zero versus missing.** The sample always contained every token key, and cacheCreationTokens was always 0. Preserve that as a raw present zero. The sample does not establish whether the producer defaulted an absent provider measurement; normalized detail zero remains unknown without the source contract. A missing key is distinct from raw 0. Apply the same distinction to reasoning/cache-read zeros if encountered.

**Model and repository.** `primaryModelId` is a reported harness identifier; retain that value as `model.id` with `model.basis=harness_reported` (`model_basis` is not a key of this wire). It must not replace all modelUsage entries in a mixed-model turn. A report using this available model identifier states its basis instead of requiring an independent server receipt to show any result. No repo field exists: `unattributed`, with an optional separately evidenced touched list carrying no allocated spend. Friend/bench binding describes original execution; current collector host is not historical evidence.

**Synthetic numeric fixture (invented values, not actual usage):**

```json
{
  "sessionId": "fixture-grok-session",
  "turns": [
    {
      "turnNumber": 1,
      "endedAt": "2026-09-12T00:05:00Z",
      "inputTokens": 1000,
      "outputTokens": 100,
      "cachedReadTokens": 800,
      "cacheCreationTokens": 0,
      "reasoningTokens": 40,
      "totalTokens": 1100,
      "modelCalls": 2,
      "costUsdTicks": 77,
      "turnCount": 1,
      "primaryModelId": "grok-model-example",
      "modelUsage": {
        "grok-model-example": {
          "inputTokens": 1000,
          "outputTokens": 100,
          "cachedReadTokens": 800,
          "cacheCreationTokens": 0,
          "reasoningTokens": 40,
          "totalTokens": 1100,
          "modelCalls": 2,
          "costUsdTicks": 77
        }
      }
    }
  ]
}
```

Expected totals under the proposed inclusive mapping: input 1000, output 100, total 1100; cache-read 800 and reasoning 40 shown as subsets. Raw cache-creation 0 is retained with the stated measurement uncertainty. Repository unattributed; model basis harness-reported; day basis UTC turn-completion. The mock cost tick value 77 is uninterpreted. This minimal turn fixture omits optional surrounding export metadata and is not a copied private export.

Before adapter acceptance: confirm the identity/finality rule and mixed-model modelUsage behavior, independently read the arithmetic/basis/default handling, and test copied exports, changed same-turn values, missing fields, multiple models and a turn crossing midnight. No new model run is needed. Raw ingestion can preserve uncertain evidence without asserting a normalized field it cannot establish.

## Wire literals under the mapping decisions

Every cell below is the source owner's decision on this PR (Stella, comment
[5647652764](https://github.com/mas-bandwidth/nova-tools/pull/142#issuecomment-5647652764)).
A cell her decision does not reach reads `UNDECIDED (Stella)` rather than a guess, and the
open cells are listed together at the end of this section. Where an earlier paragraph of
this document defers a question these tables decide, the tables are the decision.

The executable form is `testdata/tokens/grok/`, checked by the landed publisher boundary in
`internal/records/mapping_fixtures_test.go` (`TestGrokRetainedMappingFixtures`).

### The observation envelope, key by key

| Wire key | Literal |
|---|---|
| `schema` | `nova.tokens.observation/2` |
| `source.kind` | `grok` |
| `source.namespace` | `nova.grok.turns` |
| `source.producer_version` | the export's reported producer version (`grok usage v1.0.30` for the read sample; the fixture carries `1.0.30`); null when absent |
| `source.session_id` | the original native `sessionId`; a containing session ID is never substituted |
| `source.event_key` | `[original_session_id, turn_number_as_string]`, an array of strings, never concatenated text |
| `kind` | `turn` |
| `revision` | `{native: null, supersedes: [], basis: none}`. The export's `updatedAt` is collection evidence: it is neither the event time nor a source order. |
| `time.occurred_at` | `endedAt`, offset and precision preserved; null when the turn has none |
| `time.basis` | `turn_completion`; `unknown` when `endedAt` is absent |
| `time.start`, `time.end` | null: the source supplies no measured interval |
| `origin` | `{friend, bench, basis: owner_binding, binding_id}` with an explicit stable source-binding ID and the supplied original friend/bench; otherwise `{null, null, unknown, null}` |
| `model.id` / `model.basis` | one reported model ID, compatible with the turn: that ID with `harness_reported`. More than one model ID in `modelUsage`: `{id: null, basis: mixed}`. No reported ID: `{id: null, basis: unknown}`. `model_basis` and `requested_model` are never emitted as wire keys. |
| `repository` | `{id: null, basis: unattributed, policy_id: null, touched: []}` absent a separately evidenced policy |
| `raw_usage` | the closed nine-field allowlist below, under the original camelCase source names |
| `model_usage` | the source `modelUsage` split, one entry per source model ID, sorted by that ID, each entry carrying the eight numeric fields (everything except `turnCount`) under the same presence, type, unit and zero rules; `[]` when the source supplies no split |
| `mapping_id` | the content ID of the sealed mapping manifest (`testdata/tokens/grok/mapping.json`) |
| `receipt` | `turn_number` only, as a string |

### `raw_usage`: the per-mapping allowlist

This is the mapping's own closed set of original source names, not the landed
`internal/records/testdata/allowlists.json` vocabulary, which is a fixture input to the core
tests rather than a global field vocabulary.

| Source field | `number_kind` | `unit` | `zero_semantics` | Role | In `model_usage` |
|---|---|---|---|---|---|
| `inputTokens` | `integer` | `tokens` | `unknown` | base counter | yes |
| `outputTokens` | `integer` | `tokens` | `unknown` | base counter | yes |
| `totalTokens` | `integer` | `tokens` | `unknown` | base counter | yes |
| `cachedReadTokens` | `integer` | `tokens` | `unknown` | subset of input | yes |
| `cacheCreationTokens` | `integer` | `tokens` | `unknown` | subset of input | yes |
| `reasoningTokens` | `integer` | `tokens` | `unknown` | subset of output | yes |
| `modelCalls` | `integer` | `calls` | `unknown` | not token spend | yes |
| `costUsdTicks` | `decimal` | `usd_ticks` | `unknown` | not token spend | yes |
| `turnCount` | `integer` | `turns` | `unknown` | not token spend | no |

`zero_semantics` is `unknown` for all nine until the producer's semantics are verified: a
raw present zero is preserved and normalizes to nothing. A missing supported field is
`presence: absent`, `value: null`, `reason: not_supplied`. `costUsdTicks` keeps its exact
decimal lexeme — the sample's `77` is retained as the string `"77"` — with no dollars
conversion and no guessed unit. The outcome for an explicit null or non-numeric counter is
`UNDECIDED (Stella)`.

### Identity, overlap and mixed models

| Question | Rule |
|---|---|
| normalized spend from this key | unsupported until resume/fork/renumber stability is evidenced or an explicit original-source binding resolves it; raw retention proceeds meanwhile, and the gap is reported rather than smoothed |
| a copied export | identical copied keys deduplicate; the copy is the same observation |
| the same key with changed content | a conflict, retained and excluded from spend; there is no newest-wins rule and no filename or commit order |
| a turn with no `endedAt` | retained with partial coverage; never assigned the export's date or a collection timestamp |
| session totals vs turn rows | a scoped checksum only, never a second spend row; a mismatch is a mapping conflict |
| a request-grain Grok mapping | not established by a generic request/decimal fixture; that fixture is structural evidence, not a producer contract. Its literals are `UNDECIDED (Stella)`. |
| evidenced request-level overlap over the same session/interval | the view selects one evidenced source and grain; no union of request plus turn plus session totals |
| more than one model ID | `model.basis = mixed`; the turn aggregate is the counting candidate and the split is retained, never counted again. No model split is normalized until its source relationship is verified. |

### Day allocation

A known completion instant shards by its UTC day, whatever its source offset: the fixture's
turn completing at `2026-09-11T23:30:00-04:00` allocates to `2026-09-12`. An unknown instant
is `unallocated`. A completion-day report labels that convention and never claims exact
call-day allocation for a turn spanning midnight.

### Open cells

1. The outcome and reason code for an explicit null or non-numeric counter.
2. Whether the session aggregate is itself retained as a `kind: aggregate` observation; the
   decision fixes only that it is a checksum and never a second spend row.
3. The `namespace`, `kind` and `event_key` of a future request-grain Grok mapping.
4. Whether `producer_version` carries the bare version (`1.0.30`) or the full reported
   producer string.

### The fixtures

| File | Contents |
|---|---|
| `mapping.json` | the sealed `nova.tokens.mapping/2` manifest; its ID is the `mapping_id` of every expected envelope, and it names the digest of each source file |
| `source_export.json` | one export with four turns: single model, mixed model, missing fields with no `endedAt`, and all-zero counters; each turn also carries unsupported fields holding privacy sentinels |
| `source_export_copy.json` | the first turn copied byte for byte to another bench |
| `source_export_changed.json` | the same session and turn number with changed counters |
| `expected_records.jsonl` | six sealed observation envelopes: four spend keys, one exact duplicate that deduplicates, one changed copy that conflicts |
| `refused_records.jsonl` | four shapes the wire must refuse, each with its rule and field: a source field outside the allowlist, a receipt field outside it, a missing entry of the closed nine, and `costUsdTicks` declared as an integer |

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
