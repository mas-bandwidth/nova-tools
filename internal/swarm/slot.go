package swarm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
)

// Nonce is twelve hex characters from the OS random source, drawn once per launch and never
// reused. It is what makes a stale supervisor's identify fail against a reservation that
// has moved on.
func Nonce() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("the OS random source would not supply a launch nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// The job directory's durable records.
func ExitPath(jobDir string) string   { return filepath.Join(jobDir, "exit.json") }
func ResultPath(jobDir string) string { return filepath.Join(jobDir, "RESULT.md") }
