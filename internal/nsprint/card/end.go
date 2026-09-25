package card

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type EndRecord struct {
	Identity           string
	Sprint             string
	Label              string
	Token              string
	Results            string
	Outcome            string
	Reason             string
	CheckReceiptSha256 string
}

// ParseEndRecord parses 7 or 8 line end records.
func ParseEndRecord(content string) (EndRecord, error) {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) < 7 {
		return EndRecord{}, fmt.Errorf("invalid end record: expected at least 7 lines, got %d", len(lines))
	}
	rec := EndRecord{
		Identity:           strings.TrimSpace(lines[0]),
		Sprint:             strings.TrimSpace(lines[1]),
		Label:              strings.TrimSpace(lines[2]),
		Token:              strings.TrimSpace(lines[3]),
		Results:            strings.TrimSpace(lines[4]),
		Outcome:            strings.TrimSpace(lines[5]),
		Reason:             strings.TrimSpace(lines[6]),
		CheckReceiptSha256: "-",
	}
	if len(lines) >= 8 {
		rec.CheckReceiptSha256 = strings.TrimSpace(lines[7])
	}
	return rec, nil
}

// ComputeReceiptSha computes sha256 over exact check.receipt bytes (including final newline).
func ComputeReceiptSha(resultsDir string) (string, Receipt, error) {
	receiptPath := filepath.Join(resultsDir, "check.receipt")
	bytes, err := os.ReadFile(receiptPath)
	if err != nil {
		return "-", Receipt{}, err
	}
	h := sha256.New()
	h.Write(bytes)
	sha := hex.EncodeToString(h.Sum(nil))

	// Parse receipt lines
	rLines := strings.Split(string(bytes), "\n")
	var rc Receipt
	for _, l := range rLines {
		parts := strings.Fields(l)
		if len(parts) < 2 {
			continue
		}
		key, val := parts[0], parts[1]
		switch key {
		case "identity":
			rc.Identity = val
		case "head":
			rc.Head = val
		case "base_sha":
			rc.BaseSha = val
		case "check_sha256":
			rc.CheckSha256 = val
		case "expect_sha256":
			rc.ExpectSha256 = val
		case "head_exit":
			rc.HeadExit = val
		case "head_match":
			rc.HeadMatch = val
		case "base_exit":
			rc.BaseExit = val
		case "base_match":
			rc.BaseMatch = val
		case "output_sha256":
			rc.OutputSha256 = val
		}
	}

	return sha, rc, nil
}
