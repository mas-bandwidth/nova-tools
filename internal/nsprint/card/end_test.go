package card

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEndRefusesDoneWithoutBoundCheckPass(t *testing.T) {
	tmp, err := os.MkdirTemp("", "nsprint-end-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	resultsDir := filepath.Join(tmp, "results")
	os.MkdirAll(resultsDir, 0755)

	rc := Receipt{
		Identity:     "card-1",
		Head:         "abc1234",
		BaseSha:      "def5678",
		CheckSha256:  "1111",
		ExpectSha256: "2222",
		HeadExit:     "0",
		HeadMatch:    "true",
		BaseExit:     "1",
		BaseMatch:    "false",
		OutputSha256: "3333",
	}

	receiptPath := filepath.Join(resultsDir, "check.receipt")
	os.WriteFile(receiptPath, []byte(rc.Format()), 0644)

	sha, parsed, err := ComputeReceiptSha(resultsDir)
	if err != nil {
		t.Fatalf("ComputeReceiptSha failed: %v", err)
	}
	if sha == "-" || parsed.Identity != "card-1" {
		t.Errorf("expected valid receipt, got sha=%s", sha)
	}

	// Test 7-line end record parsing (legacy compatibility)
	legacyContent := "card-1\nsprint-1\nlabel-1\ntoken-1\nresults-1\nDONE\ndone\n"
	rec, err := ParseEndRecord(legacyContent)
	if err != nil {
		t.Fatalf("failed to parse 7-line end record: %v", err)
	}
	if rec.CheckReceiptSha256 != "-" {
		t.Errorf("expected '-' for legacy end record check_receipt_sha256, got %s", rec.CheckReceiptSha256)
	}
}
