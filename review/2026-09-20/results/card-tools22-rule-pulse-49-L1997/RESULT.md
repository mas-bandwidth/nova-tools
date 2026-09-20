"RESULT tools22-rule-pulse-49 sha=5298f6be12ea — does the code at this base do what docs/SPEC-PULSE.md rule 49 says?
CONFORMS internal/pulse/cut.go:487
SPEC docs/SPEC-PULSE.md:1997 rule 49
PKG internal/pulse
ASK An implementation must, when rendering a card, accept a STEP 1 that only mkdirs/clones over https:// and checks out (writing it with no TMPDIR on the line) but refuse one whose STEP 1 sets a TMPDIR, printing a CUT REFUSED that names the rule and writing no card.
internal/pulse/cut.go:487
	if strings.Contains(step1, "TMPDIR") {
                return "", "rule 5: STEP 1 sets TMPDIR (the runner exports TMPDIR outside every repo; a card sets none of its own)"
internal/pulse/cut.go:490
	if strings.Contains(step1, "git@") || !strings.Contains(step1, "https://") {
                return "", "rule 5: STEP 1 does not clone over https (the clone URL is https, never git@)"
internal/pulse/cut.go:478
	for _, want := range []string{"mkdir -p scratch", "checkout -b"} {
                if !strings.Contains(step1, want) {
                        return "", fmt.Sprintf("rule 5: STEP 1 lacks %q (STEP 1 must carry mkdir -p scratch and checkout -b)", want)
                }
        }
GUARDED-BY internal/pulse/cut_test.go:168 TestCutStepOneSetsNoTmpDir
greps: grep -rn "TMPDIR\|CUT REFUSED\|cut-step-one\|stepOne" --include='*.go' internal/pulse/ ; grep -rn "stepOneIsOneShellLine\|STEP 1 sets TMPDIR" --include='*.go' internal/pulse/
Left owed: none
