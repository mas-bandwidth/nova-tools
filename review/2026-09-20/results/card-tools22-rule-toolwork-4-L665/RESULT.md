RESULT tools22-rule-toolwork-4-L665 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 4 says?
CONFORMS internal/decide/harvestclass.go:311
SPEC docs/SPEC-TOOLWORK.md:665 rule 4
PKG internal/docs
ASK The implementation must ensure that the class=/conf= fields from harvest classification never influence whether a card is pushed, a HOLD is lifted, or a read is skipped — those decisions must derive solely from the gate's own accept= verdict.
Deciding lines:
  internal/decide/harvestclass.go:18-21: "A classification ROUTES; it never accepts... The type below has no field a caller could read as permission: Pushes, LiftsHold and SkipsRead answer from the gate's own verdict, and the class is not an input to any of them."
  internal/decide/harvestclass.go:230-232: "Pushes, LiftsHold and SkipsRead below read the gate's verdict and ignore the class entirely, which is §4 rule 4 made structural rather than remembered."
  internal/decide/harvestclass.go:308-313: "Pushes reports whether this unit is pushed. It reads the GATE, never the class: no classification, at any confidence, turns a reject or an abstain into a push (§4 rule 4; S4, :631-649)."
  internal/decide/harvestclass.go:311-313: `func (c Classification) Pushes() bool { return strings.ToLower(strings.TrimSpace(c.Evidence.Accept)) == AcceptOK }`
  internal/decide/harvestclass.go:318: `func (c Classification) LiftsHold() bool { return false }`
  internal/decide/harvestclass.go:322: `func (c Classification) SkipsRead() bool { return false }`
GUARDED-BY internal/decide/harvestclass_test.go:129 TestAHarvestClassNeverPushesARejectedCard
Greps run:
  grep -rn "never.*push\|a-harvest-class" --include='*_test.go' internal/decide/
  grep -rn "a-harvest-class" --include='*.go' .
  grep -rn "Pushes\|LiftsHold\|SkipsRead\|Classification.*route\|never.*accepts" --include='*.go' internal/decide/harvestclass.go
  grep -rn "harvest" --include='*.go' . | grep -v _test.go (exploration task)
  grep -rn "OUTCOME" --include='*.go' . | grep -v _test.go (exploration task)
Left owed: none
git status --short: (nothing printed)