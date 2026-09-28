package swarm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const LaunchSchema = "nova.swarm.launch/1"

const MaxLaunchRecordBytes = 65536

// LaunchRecord is the canonical launch record for each reservation.
type LaunchRecord struct {
	Context          LaunchRecordContext     `json:"context"`
	Control          LaunchRecordControl     `json:"control"`
	EvidenceRoot     string                  `json:"evidence_root"`
	JobID            string                  `json:"job_id"`
	ManifestHash     string                  `json:"manifest_hash"`
	Realization      LaunchRecordRealization `json:"realization"`
	ReservationNonce string                  `json:"reservation_nonce"`
	Sandbox          string                  `json:"sandbox"`
	Schema           string                  `json:"schema"`
	Slot             string                  `json:"slot"`
	UsageEveryNS     string                  `json:"usage_every_ns"`
}

type LaunchRecordContext struct {
	Kind string `json:"kind"`
	Root string `json:"root"`
}

type LaunchRecordControl struct {
	ManifestHash  string               `json:"manifest_hash"`
	Root          string               `json:"root"`
	SandboxSource string               `json:"sandbox_source"`
	Launcher      LaunchRecordLauncher `json:"launcher"`
}

type LaunchRecordLauncher struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Source string `json:"source"`
}

type LaunchRecordRealization struct {
	EnvHash string `json:"env_hash"`
}

func LaunchRecordHash(r LaunchRecord) (string, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func LaunchRecordPath(evidenceRoot, jobID, nonce string) string {
	return filepath.Join(evidenceRoot, jobID, "launch", nonce+".json")
}

type LaunchRecordRefusal struct {
	Reason string
}

func (r LaunchRecordRefusal) Error() string { return r.Reason }

func ValidateLaunchRecord(r LaunchRecord) error {
	if r.Schema != LaunchSchema {
		return LaunchRecordRefusal{Reason: fmt.Sprintf("schema must be %s", LaunchSchema)}
	}
	if r.Context.Kind != "pool" && r.Context.Kind != "one-shot" {
		return LaunchRecordRefusal{Reason: "context.kind must be pool or one-shot"}
	}
	if r.Context.Root == "" || r.Context.Root[0] != '/' {
		return LaunchRecordRefusal{Reason: "context.root must be an absolute path"}
	}
	if r.EvidenceRoot == "" || r.EvidenceRoot[0] != '/' {
		return LaunchRecordRefusal{Reason: "evidence_root must be an absolute path"}
	}
	if r.JobID == "" {
		return LaunchRecordRefusal{Reason: "job_id is required"}
	}
	if !isPositiveDecimal(r.Slot) {
		return LaunchRecordRefusal{Reason: "slot must be a positive canonical decimal string"}
	}
	if !isLowerHex(r.ReservationNonce, 12) {
		return LaunchRecordRefusal{Reason: "reservation_nonce must be exactly twelve lowercase hexadecimal characters"}
	}
	if !isSHA256Digest(r.ManifestHash) {
		return LaunchRecordRefusal{Reason: `manifest_hash must be sha256:<64 lowercase hex>`}
	}
	if !isSHA256Digest(r.Control.ManifestHash) {
		return LaunchRecordRefusal{Reason: `control.manifest_hash must be sha256:<64 lowercase hex>`}
	}
	if r.Control.Root == "" || r.Control.Root[0] != '/' {
		return LaunchRecordRefusal{Reason: "control.root must be an absolute path"}
	}
	if r.Control.SandboxSource == "" || r.Control.SandboxSource[0] != '/' {
		return LaunchRecordRefusal{Reason: "control.sandbox_source must be an absolute path"}
	}
	if r.Control.Launcher.Path == "" || r.Control.Launcher.Path[0] != '/' {
		return LaunchRecordRefusal{Reason: "control.launcher.path must be an absolute path"}
	}
	if !isSHA256Digest(r.Control.Launcher.SHA256) {
		return LaunchRecordRefusal{Reason: `control.launcher.sha256 must be sha256:<64 lowercase hex>`}
	}
	if r.Control.Launcher.Source == "" || r.Control.Launcher.Source[0] != '/' {
		return LaunchRecordRefusal{Reason: "control.launcher.source must be an absolute path"}
	}
	if !isSHA256Digest(r.Realization.EnvHash) {
		return LaunchRecordRefusal{Reason: `realization.env_hash must be sha256:<64 lowercase hex>`}
	}
	if r.Sandbox == "" || r.Sandbox[0] != '/' {
		return LaunchRecordRefusal{Reason: "sandbox must be an absolute path"}
	}
	if !isPositiveDecimal(r.UsageEveryNS) {
		return LaunchRecordRefusal{Reason: "usage_every_ns must be a positive canonical decimal string"}
	}
	return nil
}

func PublishLaunchRecord(root, jobID, nonce string, r LaunchRecord) error {
	dir := filepath.Join(root, jobID, "launch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := LaunchRecordPath(root, jobID, nonce)
	if _, err := os.Stat(path); err == nil {
		return LaunchRecordRefusal{Reason: fmt.Sprintf("duplicate publication: %s already exists", path)}
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'), 0o644)
}

func ReadLaunchRecord(path string, budget time.Time) (LaunchRecord, error) {
	budgetPassed := false
	if !budget.IsZero() && time.Now().After(budget) {
		budgetPassed = true
	}
	raw, err := readFileSteadyBy(path, budget)
	if err != nil {
		return LaunchRecord{}, err
	}
	if !budget.IsZero() && !budgetPassed && time.Now().After(budget) {
		return LaunchRecord{}, LaunchRecordRefusal{Reason: "launch record read exceeded timeout"}
	}
	if len(raw) > MaxLaunchRecordBytes+1 {
		return LaunchRecord{}, LaunchRecordRefusal{Reason: fmt.Sprintf("launch record exceeds maximum size: %d bytes", len(raw))}
	}
	if !utf8.Valid(raw) {
		return LaunchRecord{}, LaunchRecordRefusal{Reason: "launch record is not valid UTF-8"}
	}
	if hasDuplicateKeys(raw) {
		return LaunchRecord{}, LaunchRecordRefusal{Reason: "launch record contains duplicate keys"}
	}
	if exceedsNesting(raw, 8) {
		return LaunchRecord{}, LaunchRecordRefusal{Reason: "launch record exceeds maximum nesting depth of 8"}
	}
	if hasTrailingData(raw) {
		return LaunchRecord{}, LaunchRecordRefusal{Reason: "launch record contains trailing data"}
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var r LaunchRecord
	if err := dec.Decode(&r); err != nil {
		return LaunchRecord{}, LaunchRecordRefusal{Reason: fmt.Sprintf("launch record parse error: %v", err)}
	}
	if err := ValidateLaunchRecord(r); err != nil {
		return LaunchRecord{}, err
	}
	return r, nil
}

func hasDuplicateKeys(raw []byte) bool {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	if tok != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	return scanObjectKeys(dec, seen)
}

// scanObjectKeys reads the members of an object whose opening '{' has been
// consumed, and consumes its closing '}' so the caller resumes at its own
// next member. It reports true on the first key repeated within one object.
func scanObjectKeys(dec *json.Decoder, seen map[string]bool) bool {
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		key, ok := tok.(string)
		if !ok {
			return false
		}
		if seen[key] {
			return true
		}
		seen[key] = true
		if dup := scanValueKeys(dec); dup {
			return true
		}
	}
	_, _ = dec.Token() // the closing '}'
	return false
}

// scanValueKeys reads one value, descending into objects and arrays and
// consuming their closing delimiters.
func scanValueKeys(dec *json.Decoder) bool {
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	switch tok {
	case json.Delim('{'):
		return scanObjectKeys(dec, map[string]bool{})
	case json.Delim('['):
		for dec.More() {
			if dup := scanValueKeys(dec); dup {
				return true
			}
		}
		_, _ = dec.Token() // the closing ']'
	}
	return false
}

// exceedsNesting counts object and array depth outside string literals, so
// a brace or bracket inside a string (escaped quotes included) never counts.
func exceedsNesting(raw []byte, max int) bool {
	depth := 0
	inString := false
	escaped := false
	for _, b := range raw {
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > max {
				return true
			}
		case '}', ']':
			depth--
		}
	}
	return false
}

// hasTrailingData reports anything but whitespace after the first JSON
// value, measured from the decoder's input offset over the whole input
// rather than only the bytes left in its read buffer.
func hasTrailingData(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var val json.RawMessage
	if err := dec.Decode(&val); err != nil {
		return false
	}
	return len(bytes.TrimSpace(raw[dec.InputOffset():])) > 0
}

func isSHA256Digest(s string) bool {
	if !strings.HasPrefix(s, "sha256:") {
		return false
	}
	hexPart := s[len("sha256:"):]
	if len(hexPart) != 64 {
		return false
	}
	for _, c := range hexPart {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func isPositiveDecimal(s string) bool {
	if s == "" {
		return false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return false
	}
	// Canonical means the decimal round trip: no leading plus, no leading zero.
	return n > 0 && strconv.Itoa(n) == s
}

func isLowerHex(s string, wantLen int) bool {
	if len(s) != wantLen {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
