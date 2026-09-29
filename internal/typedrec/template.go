package typedrec

import (
	"strings"
)

// Template returns the fill-in skeleton for a kind (A7).
// Uses colon-delimited headers strictly.
func Template(kind string) string {
	var b strings.Builder
	b.WriteString("<line 1 of this card verbatim>\n")
	b.WriteString("<DONE | ABSTAIN <why> | BLOCKED <why>>\n")
	for _, f := range Fields(kind) {
		b.WriteString(f + "\n")
	}

	switch kind {
	case KindRead:
		b.WriteString("## Findings\n")
		b.WriteString("none\n")
	case KindReport:
		b.WriteString("## Probes\n")
		b.WriteString("- p95 latency: 142ms\n")
		b.WriteString("## Summary\n")
		b.WriteString("- harvest throughput measured within nominal envelope\n")
	case KindDocsGuard:
		b.WriteString("## Verification\n")
		b.WriteString("- docs/SPEC-SWARM.md: verified\n")
		b.WriteString("## Gates\n")
		b.WriteString("- make preflight: pass 1.05s\n")
	default: // fix, recut, port
		b.WriteString("## Gates\n")
		b.WriteString("- make preflight: pass 0.98s\n")
		b.WriteString("## Left owed\n")
		b.WriteString("- none\n")
	}
	return b.String()
}

// Exemplar returns one format illustration for each kind (A7).
// Demonstrates syntax and architectural boundaries:
// 1. Status preserves DONE/ABSTAIN/BLOCKED.
// 2. Separate typed CHECK conclusion.
// 3. Verified/Landed never appear as worker result words.
// 4. Worker claims distinct from machinery facts.
// 5. Uses colon-delimited headers.
func Exemplar(kind string) string {
	switch kind {
	case KindRead:
		return `RESULT CARD-100 sha=a1b2c3d4e5f6 nova-tools read: read PR 812 at abc123def456
DONE
SCHEMA: v2
KIND: read
ATTEMPT: 1
CHECK: pass
REPO: mas-bandwidth/nova-tools
PR: 812
HEAD: 0123456789abcdef0123456789abcdef01234567
FINDINGS: 1
FLOOR: HIGH
SUGGEST: HOLD
## Findings
- HIGH internal/pulse/cut.go:42 ` + "`STEP 1 must carry mkdir -p scratch`" + ` add directory creation before clone`

	case KindRecut:
		return `RESULT CARD-101 sha=b2c3d4e5f6a1 nova-tools recut: fix boundary handling in cut
DONE
SCHEMA: v2
KIND: recut
ATTEMPT: 1
CHECK: pass
REPO: mas-bandwidth/nova-tools
BRANCH: emma/fix-boundary-recut
PATHS: internal/pulse/cut.go internal/pulse/cut_test.go
RED: go test ./internal/pulse -run TestBoundary failed with nil pointer dereference
GREEN: go test ./internal/pulse -run TestBoundary passed in 0.04s
PRIOR: #100 @abcdef012345
## Gates
- go test ./internal/pulse -run TestBoundary: pass 0.04s
- make preflight: pass 1.12s
## Left owed
- none`

	case KindPort:
		return `RESULT CARD-102 sha=c3d4e5f6a1b2 serialize port: port varint encoder to rust
DONE
SCHEMA: v2
KIND: port
ATTEMPT: 1
CHECK: pass
REPO: mas-bandwidth/serialize
BRANCH: emma/port-varint-rs
PATHS: serialize.rs/src/varint.rs serialize.rs/tests/varint_test.rs
RED: cargo test test_varint_parity failed: reference vector mismatch at byte 3
GREEN: cargo test test_varint_parity passed: 48/48 test vectors identical to C++ reference
## Gates
- cargo test: pass 2.10s
- make preflight: pass 0.85s
## Left owed
- none`

	case KindDocsGuard:
		return `RESULT CARD-103 sha=d4e5f6a1b2c3 nova-tools docs-guard: verify spec-swarm CLI flags
DONE
SCHEMA: v2
KIND: docs-guard
ATTEMPT: 1
CHECK: pass
REPO: mas-bandwidth/nova-tools
BRANCH: emma/docs-guard-swarm
PATHS: docs/SPEC-SWARM.md
## Verification
- docs/SPEC-SWARM.md:1448: verified
## Gates
- go test ./internal/docs -run TestDocLinks: pass 0.45s
- make preflight: pass 1.05s`

	case KindReport:
		return `RESULT CARD-104 sha=e5f6a1b2c3d4 nova-tools report: measure harvest throughput
DONE
SCHEMA: v2
KIND: report
ATTEMPT: 1
CHECK: pass
REPO: mas-bandwidth/nova-tools
BRANCH: worker/report-throughput
PATHS: reports/2026-09-21-harvest.tsv
PROBES: 2
## Probes
- p95 latency: nova-pulse harvest --bench-measure -> 142ms
- memory rss: ps -o rss= -p $PID -> 34.2MB
## Summary
- Harvest latency stays under 150ms across 100 iterations with zero heap growth.`

	default: // fix
		return `RESULT CARD-105 sha=f6a1b2c3d4e5 nova-tools fix: null pointer on empty queue
DONE
SCHEMA: v2
KIND: fix
ATTEMPT: 1
CHECK: pass
REPO: mas-bandwidth/nova-tools
BRANCH: emma/fix-nil-queue
PATHS: internal/pulse/queue.go internal/pulse/queue_test.go
RED: go test ./internal/pulse -run TestEmptyQueueDoesNotPanic panic: runtime error: invalid memory address
GREEN: go test ./internal/pulse -run TestEmptyQueueDoesNotPanic passed in 0.02s
## Gates
- go test ./internal/pulse -run TestEmptyQueueDoesNotPanic: pass 0.02s
- make preflight: pass 0.98s
## Left owed
- none`
	}
}

// UpdateBranchLine replaces an existing BRANCH header line with `BRANCH: <branch>` (for v2)
// or `BRANCH <branch>` (for legacy) or appends it to the header block.
func UpdateBranchLine(content, branch string) (updated string, changed bool, isNew bool) {
	lines := strings.Split(content, "\n")
	found := false
	prefix := "BRANCH "
	if strings.Contains(content, "SCHEMA: v2") {
		prefix = "BRANCH: "
	}
	exact := prefix + branch
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "BRANCH:") || strings.HasPrefix(t, "BRANCH ") {
			found = true
			if t == exact {
				return content, false, false
			}
			lines[i] = exact
			return strings.Join(lines, "\n"), true, false
		}
	}
	if !found {
		trimmed := strings.TrimRight(content, "\n")
		return trimmed + "\n" + exact + "\n", true, true
	}
	return content, false, false
}
