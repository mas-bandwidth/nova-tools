package main

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// AdoptionStatus is one of the four dispositions an adoption cell can hold.
type AdoptionStatus string

const (
	Adopted  AdoptionStatus = "adopted"
	Trying   AdoptionStatus = "trying"
	Declined AdoptionStatus = "declined"
	Unknown  AdoptionStatus = "unknown"
)

func (s AdoptionStatus) valid() bool {
	switch s {
	case Adopted, Trying, Declined, Unknown:
		return true
	}
	return false
}

// AdoptionCell is one (line, tool) coordinate of the matrix: who lines up with
// which tool, and whether that pairing has landed, is still in flight, was
// refused, or has not been established.
type AdoptionCell struct {
	Line     string
	Tool     string
	Status   AdoptionStatus
	Evidence string
}

// AdoptionMatrix is the set of cells making up one adoption picture.
type AdoptionMatrix struct {
	Cells []AdoptionCell
}

// ParseAdoptionMatrix reads one cell per line as "<line> <tool> <status>
// [evidence]". Blank lines and lines beginning with "#" are ignored. A status
// outside the four allowed ones is a refusal, and an "adopted" cell must carry
// a non-empty evidence token: a "bus:" or "receipt:" id, or a captured tool
// output line.
func ParseAdoptionMatrix(input string) (*AdoptionMatrix, error) {
	m := &AdoptionMatrix{}
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cell, err := parseCell(line)
		if err != nil {
			return nil, err
		}
		m.Cells = append(m.Cells, cell)
	}
	return m, nil
}

func parseCell(line string) (AdoptionCell, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return AdoptionCell{}, fmt.Errorf("adoption cell %q needs line, tool and status", line)
	}
	lineName, tool, statusStr := fields[0], fields[1], fields[2]
	var evidence string
	if len(fields) > 3 {
		evidence = strings.Join(fields[3:], " ")
	}
	status := AdoptionStatus(statusStr)
	if !status.valid() {
		return AdoptionCell{}, fmt.Errorf("adoption status %q is not adopted, trying, declined or unknown", statusStr)
	}
	cell := AdoptionCell{Line: lineName, Tool: tool, Status: status, Evidence: evidence}
	if status == Adopted && !validEvidence(evidence) {
		return AdoptionCell{}, fmt.Errorf("adopted cell %s %s carries no evidence", lineName, tool)
	}
	return cell, nil
}

func validEvidence(evidence string) bool {
	return strings.TrimSpace(evidence) != ""
}

// GenerateAskLines emits one ASK line for every cell that is not "adopted",
// so the unresolved pairings are the only ones a reader must chase.
func GenerateAskLines(m *AdoptionMatrix) []string {
	if m == nil {
		return nil
	}
	var asks []string
	for _, c := range m.Cells {
		if c.Status == Adopted {
			continue
		}
		asks = append(asks, fmt.Sprintf("ASK line=%s tool=%s status=%s",
			oneline.Field(c.Line), oneline.Field(c.Tool), oneline.Field(string(c.Status))))
	}
	return asks
}

// EvaluateAdoptionExitCode returns 0 only when every cell is resolved —
// "adopted" or "declined" — and 1 while any cell is "unknown" or "trying".
func EvaluateAdoptionExitCode(m *AdoptionMatrix) int {
	if m == nil {
		return 0
	}
	for _, c := range m.Cells {
		if c.Status == Unknown || c.Status == Trying {
			return 1
		}
	}
	return 0
}
