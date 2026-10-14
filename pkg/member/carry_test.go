package member

// The CARRY: line and the PATHS-PROPOSED line a finish carries, as nova-sprint's copy
// changed them (finish-PR cards, a cut report): pure string functions, so nothing here
// sleeps, reads the clock, opens a socket or starts a child.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const carrySha = "0123456789abcdef0123456789abcdef01234567"

// PathsProposed reads the LAST line: a report cut to its cap may hold a fragment of the
// line, and the whole one the finish carries comes after it.
func TestPathsProposedReadsTheLastLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
		want       []string
		ok         bool
	}{
		{"none", "did the work", nil, false},
		{"one line", "PATHS-PROPOSED: a.go, b.go", []string{"a.go", "b.go"}, true},
		{"the last wins over a cut first", "x; PATHS-PROPOSED: a.go,b\ny; PATHS-PROPOSED: a.go,b.go", []string{"a.go", "b.go"}, true},
		{"prose after the paths", "PATHS-PROPOSED: `c.go`, d.go because the test needs it", []string{"c.go", "d.go"}, true},
	} {
		got, ok := PathsProposed(tc.text)
		assert.Equal(t, tc.ok, ok, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}
}

// CarryProposed keeps the whole PATHS-PROPOSED line at the report's end within the
// 500-byte cut: a line the report already carries (whole or cut) is replaced by the whole
// one, the report is cut and never the line, and a line of 500 bytes or more is kept whole.
func TestCarryProposedKeepsTheWholeLine(t *testing.T) {
	t.Parallel()
	long := make([]string, 60)
	for i := range long {
		long[i] = "pkg/somewhere/file" + strings.Repeat("x", 2) + string(rune('a'+i%26)) + ".go"
	}
	longLine := "; " + ProposedKey + " " + strings.Join(long, ",")
	require.GreaterOrEqual(t, len(longLine), 500)
	for _, tc := range []struct {
		name, report string
		r            Result
		want         string
	}{
		{"nothing proposed", "did it", Result{Report: "did it"}, "did it"},
		{"appended", "did it", Result{Report: "did it\nPATHS-PROPOSED: a.go,b.go"}, "did it; PATHS-PROPOSED: a.go,b.go"},
		{"read from the body", "did it", Result{Body: "PATHS-PROPOSED: a.go"}, "did it; PATHS-PROPOSED: a.go"},
		{"an existing whole line is not repeated", "did it; PATHS-PROPOSED: a.go,b.go", Result{Report: "PATHS-PROPOSED: a.go,b.go"}, "did it; PATHS-PROPOSED: a.go,b.go"},
		{"a cut line is replaced by the whole one", "did it; PATHS-PROPOSED: a.go,b", Result{Report: "PATHS-PROPOSED: a.go,b.go"}, "did it; PATHS-PROPOSED: a.go,b.go"},
		{"the report is cut, never the line", strings.Repeat("r", 600), Result{Report: "PATHS-PROPOSED: a.go,b.go"}, strings.Repeat("r", 500-len("; PATHS-PROPOSED: a.go,b.go")) + "; PATHS-PROPOSED: a.go,b.go"},
		{"a line of 500 bytes or more is kept whole", "did it", Result{Report: "PATHS-PROPOSED: " + strings.Join(long, ",")}, longLine},
	} {
		assert.Equal(t, tc.want, CarryProposed(tc.report, tc.r), tc.name)
	}
}

// CarryLine and CarryOf are one format: every Carry a line can say reads back as itself,
// in both shapes (a widen's card and attempt, a finish-PR card's head and branch).
func TestCarryLineRoundTripsThroughCarryOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		c    Carry
		line string
	}{
		{"a widen", Carry{Card: "s1-1", Attempt: 2, Head: carrySha}, "CARRY: s1-1 attempt 2 head=" + carrySha},
		{"a widen naming its branch", Carry{Card: "s1-1", Attempt: 2, Head: carrySha, Branch: "sprint/s1-1"}, "CARRY: s1-1 attempt 2 head=" + carrySha + " branch=sprint/s1-1"},
		{"a finish-PR card", Carry{Head: carrySha, Branch: "fix/x"}, "CARRY: head=" + carrySha + " branch=fix/x"},
		{"a finish-PR card naming its card", Carry{Card: "s1-2", Head: carrySha}, "CARRY: s1-2 head=" + carrySha},
		{"a finish-PR card with neither", Carry{Head: carrySha}, "CARRY: head=" + carrySha},
	} {
		require.Equal(t, tc.line, CarryLine(tc.c), tc.name)
		got, ok := CarryOf("RESULT: s1-1 sha=000000000000\n" + CarryLine(tc.c) + "\n\nprose\n")
		require.True(t, ok, "%s: %q does not read back", tc.name, tc.line)
		assert.Equal(t, tc.c, got, tc.name)
	}
}

// The reversed witnesses: a line that is not a carry reads as none.
func TestCarryOfRefusesAnIncompleteLine(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"CARRY: s1-1 attempt 2 head=0123456",     // a short head
		"CARRY: s1-1 attempt 2",                  // no head
		"CARRY: attempt 2 head=" + carrySha,      // an attempt with no card
		"CARRY: s1-1 attempt 0 head=" + carrySha, // a widen's attempt is positive
		"CARRY: branch=fix/x",                    // no head
	} {
		_, ok := CarryOf(line)
		assert.False(t, ok, "%q read as a carry", line)
	}
	_, ok := CarryOf("RESULT: c\n\nCARRY: head=" + carrySha)
	assert.False(t, ok, "a CARRY: line below the header is not the brief's")
}
