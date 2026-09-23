package pulse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarvestVersionDowngradePrevention(t *testing.T) {
	// A v2 card is cut with SCHEMA: v2 and ATTEMPT: 1 in its contract template.
	// A worker attempts to downgrade by omitting SCHEMA and CHECK, returning legacy DONE/BRANCH/REPO.
	// Harvest must refuse the downgrade, classifying it as mismatch instead of done/push.
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/1")

	contract := "RESULT CARD-1 sha=1234567890ab owner/repo fix: prevent downgrade"
	cardContent := contract + "\nSCHEMA: v2\nATTEMPT: 1\nREPO owner/repo\n"

	cardDir := filepath.Join(root, "cardsrc")
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(cardDir, "card-1.md")
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Worker returns contract line 1, DONE, BRANCH, REPO, but omits SCHEMA: v2 and CHECK: pass
	downgradeResult := contract + `
DONE
BRANCH worker/downgrade-branch
REPO owner/repo
PATHS internal/pulse/harvest.go
RED: failed
GREEN: passed
`
	jobDir := filepath.Join(root, "0", "jobs", "card-1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(downgradeResult), 0o644); err != nil {
		t.Fatal(err)
	}

	cardsTSV := fmt.Sprintf("card-1\t0\t-\t%s\tv2\t1\n", cardPath)
	if err := os.WriteFile(filepath.Join(root, "cards.tsv"), []byte(cardsTSV), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errs := runHarvest(t, root)
	if strings.Contains(out, "HARVEST PUSH") || strings.Contains(out, "pushed=1") {
		t.Errorf("harvest pushed downgraded card; want mismatch refusal:\nout=%s\nerrs=%s", out, errs)
	}
	if !strings.Contains(out, "mismatch=1") && !strings.Contains(out, "HARVEST MISMATCH") {
		t.Errorf("harvest did not score downgraded card as mismatch:\nout=%s\nerrs=%s", out, errs)
	}
}

func TestHarvestUnknownSchemaRefused(t *testing.T) {
	// A result declaring an unknown schema (e.g. SCHEMA: v3) must be refused before any dispatch.
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/1")

	contract := "RESULT CARD-2 sha=1234567890ab owner/repo fix: test unknown schema"
	cardContent := contract + "\nSCHEMA: v2\nATTEMPT: 1\nREPO owner/repo\n"

	cardDir := filepath.Join(root, "cardsrc")
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(cardDir, "card-2.md")
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	unknownSchemaResult := contract + `
DONE
SCHEMA: v3
BRANCH worker/v3-branch
REPO owner/repo
PATHS internal/pulse/harvest.go
RED: failed
GREEN: passed
`
	jobDir := filepath.Join(root, "0", "jobs", "card-2")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(unknownSchemaResult), 0o644); err != nil {
		t.Fatal(err)
	}

	cardsTSV := fmt.Sprintf("card-2\t0\t-\t%s\tv2\t1\n", cardPath)
	if err := os.WriteFile(filepath.Join(root, "cards.tsv"), []byte(cardsTSV), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _ := runHarvest(t, root)
	if strings.Contains(out, "HARVEST PUSH") || strings.Contains(out, "pushed=1") {
		t.Errorf("harvest pushed result with unknown schema:\n%s", out)
	}
	if !strings.Contains(out, "mismatch=1") {
		t.Errorf("harvest did not score unknown schema as mismatch:\n%s", out)
	}
}

func TestHarvestReplayedAttemptRefused(t *testing.T) {
	// A card is on attempt 2 (e.g. reissue). A worker replays a stale result from attempt 1.
	// Harvest must refuse the attempt mismatch.
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/1")

	contract := "RESULT CARD-3 sha=1234567890ab owner/repo fix: test replayed attempt"
	cardContent := contract + "\nSCHEMA: v2\nATTEMPT: 2\nREPO owner/repo\n"

	cardDir := filepath.Join(root, "cardsrc")
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(cardDir, "card-3.md")
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Worker provides ATTEMPT: 1 (replaying attempt 1 on attempt 2 card)
	replayedResult := contract + `
DONE
SCHEMA: v2
ATTEMPT: 1
CHECK: pass
BRANCH worker/replayed-branch
REPO owner/repo
PATHS internal/pulse/harvest.go
RED: failed
GREEN: passed
`
	jobDir := filepath.Join(root, "0", "jobs", "card-3")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(replayedResult), 0o644); err != nil {
		t.Fatal(err)
	}

	cardsTSV := fmt.Sprintf("card-3\t0\t-\t%s\tv2\t2\n", cardPath)
	if err := os.WriteFile(filepath.Join(root, "cards.tsv"), []byte(cardsTSV), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _ := runHarvest(t, root)
	if strings.Contains(out, "HARVEST PUSH") || strings.Contains(out, "pushed=1") {
		t.Errorf("harvest pushed replayed attempt result:\n%s", out)
	}
	if !strings.Contains(out, "mismatch=1") {
		t.Errorf("harvest did not score replayed attempt as mismatch:\n%s", out)
	}
}

func TestHarvestLegitimateLegacyCardAccepted(t *testing.T) {
	// A legitimate old-format card (cut without v2 SCHEMA/CHECK) must pass through
	// the explicit legacy adapter and score done when valid.
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/10")

	contract := "RESULT CARD-10 legacy card test"
	result := contract + `
DONE
BRANCH rowan/legacy-clean-branch
REPO owner/repo
PATHS internal/pulse/harvest.go
RED: failed
GREEN: passed
`
	addCard(t, root, "card-10", "0", "flash", contract, result)

	out, _ := runHarvest(t, root)
	if !strings.Contains(out, "pushed=1") {
		t.Errorf("harvest failed to push valid legacy card:\n%s", out)
	}
	if strings.Contains(out, "mismatch=1") {
		t.Errorf("harvest scored valid legacy card as mismatch:\n%s", out)
	}
}

func TestHarvestTruncatedLegacyResultNoSlicePanicAlongsideHealthyCard(t *testing.T) {
	// Root has 2 cards:
	// Card 1 has a truncated 1-line result (only contract line; len(lines) == 1).
	// Card 2 has a healthy result.
	// Harvest must NOT panic on lines[2:], must refuse Card 1 as mismatch, and must successfully process Card 2.
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/20")

	contract1 := "RESULT CARD-21 truncated one line"
	// 1-line result: only contract line 1
	result1 := contract1

	contract2 := "RESULT CARD-22 healthy card"
	result2 := contract2 + `
DONE
BRANCH rowan/healthy-branch
REPO owner/repo
PATHS internal/pulse/harvest.go
RED: failed
GREEN: passed
`
	addCard(t, root, "card-21", "0", "flash", contract1, result1)
	addCard(t, root, "card-22", "1", "flash", contract2, result2)

	out, errs := runHarvest(t, root)
	if strings.Contains(errs, "panic") {
		t.Fatalf("harvest panicked on truncated 1-line result:\n%s", errs)
	}
	if !strings.Contains(out, "mismatch=1") {
		t.Errorf("harvest did not mark truncated card-21 as mismatch:\nout=%s\nerrs=%s", out, errs)
	}
	if !strings.Contains(out, "pushed=1") {
		t.Errorf("harvest did not push healthy card-22:\nout=%s\nerrs=%s", out, errs)
	}

	// Verify truncated card artifact is retained on disk
	raw1, err := os.ReadFile(filepath.Join(root, "0", "jobs", "card-21", "RESULT.md"))
	if err != nil {
		t.Fatalf("card-21 RESULT.md was not retained: %v", err)
	}
	if string(raw1) != result1 {
		t.Errorf("card-21 RESULT.md changed, got %q, want %q", string(raw1), result1)
	}
}
