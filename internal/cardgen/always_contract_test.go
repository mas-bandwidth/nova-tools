package cardgen

import (
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

func TestTheGeneratedReaderIsHandedTheAlwaysAllowedFiles(t *testing.T) {
	t.Parallel()
	brief := Render(Header{}, Card{ID: "scope", File: "internal/x/x.go", Paths: []string{"internal/x/x.go"}, Test: "internal/x TestX", Tier: "pro", Task: "Keep scope narrow."})
	readBrief, why := AsRead(brief)
	assert.Empty(t, why)
	_, read, ok := strings.Cut(readBrief, "\nAS A READ\n")
	assert.True(t, ok)
	assert.Contains(t, read, "Files a change must touch to keep the tree green are always inside PATHS, whatever the brief names: every *_test.go, every file under a testdata/ directory, tla/RUNS.tsv and tla/CASES.tsv, internal/docs/catalog.go, and every AGENTS.md map; any other file outside PATHS is still out of scope.")
}
