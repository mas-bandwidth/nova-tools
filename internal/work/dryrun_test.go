package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRunImportVerifiesRawCapturesAndByteExactHashes(t *testing.T) {
	t.Parallel()

	rawDir := filepath.Join("testdata", "raw_captures")
	report, err := RunDryRunImport(DryRunOptions{
		RawCapturesDir: rawDir,
	})
	if err != nil {
		t.Fatalf("RunDryRunImport failed: %v", err)
	}

	if report.RawCapturesVerified != 3 {
		t.Errorf("expected 3 raw captures verified, got %d", report.RawCapturesVerified)
	}

	expectedHashes := map[string]string{
		"issues.json":     "b14fedb624be76178600a4431b97931031a9b141353fb6245208c9de00bbdc8d",
		"comments-2.json": "b29306699b7fc16f8bec5c24693aff0e616e4dbc757e35f95358d887ca21a364",
		"comments-7.json": "18db666c45a95cf096456404f4f4429a6c98565bd8b6eab625b3b1cd0ada9a87",
	}

	for _, vh := range report.VerifiedHashes {
		exp, ok := expectedHashes[vh.File]
		if !ok {
			t.Errorf("unexpected verified file %s", vh.File)
			continue
		}
		if !strings.EqualFold(vh.Actual, exp) {
			t.Errorf("%s hash mismatch: got %s, want %s", vh.File, vh.Actual, exp)
		}
		if !vh.Verified {
			t.Errorf("%s was not marked verified", vh.File)
		}
	}
}

func TestDryRunImportAccurateAssignees(t *testing.T) {
	t.Parallel()

	rawDir := filepath.Join("testdata", "raw_captures")
	synPath := filepath.Join("testdata", "synthetic_cases.json")
	report, err := RunDryRunImport(DryRunOptions{
		RawCapturesDir:    rawDir,
		SyntheticFilePath: synPath,
	})
	if err != nil {
		t.Fatalf("RunDryRunImport failed: %v", err)
	}

	// Live captures must have all 8 issues unassigned
	if len(report.UnassignedIssues) != 8 {
		t.Errorf("expected all 8 live issues to be unassigned, got %d unassigned (%v)", len(report.UnassignedIssues), report.UnassignedIssues)
	}

	// Synthetic fixture must cover multiple assignees
	synData, err := os.ReadFile(synPath)
	if err != nil {
		t.Fatalf("reading synthetic cases: %v", err)
	}
	var syn struct {
		Cases []struct {
			Name  string `json:"name"`
			Issue Issue  `json:"issue"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(synData, &syn); err != nil {
		t.Fatalf("parsing synthetic cases: %v", err)
	}

	foundMultiple := false
	for _, c := range syn.Cases {
		if len(c.Issue.Assignees) > 1 {
			foundMultiple = true
			if len(c.Issue.Assignees) != 2 {
				t.Errorf("expected 2 assignees in multiple assignees fixture, got %d", len(c.Issue.Assignees))
			}
		}
	}
	if !foundMultiple {
		t.Errorf("synthetic fixtures must provide explicit multiple assignee coverage")
	}
}

func TestDryRunImportDistinguishesNullAndEmptyBody(t *testing.T) {
	t.Parallel()

	rawDir := filepath.Join("testdata", "raw_captures")
	synPath := filepath.Join("testdata", "synthetic_cases.json")
	report, err := RunDryRunImport(DryRunOptions{
		RawCapturesDir:    rawDir,
		SyntheticFilePath: synPath,
	})
	if err != nil {
		t.Fatalf("RunDryRunImport failed: %v", err)
	}

	// Issue #4 in live captures has body == null
	if len(report.NullBodyIssues) != 1 || report.NullBodyIssues[0] != 4 {
		t.Errorf("expected Issue #4 to be null body, got %v", report.NullBodyIssues)
	}

	// Synthetic fixtures provide empty string body and missing body key
	synData, err := os.ReadFile(synPath)
	if err != nil {
		t.Fatalf("reading synthetic cases: %v", err)
	}
	var syn struct {
		Cases []struct {
			Name  string `json:"name"`
			Issue Issue  `json:"issue"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(synData, &syn); err != nil {
		t.Fatalf("parsing synthetic cases: %v", err)
	}

	var hasEmpty, hasMissing bool
	for _, c := range syn.Cases {
		switch c.Issue.BodyState {
		case BodyKindEmpty:
			hasEmpty = true
			if c.Issue.Body == nil || *c.Issue.Body != "" {
				t.Errorf("expected non-nil empty string, got %v", c.Issue.Body)
			}
		case BodyKindMissing:
			hasMissing = true
			if c.Issue.Body != nil {
				t.Errorf("expected nil body on missing key, got %v", c.Issue.Body)
			}
		}
	}

	if !hasEmpty {
		t.Errorf("synthetic fixture must cover empty string body")
	}
	if !hasMissing {
		t.Errorf("synthetic fixture must cover missing body key")
	}
}

func TestDryRunImportCleanHistory(t *testing.T) {
	t.Parallel()

	rawDir := filepath.Join("testdata", "raw_captures")
	report, err := RunDryRunImport(DryRunOptions{
		RawCapturesDir: rawDir,
	})
	if err != nil {
		t.Fatalf("RunDryRunImport failed: %v", err)
	}

	// All 4 live comments across issues 2 and 7 have equal created_at/updated_at and no edit history
	if len(report.CleanHistoryComments) != 4 {
		t.Errorf("expected 4 clean history comments, got %d (%v)", len(report.CleanHistoryComments), report.CleanHistoryComments)
	}

	// Synthetic fixtures provide genuine edit history
	synPath := filepath.Join("testdata", "synthetic_cases.json")
	synData, err := os.ReadFile(synPath)
	if err != nil {
		t.Fatalf("reading synthetic cases: %v", err)
	}
	var syn struct {
		Cases []struct {
			Name  string `json:"name"`
			Issue Issue  `json:"issue"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(synData, &syn); err != nil {
		t.Fatalf("parsing synthetic cases: %v", err)
	}

	foundEdited := false
	for _, c := range syn.Cases {
		for _, comment := range c.Issue.Comments {
			if comment.Edited && len(comment.EditHistory) > 0 {
				foundEdited = true
				if comment.CreatedAt.Equal(comment.UpdatedAt) {
					t.Errorf("expected edited comment to have distinct updated_at")
				}
			}
		}
	}
	if !foundEdited {
		t.Errorf("synthetic fixtures must provide explicit comment edit history coverage")
	}
}

func TestDryRunImportNoMutationsAndNoClosureReceipt(t *testing.T) {
	t.Parallel()

	rawDir := filepath.Join("testdata", "raw_captures")
	seedPath := filepath.Join("testdata", "work_test_seed.json")
	synPath := filepath.Join("testdata", "synthetic_cases.json")

	report, err := RunDryRunImport(DryRunOptions{
		RawCapturesDir:    rawDir,
		SeedFilePath:      seedPath,
		SyntheticFilePath: synPath,
	})
	if err != nil {
		t.Fatalf("RunDryRunImport failed: %v", err)
	}

	if report.ClosureReceiptEmitted {
		t.Errorf("dry-run import must never emit a closure-authorizing receipt")
	}
	if report.MutationsPerformed != 0 {
		t.Errorf("dry-run import must perform zero mutations, got %d", report.MutationsPerformed)
	}
	if report.Status != "DRY_RUN_VERIFIED" {
		t.Errorf("expected DRY_RUN_VERIFIED status, got %s", report.Status)
	}
}

func TestLosslessTreeCodecRoundTrip(t *testing.T) {
	t.Parallel()

	inputJSON := `{"id":9007199254740993,"title":"Lossless test","active":true,"details":null,"tags":["a","b"],"nested":{"count":42}}`
	node, err := JSONToLisp([]byte(inputJSON))
	if err != nil {
		t.Fatalf("JSONToLisp failed: %v", err)
	}

	lispStr := FormatLisp(node)
	if !strings.Contains(lispStr, `(number "9007199254740993")`) {
		t.Errorf("expected number lexeme preserved in lisp, got: %s", lispStr)
	}
	if !strings.Contains(lispStr, `(null)`) {
		t.Errorf("expected null preserved in lisp, got: %s", lispStr)
	}
	if !strings.Contains(lispStr, `(boolean true)`) {
		t.Errorf("expected boolean true in lisp, got: %s", lispStr)
	}

	outJSON, err := LispToJSON(node)
	if err != nil {
		t.Fatalf("LispToJSON failed: %v", err)
	}

	var originalMap, roundtripMap map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &originalMap); err != nil {
		t.Fatalf("unmarshal original: %v", err)
	}
	if err := json.Unmarshal(outJSON, &roundtripMap); err != nil {
		t.Fatalf("unmarshal roundtrip: %v", err)
	}

	if len(originalMap) != len(roundtripMap) {
		t.Errorf("map size mismatch: original %d, roundtrip %d", len(originalMap), len(roundtripMap))
	}
}

func TestSeedDatasetContainsRealCommitSHA(t *testing.T) {
	t.Parallel()

	seedPath := filepath.Join("testdata", "work_test_seed.json")
	data, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("reading seed dataset: %v", err)
	}

	var ds IssueSeedDataset
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatalf("parsing seed dataset: %v", err)
	}

	wantSHA := "f38fe9fe5b9e6d8abbf0a01fc4e7e330d856ec89"
	if ds.CommitSHA != wantSHA {
		t.Errorf("seed dataset commit_sha: got %s, want %s", ds.CommitSHA, wantSHA)
	}
}
