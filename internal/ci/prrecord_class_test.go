package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoVerbPathNeedsAHandPRRecord asserts that no verb path needs a hand
// `pr record` (mas-bandwidth/nova-tools#4349).
//
// Prior to #4349, card end --ok --pr refused with NOPR if the PR record had not
// been created by a hand `nova-sprint pr record` command. Under #4349:
//  1. The ingest (ev:github pull_request) decodes PR body and sets head, base,
//     and stream/task from branch stream/<s> or body CARD: <id>.
//  2. land.PRClaim permits ClaimREST for open PRs (opened, synchronize, reopened, open).
//  3. card end --ok --pr resolves an unrecorded PR via REST (gh.ViewPR) and records
//     it with RecordPRHead, printing READ PR, rather than refusing with NOPR.
//  4. card end --ok on a copy in ready with --as automatically performs work+end.
func TestNoVerbPathNeedsAHandPRRecord(t *testing.T) {
	t.Parallel()

	// (a) land/prrecord.go accepts ClaimREST for open actions.
	prrecordPath := filepath.Join("..", "..", "internal", "nsprint", "land", "prrecord.go")
	prrecordSrc, err := os.ReadFile(prrecordPath)
	if err != nil {
		t.Fatalf("reading prrecord.go: %v", err)
	}
	prrecordStr := string(prrecordSrc)
	for _, action := range []string{`"open"`, `"opened"`, `"synchronize"`, `"reopened"`} {
		if !strings.Contains(prrecordStr, action) {
			t.Errorf("prrecord.go missing open action support for %s in ClaimREST validation", action)
		}
	}
	if !strings.Contains(prrecordStr, "prBodyCardRx") || !strings.Contains(prrecordStr, "streamFromBranch") {
		t.Errorf("prrecord.go missing prBodyCardRx / streamFromBranch for branch and body extraction")
	}

	// (b) ghevent/event.go preserves PR body so ingest can extract CARD: <id>.
	gheventPath := filepath.Join("..", "..", "internal", "ghevent", "event.go")
	gheventSrc, err := os.ReadFile(gheventPath)
	if err != nil {
		t.Fatalf("reading event.go: %v", err)
	}
	gheventStr := string(gheventSrc)
	if !regexp.MustCompile(`Body\s+\*string`).MatchString(gheventStr) {
		t.Errorf("ghevent/event.go missing Body field on pullRequest struct")
	}
	if !strings.Contains(gheventStr, `v["body"] = e.Body`) {
		t.Errorf("ghevent/event.go missing body field serialization in Fields()")
	}

	// (c) card end implements automatic REST resolution and WORK+END ready transition.
	cardMovesPath := filepath.Join("..", "..", "deprecated", "cmd", "nova-sprint", "card_moves.go")
	src, err := os.ReadFile(cardMovesPath)
	if err != nil {
		t.Fatalf("reading card_moves.go: %v", err)
	}
	srcStr := string(src)
	for _, requiredPattern := range []string{
		"READ PR",
		"WORK+END",
		"RecordPRHead",
		"ViewPR",
	} {
		if !strings.Contains(srcStr, requiredPattern) {
			t.Errorf("%s missing required #4349 pattern %q", cardMovesPath, requiredPattern)
		}
	}

	// (d) Scan Go files in cmd/ to ensure no verb path advises running pr record by hand.
	repoRoot := "../.."
	prRecordAdviceRe := regexp.MustCompile(`(?i)(?:record the PR first|pr record first)`)
	err = filepath.Walk(filepath.Join(repoRoot, "cmd"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if prRecordAdviceRe.MatchString(trimmed) {
				t.Errorf("%s:%d: verb path instructs running pr record: %s", path, i+1, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
