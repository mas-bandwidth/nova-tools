RESULT tools22-rule-toolwork-3-L656 sha=5298f6be12ea
GAP internal/pulse/harvest_result.go:205 internal/decide/harvestclass.go:247
SPEC docs/SPEC-TOOLWORK.md:656 rule 3
PKG internal/decide, internal/pulse
ASK The pulse harvest must route each finished job through the Jev boundary: build state from OUTCOME fields only (Accept, Line2, reason token), ask only when NeedsProvider is true, classify via ClassifyHarvest to produce class/confidence, write class=/conf= back to OUTCOME and append to outcomes.jsonl, and set class=unknown with requeue-once below the floor.

The Jev boundary is fully implemented but never wired into execution:

internal/decide/harvestclass.go:155 -- NeedsProvider gates which shapes need the provider (accept=abstain, accept=none with BLOCKED/ABSTAIN line2)
internal/decide/harvestclass.go:175 -- HarvestState builds framed state from three bounded OUTCOME fields, never from RESULT.md prose
internal/decide/harvestclass.go:247 -- ClassifyHarvest applies the boundary: gate-decided => rules answer; below-floor => unknown+requeue; at-floor => chain's answer as class
internal/decide/harvestclass.go:311 -- Pushes/LiftsHold/SkipsRead always read the GATE verdict, never the class (§4 rule 4)
internal/decide/harvestclass.go:329 -- AppendOutcomeRow writes a JSONL row with class=/conf= per §4 rule 5

None of these functions are called from any production code path. A grep for `ClassifyHarvest` and `AppendOutcomeRow` across all non-test Go files finds them only in harvestclass.go (definition) and nowhere else. The actual decision flow lives in internal/pulse/harvest_result.go:205 (`func (in HarvestInput) decideHarvest`) which calls `in.Decider.Decide()` directly using state built from RESULT.md text (harvestClassState, harvestResultEvidence), completely bypassing the OUTCOME-based boundary.

UNGUARDED. The red tests for rule 3 live entirely within harvestclass_test.go:
- TestDecideStateIsBuiltFromOutcomeOnly (line 31) -- proves state contains only bounded fields
- TestAcceptRejectIsClassRejectedNeverFailedAndNoProviderIsAsked (line 87) -- gate decided => no provider
- TestAHarvestClassNeverPushesARejectedCard (line 129) -- classification never grants push/lift/skip
- TestBelowFloorIsUnknownAndRequeuesOnce (line 162) -- below floor => unknown + requeue
- TestEveryOutcomeIsOneAppendedJSONLRow (line 197) -- every outcome appended to JSONL
- TestTheProviderIsNeverCalledWhereTheGateAlreadyDecided (line 338) -- no-provider negative control
- TestTheCapturedRequestCarriesNoProseAndNoOutputTail (line 366) -- nothing private crosses

All pass, but they test isolated units, not the end-to-end integration where harvest actually produces an OUTCOME line with class=/conf= or appends to outcomes.jsonl.

Rule 3(a) -- state from OUTCOME, never RESULT.md prose: implemented in HarvestState(), violated in harvestClassState()/harvestResultEvidence() which send RESULT.md first-line, BRANCH, commits, files, red:, and OUTPUT TAIL to the provider.

Rule 3(b) -- answer lands in class=/conf= on OUTCOME and route log outcome: neither occurs. AppendOutcomeRow is dead code. No OUTCOME line with class=/conf= is written anywhere.

Rule 3(c) -- below-floor class=unknown + requeue-once: ClassifyHarvest does this correctly, but it is never invoked during harvest execution.

Left owed: wire the harvest execution path (pulse.Harvest > decideHarvest) through the Jev boundary (HarvestEvidence -> NeedsProvider -> HarvestState -> Chain.Classify -> ClassifyHarvest -> AppendOutcomeRow + class output).

git status --short
