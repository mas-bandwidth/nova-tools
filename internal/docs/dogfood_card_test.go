package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var dogfoodFindingNumber = regexp.MustCompile(`^(\d+)\. Command: `)
var dogfoodScore = regexp.MustCompile(`^(READ|USE) ([0-9]+)/(10): .+\.$`)

// TestDocsTreeIsConsistent keeps the dated nova-card dogfood record complete
// and its closing counts consistent with the findings it records.
func TestDocsTreeIsConsistent(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "docs", "dogfood", "2026-10-06", "codex")
	data, err := os.ReadFile(filepath.Join(dir, "card.md"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.GreaterOrEqual(t, len(lines), 5)

	urgent, next, findings := 0, 0, 0
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if m := dogfoodFindingNumber.FindStringSubmatch(line); m != nil {
			findings++
			n, convErr := strconv.Atoi(m[1])
			require.NoError(t, convErr)
			require.Equal(t, findings, n, "finding numbers are consecutive")
			require.Less(t, i+2, len(lines), "finding %d has captured output", findings)
			require.True(t, strings.HasPrefix(strings.TrimSpace(lines[i+2]), "Output (first 3 lines):"))
			var hasExpected bool
			grade := ""
			for _, detail := range lines[i+1 : min(i+32, len(lines))] {
				detail = strings.TrimSpace(detail)
				hasExpected = hasExpected || strings.HasPrefix(detail, "Expected:")
				if strings.HasPrefix(detail, "Grade: ") {
					grade = detail
					break
				}
			}
			require.True(t, hasExpected, "finding %d records the expected result", findings)
			switch grade {
			case "Grade: URGENT":
				urgent++
			case "Grade: NEXT":
				next++
			default:
				require.FailNow(t, "finding %d has an unknown grade: %s", findings, grade)
			}
		}
	}
	require.Positive(t, findings, "the record contains at least one finding")

	read, use := false, false
	for _, line := range lines {
		m := dogfoodScore.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		score, convErr := strconv.Atoi(m[2])
		require.NoError(t, convErr)
		require.LessOrEqual(t, score, 10)
		if m[1] == "READ" {
			require.False(t, read, "READ is recorded once")
			read = true
		} else {
			require.False(t, use, "USE is recorded once")
			use = true
		}
	}
	require.True(t, read, "the record includes a READ score and reason")
	require.True(t, use, "the record includes a USE score and reason")
	require.Equal(t, "urgent="+strconv.Itoa(urgent)+" next="+strconv.Itoa(next), lines[len(lines)-1], "the final line counts every grade")
}
