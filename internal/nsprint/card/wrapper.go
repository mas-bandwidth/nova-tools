package card

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// RunWrapper executes the wrapper check step and produces the end record and receipt.
func RunWrapper(card WrapperCard, repoDir, branch string, resultsDir string, timeout time.Duration) (outcome, reason string, receiptSha string, err error) {
	if !CheckBound(card.Kind) {
		return "DONE", "done", "-", nil
	}

	outcome, reason, receipt, err := RunChecks(repoDir, branch, card.BaseSha, card.Check, card.Expect, card.Identity, resultsDir, timeout)
	if err != nil && receipt.Head == "" {
		return outcome, reason, "-", err
	}

	receiptPath := filepath.Join(resultsDir, "check.receipt")
	bytes, err := os.ReadFile(receiptPath)
	if err != nil {
		return outcome, reason, "-", err
	}

	h := sha256.New()
	h.Write(bytes)
	receiptSha = hex.EncodeToString(h.Sum(nil))

	return outcome, reason, receiptSha, nil
}
