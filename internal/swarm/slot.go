package swarm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// ExitRecord is <job>/exit.json: the supervisor's durable completion evidence (rule 18,
// step 6). An outcome with none is `unknown`, never guessed. `Attest` is the per-launch
// secret the supervisor minted in its own memory, written here only inside endWith after the
// job's whole process group is dead; its hash lives in the slot file, and a reader accepts
// this record as the supervisor's own word only when both the nonce AND the attestation
// match.
type ExitRecord struct {
	RC        int    `json:"rc"`
	Signal    string `json:"signal,omitempty"`
	Ended     string `json:"ended"`
	Survivors int    `json:"survivors"`
	Nonce     string `json:"nonce"`
	Attest    string `json:"attest,omitempty"`
	End       string `json:"end,omitempty"`
	Spent     int    `json:"spent,omitempty"`
	Observed  bool   `json:"observed,omitempty"`
	Partial   bool   `json:"partial,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

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

// ReadJSON reads one of this package's small records back.
func ReadJSON(path string, v any) error {
	raw, err := readFileSteady(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// The job directory's durable records.
func ExitPath(jobDir string) string   { return filepath.Join(jobDir, "exit.json") }
func ResultPath(jobDir string) string { return filepath.Join(jobDir, "RESULT.md") }
