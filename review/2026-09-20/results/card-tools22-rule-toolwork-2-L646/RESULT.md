RESULT tools22-rule-toolwork-2-L646 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 2 says?
GAP internal/decide/harvestclass.go:252-259
SPEC docs/SPEC-TOOLWORK.md:646 rule 2
PKG internal/decide
ASK When the gate decides (accept=ok or accept=reject), the implementation must assign class=fixed or class=rejected respectively with conf=-, never ask a model, keep rejected distinct from failed, push nothing for rejected cards, and never offer rejected to any provider.

The code satisfies the structural requirements (no model called, rejected separate from failed, no push for rejected, rejected excluded from AskedHarvestClasses) but uses different values from what the rule specifies:

  internal/decide/harvestclass.go:252: `c.Class = ClassClean`  
    Rule says `class=fixed` for accept=ok; code sets `class=clean`.

  internal/decide/harvestclass.go:247: `c := Classification{Confidence: res.Confidence, ...}`  
    Rule says `conf=-` for gate-decided cases; code uses `res.Confidence` (default 0.0, and when called from `ClassifyHarvest` with `Result{}` the field is indeed 0.0).

The mapping for accept=reject (`ClassRejected = "rejected"`) matches the spec.

GUARDED-BY internal/decide/harvestclass_test.go:87 TestAcceptRejectIsClassRejectedNeverFailedAndNoProviderIsAsked
(The test passes and confirms the code's values — but the test itself uses `ClassClean` not `class=fixed`, so it guards the current implementation, not the spec.)

GREPS USED:
  grep -rn "rejected" --include='*.go' .
  grep -rn 'accept=ok|accept=reject|class=fixed' --include='*.go' .
  grep -rn "OUTCOME|class=fixed" --include='*.go' internal/
  grep -rn "OUTCOME.*class=" --include='*.go' .
  grep -rn '"OUTCOME "' --include='*.go' internal/
  grep -rn "class=clean|class=rejected" --include='*.go' .
  grep -rn "conf=-" --include='*.go' internal/decide/
  grep -rn "fixed\|conf.*-" internal/decide/harvestclass.go
  grep -rn "func Test" internal/decide/harvestclass_test.go

FILES READ:
  docs/SPEC-TOOLWORK.md (lines 626-672)
  internal/decide/harvestclass.go (full, 361 lines)
  internal/decide/harvestclass_test.go (full, 500 lines)
  internal/pulse/harvest_working.go (lines 1-60, 277-324)
  internal/pulse/harvest_decide.go (full, 143 lines)
  internal/pulse/harvest_result.go (full, 279 lines)
  internal/pulse/harvest.go (lines 90-269)
  cmd/nova-decide/outcome.go (full, 99 lines)
  docs/SPEC-DECIDE.md (lines 938-955)

Left owed: none.

git status --short