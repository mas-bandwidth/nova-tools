"RESULT tools22-rule-version-12 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 12 says?
CONFORMS internal/update/snapverb.go:40
SPEC docs/SPEC-VERSION.md:176 rule 12
PKG cmd/nova-version
ASK A snapshot of a fake binary that is slow on its FIRST version invocation and immediate on every one after must be read, not refused, when that first exec falls inside the default per-binary bound — so the verb's normal case, a --bin one `go install` old, is not a refusal.
internal/update/snapverb.go:40  var snapshotChildTimeout = 30 * time.Second
internal/update/snapverb.go:99  timeout, budget := snapshotChildTimeout, snapshotBudget
internal/update/snapverb.go:148 ctx, cancel := context.WithTimeout(run, timeout)
internal/update/snapverb.go:149 p := process(ctx, []string{path, "version"}, nil, ChildCap)
The snapshot verb defaults one binary's read deadline to 30s (snapverb.go:40, 99) and runs each child under it (snapverb.go:148-149); a first exec that costs the fixture's 6s sleep is inside the default bound, so the binary is read and its version row recorded rather than refused. The deciding comment names the reasoning verbatim (snapverb.go:21 "THIRTY SECONDS, NOT FIVE, BECAUSE THIS VERB'S FIRST EXEC IS ALWAYS A COLD ONE").
GUARDED-BY internal/update/inventory_spec_test.go:310 TestSnapshotToleratesTheFirstExecOfANeverSeenBinary
Greps: grep -rn "func Test" --include='*_test.go' cmd/nova-version/; grep -rn "Tolerates|FirstExec|NeverSeen|never-seen|first exec|firstrun|cold" --include='*.go' cmd/nova-version/ internal/. The cmd/nova-version package holds no snapshot-bounds tests; the rule's test lives in internal/update/inventory_spec_test.go. Confirmed by running it once: GOMAXPROCS=8 go test ./internal/update/ -count=1 -run TestSnapshotToleratesTheFirstExecOfANeverSeenBinary -> PASS (6.00s).
Left owed