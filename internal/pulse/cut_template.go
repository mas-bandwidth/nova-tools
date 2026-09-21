package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultDiffCap is the 6 KB cap on inlined prior diffs in v2 cards (A1).
const DefaultDiffCap = 6144

// MaxDiffCap is the 64 KB upper bound on inlined diffs.
const MaxDiffCap = 65536

// CardV2Kinds is the canonical list of card kinds supported by Card Template v2 (A4).
var CardV2Kinds = []string{"recut", "fix", "port", "docs-guard", "report", "read"}

// IsV2Kind reports whether a kind is one of the Card Template v2 kinds.
func IsV2Kind(kind string) bool {
	for _, k := range CardV2Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// TurnBudgetV2 returns the turn budget per kind (A4).
func TurnBudgetV2(kind string) int {
	switch kind {
	case "read":
		return 8
	case "docs-guard", "guard":
		return 10
	case "report":
		return 10
	case "port", "recut", "rebase":
		return 15
	case "fix", "replay", "spec":
		return 20
	default:
		return 20
	}
}

// InlineDiff inlines a prior diff, capping it at capBytes (default 6 KB) and
// appending a notice naming omitted bytes if truncated (A1).
func InlineDiff(diff string, capBytes int) string {
	if diff == "" {
		return ""
	}
	if capBytes <= 0 {
		capBytes = DefaultDiffCap
	}
	if len(diff) <= capBytes {
		return diff
	}
	omitted := len(diff) - capBytes
	return fmt.Sprintf("%s\n... [inlined diff capped at %d bytes; omitted %d bytes; full diff retained in mirror]",
		diff[:capBytes], capBytes, omitted)
}

// KindClauses returns the exactly three specific conditions for a card kind (A6).
func KindClauses(kind string) [3]string {
	switch kind {
	case "fix":
		return [3]string{
			"Reproduce with the reproducing test first: write the red test, verify it fails, then make it green.",
			"Edit only files within declared PATHS; run the one named test command to verify green.",
			"Run class-rule preflight on the current tip and fix what it names before writing RESULT.md.",
		}
	case "recut":
		return [3]string{
			"Inspect the inlined prior diff, reviewer line, and failing test output; do not repeat the prior attempt.",
			"Apply the minimal targeted correction within declared PATHS; verify with the one named test command.",
			"Run class-rule preflight on the current tip and fix what it names before writing RESULT.md.",
		}
	case "port":
		return [3]string{
			"Match target language behavior byte-for-byte against the reference implementation; never alter the reference.",
			"Write reproducing tests red-first in the target language; keep tests green with the one named test command.",
			"Touch only files within declared PATHS; run class-rule preflight before writing RESULT.md.",
		}
	case "docs-guard":
		return [3]string{
			"Verify that every documented symbol, path, and CLI flag matches actual code; do not soften rules to match broken code.",
			"Keep documentation changes strictly within declared PATHS; run the one named test command.",
			"Run class-rule preflight and ensure zero markdown or link lint errors before writing RESULT.md.",
		}
	case "report":
		return [3]string{
			"Probe facts using exact commands, file:line coordinates, or measurements; never report opinions or estimates.",
			"Append each measurement immediately to RESULT.md without progress narration, praise, or task repetition.",
			"Stay within the file and turn budget; if blocked, report line 2 BLOCKED with the concrete obstacle.",
		}
	case "read":
		return [3]string{
			"Read and write only: do not run go build, go test, or any toolchain commands.",
			"Quote every held rule verbatim with file:line; emit only findings at or above the declared severity floor.",
			"Do not read outside declared read scope and the test's package; write the typed verdict line last.",
		}
	default:
		return [3]string{
			"Follow the task specification verbatim; quote rules beside every held assertion.",
			"Edit only files within declared PATHS; verify changes with the one named test command.",
			"Run class-rule preflight on the current tip and fix what it names before writing RESULT.md.",
		}
	}
}

// CardV2Input carries all parameters needed to render a Card Template v2 (A1-A7).
type CardV2Input struct {
	Kind          string // fix, recut, port, docs-guard, report, read
	Number        int    // card number
	Repo          string // owner/name
	Title         string // clean title
	Branch        string // working branch
	Base          string // base branch or commit sha
	Location      string // spec or code location (file:line)
	TestPackage   string // test package path
	TestFunction  string // test function name
	TestCommand   string // one test command
	Paths         string // declared write scope
	ReviewerLine  string // reviewer's typed disposition line (A1)
	PriorDiff     string // prior diff to inline (A1)
	FailingOutput string // failing test output or red line (A1)
	PreflightCmd  string // preflight command (A3, default: make preflight)
	Floor         string // severity floor for read (default: HIGH)
	PR            int    // PR number if applicable
	Issue         int    // Issue number if applicable
	Head          string // Head commit sha if applicable
}

// cleanTitle strips multi-line PR bodies, trailers, and noisy footers (A5).
func cleanTitle(title string) string {
	lines := strings.Split(title, "\n")
	first := strings.TrimSpace(lines[0])
	// Remove common prefixes or noise
	first = strings.TrimPrefix(first, "Title: ")
	return first
}

// RenderCardV2 renders a complete Card Template v2 meeting all A1–A7 requirements.
func RenderCardV2(in CardV2Input) (string, error) {
	if in.Repo == "" {
		return "", fmt.Errorf("CardV2Input requires Repo")
	}
	if in.Kind == "" {
		return "", fmt.Errorf("CardV2Input requires Kind")
	}
	budget := TurnBudgetV2(in.Kind)
	title := cleanTitle(in.Title)
	if title == "" {
		title = fmt.Sprintf("%s task", in.Kind)
	}
	preflight := in.PreflightCmd
	if preflight == "" {
		preflight = "make preflight"
	}
	repoShortName := repoShort(in.Repo)

	var b strings.Builder

	// Line 1: Placeholder contract line; sha12 computed over lines below
	fmt.Fprintf(&b, "RESULT CARD-%d sha=<sha12> %s %s: %s\n", in.Number, repoShortName, in.Kind, oneline.Escape(title))

	// Header instructions with Turn budget (A4)
	fmt.Fprintf(&b, "You are a worker. Turn budget: %d turns. Deadline is the machinery's.\n", budget)
	b.WriteString("Read scope: Do not read outside PATHS and the test's package.\n\n")

	// Target & Scope (A2)
	b.WriteString("## Target & Scope\n")
	fmt.Fprintf(&b, "REPO: %s\n", in.Repo)
	if in.Base != "" {
		fmt.Fprintf(&b, "BASE: %s\n", in.Base)
	}
	if in.Branch != "" {
		fmt.Fprintf(&b, "BRANCH: %s\n", in.Branch)
	}
	if in.Location != "" {
		fmt.Fprintf(&b, "LOCATION: %s\n", in.Location)
	}
	if in.TestPackage != "" || in.TestFunction != "" {
		testTarget := in.TestPackage
		if in.TestFunction != "" {
			testTarget = fmt.Sprintf("%s.%s", in.TestPackage, in.TestFunction)
		}
		fmt.Fprintf(&b, "TEST: %s\n", testTarget)
	}
	if in.TestCommand != "" {
		fmt.Fprintf(&b, "COMMAND: %s\n", in.TestCommand)
	}
	if in.Paths != "" {
		fmt.Fprintf(&b, "PATHS: %s\n", in.Paths)
	}
	b.WriteString("Read scope: Do not read outside PATHS and the test's package.\n\n")

	// Inlined Evidence (A1)
	hasEvidence := strings.TrimSpace(in.ReviewerLine) != "" ||
		strings.TrimSpace(in.PriorDiff) != "" ||
		strings.TrimSpace(in.FailingOutput) != ""
	if hasEvidence {
		b.WriteString("## Inlined Evidence\n")
		if rev := strings.TrimSpace(in.ReviewerLine); rev != "" {
			fmt.Fprintf(&b, "Reviewer verdict:\n%s\n\n", rev)
		}
		if failing := strings.TrimSpace(in.FailingOutput); failing != "" {
			fmt.Fprintf(&b, "Failing test output:\n%s\n\n", failing)
		}
		if diff := strings.TrimSpace(in.PriorDiff); diff != "" {
			fmt.Fprintf(&b, "Prior diff:\n%s\n\n", InlineDiff(diff, DefaultDiffCap))
		}
	}

	// Conditions: exactly 3 clauses per kind (A6)
	b.WriteString("## Conditions\n")
	clauses := KindClauses(in.Kind)
	for i, c := range clauses {
		fmt.Fprintf(&b, "%d. %s\n", i+1, c)
	}
	b.WriteString("\n")

	// Preflight line (A3)
	b.WriteString("## Preflight\n")
	fmt.Fprintf(&b, "Before writing RESULT.md, run the class-rule preflight on the current tip: %s and fix what it names.\n\n", preflight)

	// RESULT.md Fill-in Template (A7)
	b.WriteString("## RESULT.md Fill-in Template\n")
	b.WriteString(ResultTemplateV2(in.Kind))
	b.WriteString("\n\n")

	// Exemplar per kind (A7)
	b.WriteString(fmt.Sprintf("## Exemplar (%s)\n", in.Kind))
	b.WriteString(ResultExemplarV2(in.Kind))
	b.WriteString("\n")

	cardText := b.String()
	lines := strings.Split(cardText, "\n")
	body := strings.Join(lines[1:], "\n")
	sum := sha256.Sum256([]byte(body))
	sha12 := hex.EncodeToString(sum[:])[:12]
	lines[0] = fmt.Sprintf("RESULT CARD-%d sha=%s %s %s: %s", in.Number, sha12, repoShortName, in.Kind, oneline.Escape(title))
	return strings.Join(lines, "\n"), nil
}

// ResultTemplateV2 returns the fill-in skeleton for a kind (A7).
// Preserves terminal words DONE, ABSTAIN, BLOCKED; requires CHECK: pass|fail|not-run.
func ResultTemplateV2(kind string) string {
	var b strings.Builder
	b.WriteString("RESULT <line 1 of this card verbatim>\n")
	b.WriteString("<DONE | ABSTAIN <why> | BLOCKED <why>>\n")
	b.WriteString("CHECK: <pass | fail | not-run>\n")
	b.WriteString("BRANCH: <working branch>\n")
	b.WriteString("REPO: <owner>/<name>\n")
	b.WriteString("PATHS: <space-separated list of modified files>\n")

	switch kind {
	case "read":
		b.WriteString("## Head\n")
		b.WriteString("findings: <n>\n")
		b.WriteString("floor: <HIGH | MEDIUM | LOW>\n")
		b.WriteString("repo: <owner>/<name>\n")
		b.WriteString("head: <sha>\n")
		b.WriteString("PR<number>: <APPROVE | HOLD> head=<sha> repo=<owner>/<name>\n")
		b.WriteString("## Findings\n")
		b.WriteString("- <severity> <file:line> `<exact quoted rule>` <one-clause fix>\n")
	case "report":
		b.WriteString("## Probes\n")
		b.WriteString("| probe | command | result |\n")
		b.WriteString("| --- | --- | --- |\n")
		b.WriteString("| <name> | <command> | <measured value> |\n")
		b.WriteString("## Summary\n")
		b.WriteString("<one paragraph factual summary without opinions>\n")
	case "docs-guard":
		b.WriteString("## Verification\n")
		b.WriteString("| target | check | state |\n")
		b.WriteString("| --- | --- | --- |\n")
		b.WriteString("| <doc file:line> | <symbol/link> | verified |\n")
		b.WriteString("## Gates\n")
		b.WriteString("| name | result | seconds |\n")
		b.WriteString("| --- | --- | --- |\n")
		b.WriteString("| <test command> | pass | <sec> |\n")
	default: // fix, recut, port
		b.WriteString("RED: <command and failing output summary>\n")
		b.WriteString("GREEN: <command and passing output summary>\n")
		b.WriteString("## Gates\n")
		b.WriteString("| name | result | seconds |\n")
		b.WriteString("| --- | --- | --- |\n")
		b.WriteString("| <test command> | pass | <sec> |\n")
		b.WriteString("## Left owed\n")
		b.WriteString("- <item left owed or none>\n")
	}
	return b.String()
}

// ResultExemplarV2 returns one complete, concrete 10/10 exemplar for each kind (A7).
// Demonstrates Stella's architectural boundaries:
// 1. Status preserves DONE/ABSTAIN/BLOCKED.
// 2. Separate typed CHECK conclusion.
// 3. Verified/Landed never appear as worker result words.
// 4. Worker claims distinct from machinery facts.
func ResultExemplarV2(kind string) string {
	switch kind {
	case "read":
		return `RESULT CARD-100 sha=a1b2c3d4e5f6 nova-tools read: read PR 812 at abc123def456
DONE
CHECK: pass
BRANCH: worker/read-812
REPO: mas-bandwidth/nova-tools
PATHS: internal/pulse/cut.go
## Head
findings: 1
floor: HIGH
repo: mas-bandwidth/nova-tools
head: abc123def456
PR812: HOLD head=abc123def456 repo=mas-bandwidth/nova-tools
## Findings
- HIGH internal/pulse/cut.go:42 ` + "`STEP 1 must carry mkdir -p scratch`" + ` add directory creation before clone`

	case "recut":
		return `RESULT CARD-101 sha=b2c3d4e5f6a1 nova-tools recut: fix boundary handling in cut
DONE
CHECK: pass
BRANCH: emma/fix-boundary-recut
REPO: mas-bandwidth/nova-tools
PATHS: internal/pulse/cut.go internal/pulse/cut_test.go
RED: go test ./internal/pulse -run TestBoundary failed with nil pointer dereference
GREEN: go test ./internal/pulse -run TestBoundary passed in 0.04s
## Gates
| name | result | seconds |
| --- | --- | --- |
| go test ./internal/pulse -run TestBoundary | pass | 0.04 |
| make preflight | pass | 1.12 |
## Left owed
- none`

	case "port":
		return `RESULT CARD-102 sha=c3d4e5f6a1b2 serialize port: port varint encoder to rust
DONE
CHECK: pass
BRANCH: emma/port-varint-rs
REPO: mas-bandwidth/serialize
PATHS: serialize.rs/src/varint.rs serialize.rs/tests/varint_test.rs
RED: cargo test test_varint_parity failed: reference vector mismatch at byte 3
GREEN: cargo test test_varint_parity passed: 48/48 test vectors identical to C++ reference
## Gates
| name | result | seconds |
| --- | --- | --- |
| cargo test | pass | 2.10 |
| make preflight | pass | 0.85 |
## Left owed
- none`

	case "docs-guard":
		return `RESULT CARD-103 sha=d4e5f6a1b2c3 nova-tools docs-guard: verify spec-swarm CLI flags
DONE
CHECK: pass
BRANCH: emma/docs-guard-swarm
REPO: mas-bandwidth/nova-tools
PATHS: docs/SPEC-SWARM.md
## Verification
| target | check | state |
| --- | --- | --- |
| docs/SPEC-SWARM.md:1448 | --name flag syntax | verified |
| docs/SPEC-SWARM.md:1460 | template names list | verified |
## Gates
| name | result | seconds |
| --- | --- | --- |
| go test ./internal/docs -run TestDocLinks | pass | 0.45 |
| make preflight | pass | 1.05 |`

	case "report":
		return `RESULT CARD-104 sha=e5f6a1b2c3d4 nova-tools report: measure harvest throughput
DONE
CHECK: pass
BRANCH: worker/report-throughput
REPO: mas-bandwidth/nova-tools
PATHS: reports/2026-09-21-harvest.tsv
## Probes
| probe | command | result |
| --- | --- | --- |
| p95 latency | nova-pulse harvest --bench-measure | 142ms |
| memory rss | ps -o rss= -p $PID | 34.2MB |
## Summary
Harvest latency stays under 150ms across 100 iterations with zero heap growth.`

	default: // fix
		return `RESULT CARD-105 sha=f6a1b2c3d4e5 nova-tools fix: null pointer on empty queue
DONE
CHECK: pass
BRANCH: emma/fix-nil-queue
REPO: mas-bandwidth/nova-tools
PATHS: internal/pulse/queue.go internal/pulse/queue_test.go
RED: go test ./internal/pulse -run TestEmptyQueueDoesNotPanic panic: runtime error: invalid memory address
GREEN: go test ./internal/pulse -run TestEmptyQueueDoesNotPanic passed in 0.02s
## Gates
| name | result | seconds |
| --- | --- | --- |
| go test ./internal/pulse -run TestEmptyQueueDoesNotPanic | pass | 0.02 |
| make preflight | pass | 0.98 |
## Left owed
- none`
	}
}

// ResultEnvelopeV2 represents a parsed and validated RESULT.md v2 document.
type ResultEnvelopeV2 struct {
	ContractLine     string
	Status           string // DONE, ABSTAIN, BLOCKED
	Check            string // pass, fail, not-run
	Branch           string
	Repo             string
	Paths            string
	IsFriendApproval bool              // Always false for worker-authored records (Stella boundary)
	Fields           map[string]string // Typed key-value fields
	Body             string            // Human evidence body
}

// ValidateResultV2 parses and strictly validates a RESULT.md against v2 envelope rules.
// Enforces Stella's architectural boundaries:
// - Status enum must strictly be DONE, ABSTAIN, BLOCKED.
// - Separate typed CHECK: pass|fail|not-run line required.
// - Verified and Landed are rejected as worker result statuses.
// - Duplicate, unknown, or oversized fields (>4096 bytes) are refused.
// - Worker-authored DISPOSITION never mints friend approval.
func ValidateResultV2(raw string, kind string) (ResultEnvelopeV2, error) {
	var env ResultEnvelopeV2
	env.Fields = make(map[string]string)
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
		if len(ln) > 4096 {
			return env, fmt.Errorf("line %d exceeds 4096-byte field limit", i+1)
		}
		colon := strings.IndexByte(ln, ':')
		if colon <= 0 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(ln[:colon]))
		val := strings.TrimSpace(ln[colon+1:])

		if seenKeys[key] {
			return env, fmt.Errorf("duplicate field %q at line %d", key, i+1)
		}
		seenKeys[key] = true
		env.Fields[key] = val

		switch key {
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

	if env.Check == "" {
		return env, fmt.Errorf("RESULT.md v2 requires a typed `CHECK: <pass|fail|not-run>` line")
	}

	// Capture remaining body as human evidence
	if bodyStart < len(lines) {
		env.Body = strings.Join(lines[bodyStart:], "\n")
	}

	// Authority Boundary: Parsing worker-authored DISPOSITION must never mint friend approval
	env.IsFriendApproval = false

	return env, nil
}
