package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cairnDogfoodReport = "../../docs/dogfood/2026-10-06/codex/cairn.md"

var dogfoodGradeLine = regexp.MustCompile(`(?m)^Grade: (URGENT|NEXT)\.$`)
var dogfoodCountLine = regexp.MustCompile(`^urgent=(\d+) next=(\d+)$`)

// TestDocsTreeIsConsistent keeps the Cairn dogfood report in the dated docs
// tree and its closing totals equal to the findings actually graded.
func TestDocsTreeIsConsistent(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(cairnDogfoodReport)
	require.NoError(t, err, "%s: the Cairn dogfood report must be present in the docs tree: %v", filepath.ToSlash(cairnDogfoodReport), err)

	text := string(body)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	require.NotEmpty(t, lines)
	counts := dogfoodCountLine.FindStringSubmatch(strings.TrimSpace(lines[len(lines)-1]))
	require.Len(t, counts, 3, "%s must end with `urgent=<n> next=<n>`", cairnDogfoodReport)

	urgent, err := strconv.Atoi(counts[1])
	require.NoError(t, err)
	next, err := strconv.Atoi(counts[2])
	require.NoError(t, err)

	var gotUrgent, gotNext int
	for _, grade := range dogfoodGradeLine.FindAllStringSubmatch(text, -1) {
		switch grade[1] {
		case "URGENT":
			gotUrgent++
		case "NEXT":
			gotNext++
		}
	}
	assert.Equal(t, urgent, gotUrgent, "%s urgent total must equal the graded findings", cairnDogfoodReport)
	assert.Equal(t, next, gotNext, "%s next total must equal the graded findings", cairnDogfoodReport)
	assert.Regexp(t, `(?m)^READ [0-9]+/10 — .+$`, text, "%s must include the cold-read score and reason", cairnDogfoodReport)
	assert.Regexp(t, `(?m)^USE [0-9]+/10 — .+$`, text, "%s must include the hands-on score and reason", cairnDogfoodReport)
}
