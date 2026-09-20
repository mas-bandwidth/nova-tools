RESULT tools22-rule-pulse-5 sha=5298f6be12ea
CONFORMS internal/pulse/harvestbench.go:335
SPEC docs/SPEC-PULSE.md:2510 rule 5
PKG internal/pulse
ASK An implementation must open the pull request against the base the card itself named (the RESULT.md `BASE` line, else `--base`, else `dev`) rather than a hardcoded branch such as `main`.
internal/pulse/harvestbench.go:217-219
	base := resultField(j.Result, "BASE")
	if base == "" {
		base = fallbackBase
	}
internal/pulse/harvestbench.go:335
	pr, err = forge.CreatePR(dest.repo, base, branch, prTitle(line1, label, in.Bench), benchPRBody(in.Bench, j, in.MaxBodyBytes))
GUARDED-BY internal/pulse/harvestbench_test.go:289 TestHarvestBenchOpensThePRAgainstTheCardsBase
greps: `grep -n "TestHarvestBenchOpensThePRAgainstTheCardsBase" docs/SPEC-PULSE.md`; `grep -n "base\|BASE\|CreatePR" internal/pulse/harvestbench.go`; `grep -n "func TestHarvestBench" internal/pulse/harvestbench_test.go`
ran: `GOMAXPROCS=8 go test ./internal/pulse/ -count=1 -run TestHarvestBenchOpensThePRAgainstTheCardsBase` -> ok
Left owed
