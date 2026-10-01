package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// OpRecord is one recorded operation for idempotent retry (--op <id>).
type OpRecord struct {
	Op        string `json:"op"`
	Verb      string `json:"verb"`
	Stdout    string `json:"stdout"`
	ExitCode  int    `json:"exit_code"`
	Timestamp string `json:"timestamp,omitempty"`
}

// ValidateOpID verifies that opID is non-empty, within safe bounds, and contains only
// safe path characters (letters, digits, '.', '-', '_') starting with an alphanumeric character.
func ValidateOpID(op string) error {
	if op == "" {
		return fmt.Errorf("operation id cannot be empty")
	}
	if len(op) > 255 {
		return fmt.Errorf("operation id %q exceeds 255 characters", op)
	}
	first := op[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return fmt.Errorf("operation id %q must start with an alphanumeric character", op)
	}
	for i := 1; i < len(op); i++ {
		c := op[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return fmt.Errorf("operation id %q contains invalid character %q; must contain only letters, numbers, '.', '-' or '_'", op, c)
		}
	}
	return nil
}

// LoadOpRecord reads <storeDir>/.ops/<opID>.json if present. Returns (nil, nil) if absent.
func LoadOpRecord(storeDir, verb, opID string) (*OpRecord, error) {
	opPath := filepath.Join(storeDir, ".ops", opID+".json")
	data, err := os.ReadFile(opPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read operation record %s: %w", opPath, err)
	}
	var rec OpRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("invalid operation record %s: %w", opPath, err)
	}
	if rec.Verb != verb {
		return nil, fmt.Errorf("operation id %q was already recorded for verb %q, not %q", opID, rec.Verb, verb)
	}
	return &rec, nil
}

// SaveOpRecord writes the result of a successful operation to <storeDir>/.ops/<opID>.json atomically.
func SaveOpRecord(storeDir, verb, opID, stdout string, exitCode int) error {
	opsDir := filepath.Join(storeDir, ".ops")
	if err := os.MkdirAll(opsDir, 0o700); err != nil {
		return fmt.Errorf("cannot create operations directory %s: %w", opsDir, err)
	}
	rec := OpRecord{
		Op:        opID,
		Verb:      verb,
		Stdout:    strings.TrimRight(stdout, "\r\n"),
		ExitCode:  exitCode,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode operation record: %w", err)
	}
	opPath := filepath.Join(opsDir, opID+".json")
	if err := atomicfile.Write(filepath.Clean(opPath), append(data, '\n'), 0o600, atomicfile.ExactMode()); err != nil {
		return fmt.Errorf("cannot write operation record %s: %w", opPath, err)
	}
	return nil
}
