# Grok retained-token mapping — proposed source contract

Based on owner-supplied export structure and invariant checks; Stella has not read Johnny's private session. Real numeric observations remain in the private coordination records; the public fixture below is synthetic. Producer reported earlier: `grok usage` v1.0.30. This is a review packet, not a deployed adapter.

**Source and grain.** Top-level keys are sessionId, updatedAt, session and turns. Each turn has turnNumber and endedAt; its usage is already an aggregate of model calls (`modelCalls`). Preserve each turn at that available grain. Do not invent per-call usage or a start time, and do not count the session totals in addition to its turn rows.

**Identity and revision.** Candidate spend key: `[source.namespace, [original_session_id, turn_number_as_string]]`, using the original native sessionId and exact decimal turn string. The event key is an array, never concatenated text. Copying the export to another bench does not change it. The export's updatedAt is collection/update evidence, not a new event key and not the turn's event time. Identical repeated turns count once. Changed numeric content for the same turn needs source revision/finality evidence or explicit supersession; newest filename/Git commit is not enough. Partial turns without endedAt are retained with partial coverage and are not assigned the export's date.

**Time.** Parse endedAt including its offset and retain it; the report's daily basis is turn completion in UTC. A long turn can contain calls from an earlier day. Completion-day attribution is a labelled accounting convention, not measured call-day allocation. A report requiring exact per-call dates must identify this gap. Best-effort September collection may still use these source turn records with that stated basis.

**Raw fields.** Preserve inputTokens, outputTokens, cachedReadTokens, cacheCreationTokens, reasoningTokens, totalTokens, modelCalls, costUsdTicks and turnCount, with original names and observed presence. Keep primaryModelId and the source's modelUsage detail without collapsing it. modelUsage is a map from model ID to the eight numeric usage/call/cost fields, without turnCount. The observed one-key case matches primaryModelId and turn totals; a mixed-model case is not yet verified. Unknown extra fields are not automatically copied into the shared ledger.

**Counting.** Johnny checked the supplied turn records and session aggregate: totalTokens=inputTokens+outputTokens, cachedReadTokens<=inputTokens, reasoningTokens<=outputTokens. This supports an inclusive-input/inclusive-output mapping for that observed shape; label its evidence basis as observed invariants until the producer contract independently confirms it. Retain totalTokens and both components, and never add cache or reasoning on top. Values violating the supported relation produce a mapping conflict rather than silent adjustment. Costs remain raw ticks with an unverified unit; no dollars are calculated. Where the export establishes that session totals and all retained turn rows cover the same complete interval, compare them as a checksum: mismatch is a mapping conflict, never a second spend row. A partial export cannot claim that completeness or use its session total to fill missing turn usage.

**Zero versus missing.** The sample always contained every token key, and cacheCreationTokens was always0. Preserve that as a raw present zero. The sample does not establish whether the producer defaulted an absent provider measurement; normalized detail zero remains unknown without the source contract. A missing key is distinct from raw0. Apply the same distinction to reasoning/cache-read zeros if encountered.

**Model and repository.** `primaryModelId` is a reported harness identifier; retain that value and `model_basis=harness_reported`. It must not replace all modelUsage entries in a mixed-model turn. A report using this available model identifier states its basis instead of requiring an independent server receipt to show any result. No repo field exists: `unattributed`, with an optional separately evidenced touched list carrying no allocated spend. Friend/bench binding describes original execution; current collector host is not historical evidence.

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

Expected totals under the proposed inclusive mapping: input1000, output100, total1100; cache-read800 and reasoning40 shown as subsets. Raw cache-creation0 is retained with the stated measurement uncertainty. Repository unattributed; model basis harness-reported; day basis UTC turn-completion. The mock cost tick value77 is uninterpreted. This minimal turn fixture omits optional surrounding export metadata and is not a copied private export.

Before adapter acceptance: confirm the identity/finality rule and mixed-model modelUsage behavior, independently read the arithmetic/basis/default handling, and test copied exports, changed same-turn values, missing fields, multiple models and a turn crossing midnight. No new model run is needed. Raw ingestion can preserve uncertain evidence without asserting a normalized field it cannot establish.

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
