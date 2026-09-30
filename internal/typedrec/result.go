package typedrec

import (
	"fmt"
	"regexp"
	"strings"
)

type fieldToken struct {
	key  string
	val  string
	line int
}

// Terminal status words.
const (
	StatusDone    = "DONE"
	StatusAbstain = "ABSTAIN"
	StatusBlocked = "BLOCKED"
)

// Defects named in the specification.
const (
	DefectMissing       = "missing"
	DefectUnknown       = "unknown"
	DefectDuplicate     = "duplicate"
	DefectMalformed     = "malformed"
	DefectContradictory = "contradictory"
	DefectOversized     = "oversized"
)

// Size limits.
const (
	MaxFileSize     = 64 * 1024 // 64 KB
	MaxFieldSize    = 4096      // 4096 bytes
	MaxEvidenceSize = 32 * 1024 // 32 KB
)

var (
	keyRegex  = regexp.MustCompile(`^[A-Z][A-Z0-9-]*$`)
	repoRegex = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9._-]+$`)
	hex40     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	priorRe   = regexp.MustCompile(`^#[1-9][0-9]* @[0-9a-f]{12}$`)
)

// Result is the parsed and validated RESULT v2 envelope.
type Result struct {
	Line1     string
	Status    string // DONE, ABSTAIN, BLOCKED
	StatusWhy string

	Schema   string
	Kind     string
	Attempt  int
	Check    string // pass, fail, not-run
	Repo     string
	Branch   string
	Paths    []string
	Red      string
	Green    string
	Prior    string
	PR       int
	Head     string
	Findings int
	Floor    string
	Suggest  string
	Probes   int

	Claims   map[string]string
	Evidence string
	Sections map[string][]string // heading -> list of - bullet rows

	// Validation defect outcome
	Valid  bool
	Field  string
	Defect string
	Line   int

	RawBytes  []byte
	RawSHA256 string
}

// ParseOptions configures verification against trusted card/machinery facts.
type ParseOptions struct {
	ExpectedKind    string
	ExpectedAttempt int
	ExpectedRepo    string
	ExpectedBranch  string
	ExpectedHead    string
	ContractLine    string
}

func isValidKind(k string) bool {
	for _, valid := range Kinds {
		if valid == k {
			return true
		}
	}
	return false
}

// ResultEnvelopeV2 represents a parsed and validated RESULT.md v2 document.
type ResultEnvelopeV2 struct {
	ContractLine     string
	Status           string // DONE, ABSTAIN, BLOCKED
	Schema           string // e.g. "v2"
	Attempt          string // e.g. "1"
	Check            string // pass, fail, not-run
	Branch           string
	Repo             string
	Paths            string
	IsFriendApproval bool              // Always false for worker-authored records (Stella boundary)
	Fields           map[string]string // Typed key-value fields
	Body             string            // Human evidence body
}

// KnownResultV2Headers defines the allowed typed headers before markdown sections.
var KnownResultV2Headers = map[string]bool{
	"CHECK":     true,
	"BRANCH":    true,
	"REPO":      true,
	"PATHS":     true,
	"KIND":      true,
	"SCHEMA":    true,
	"ATTEMPT":   true,
	"HEAD":      true,
	"PR":        true,
	"ISSUE":     true,
	"BASE":      true,
	"BASE-SHA":  true,
	"BASE_SHA":  true,
	"HOLD":      true,
	"HOLD_FILE": true,
	"RED":       true,
	"GREEN":     true,
	"LOCATION":  true,
	"COMMAND":   true,
	"TEST":      true,
	"APPLIED":   true,
	"REMAINS":   true,
}

// ValidateResultV2 parses and strictly validates a RESULT.md against v2 envelope rules.
func ValidateResultV2(raw string, kind string) (ResultEnvelopeV2, error) {
	var env ResultEnvelopeV2
	env.Fields = make(map[string]string)
	if kind != "" && !isValidKind(kind) {
		return env, fmt.Errorf("card KIND %q is not one of %s", kind, strings.Join(Kinds, ", "))
	}

	if len(raw) > MaxFileSize {
		return env, fmt.Errorf("RESULT.md exceeds maximum size limit of %d bytes (got %d)", MaxFileSize, len(raw))
	}

	lines := strings.Split(raw, "\n")
	if len(lines) < 3 {
		return env, fmt.Errorf("RESULT.md v2 wants at least 3 lines, got %d", len(lines))
	}

	// Line 1: contract line
	line1 := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(line1, "RESULT ") && !strings.HasPrefix(line1, "RESULT:") {
		return env, fmt.Errorf("line 1 must begin with RESULT, got %q", line1)
	}
	env.ContractLine = line1

	// Line 2: terminal word strictly DONE, ABSTAIN, BLOCKED
	line2 := strings.TrimSpace(lines[1])
	statusWord := line2
	if idx := strings.IndexByte(line2, ' '); idx >= 0 {
		statusWord = line2[:idx]
	}
	switch statusWord {
	case "DONE", "ABSTAIN", "BLOCKED":
		env.Status = statusWord
	case "Returned", "check-failed", "Verified", "Landed":
		return env, fmt.Errorf("line 2 status %q is prohibited as a worker terminal word; must be DONE, ABSTAIN, or BLOCKED", statusWord)
	default:
		return env, fmt.Errorf("line 2 wants DONE, ABSTAIN, or BLOCKED, got %q", line2)
	}

	seenKeys := make(map[string]bool)
	bodyStart := len(lines)

	// Scan typed header fields before markdown headers
	for i := 2; i < len(lines); i++ {
		ln := strings.TrimSpace(lines[i])
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			bodyStart = i
			break
		}
		if len(ln) > MaxFieldSize {
			return env, fmt.Errorf("line %d exceeds 4096-byte field limit", i+1)
		}

		var key, val string
		colon := strings.IndexByte(ln, ':')
		space := strings.IndexByte(ln, ' ')

		// Parse either KEY: VAL or KEY VAL
		if colon > 0 && (space < 0 || colon < space) {
			key = strings.ToUpper(strings.TrimSpace(ln[:colon]))
			val = strings.TrimSpace(ln[colon+1:])
		} else if space > 0 {
			candidateKey := strings.ToUpper(strings.TrimSpace(ln[:space]))
			if KnownResultV2Headers[candidateKey] {
				key = candidateKey
				val = strings.TrimSpace(ln[space+1:])
			} else {
				return env, fmt.Errorf("unknown header field %q at line %d", candidateKey, i+1)
			}
		} else {
			return env, fmt.Errorf("malformed header line %d: %q", i+1, ln)
		}

		if !KnownResultV2Headers[key] {
			return env, fmt.Errorf("unknown header field %q at line %d", key, i+1)
		}
		if key == "KIND" && !isValidKind(val) {
			return env, fmt.Errorf("KIND %q at line %d is not one of %s", val, i+1, strings.Join(Kinds, ", "))
		}

		if seenKeys[key] {
			return env, fmt.Errorf("duplicate field %q at line %d", key, i+1)
		}
		seenKeys[key] = true
		env.Fields[key] = val

		switch key {
		case "SCHEMA":
			env.Schema = val
		case "ATTEMPT":
			env.Attempt = val
		case "CHECK":
			if val != "pass" && val != "fail" && val != "not-run" {
				return env, fmt.Errorf("CHECK wants pass, fail, or not-run, got %q", val)
			}
			env.Check = val
		case "BRANCH":
			env.Branch = val
		case "REPO":
			env.Repo = val
		case "PATHS":
			env.Paths = val
		}
	}

	if got, ok := env.Fields["KIND"]; ok && kind != "" && got != kind {
		return env, fmt.Errorf("KIND %q contradicts the card's KIND %q", got, kind)
	}
	if env.Schema == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a typed `SCHEMA: v2` line")
	}
	if env.Schema != "v2" {
		return env, fmt.Errorf("unsupported SCHEMA %q (wants v2)", env.Schema)
	}
	if env.Attempt == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a typed `ATTEMPT: <n>` line")
	}
	if env.Check == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a typed `CHECK: <pass|fail|not-run>` line")
	}
	if env.Repo == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a REPO line")
	}

	// For DONE status on non-read/non-report kinds, require BRANCH and PATHS.
	if env.Status == "DONE" && kind != "read" && kind != "report" && kind != "" {
		if env.Branch == "" {
			return env, fmt.Errorf("RESULT.md v2 for %s requires BRANCH line", kind)
		}
		if env.Paths == "" {
			return env, fmt.Errorf("RESULT.md v2 for %s requires PATHS line", kind)
		}
	}

	// Extract and validate evidence body
	if bodyStart < len(lines) {
		body := strings.Join(lines[bodyStart:], "\n")
		if len(body) > MaxEvidenceSize {
			return env, fmt.Errorf("human evidence body exceeds %d bytes (got %d)", MaxEvidenceSize, len(body))
		}
		env.Body = body
	}

	return env, nil
}
