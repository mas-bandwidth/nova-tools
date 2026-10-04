package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimingScriptHeaderSaysHowItRuns pins the header comment of the
// committed timing.go script: a //go:build ignore file that is not a verb of
// the nova-ci command must say what the script is and how to run it from a
// checkout. The header carries the run line and does not appeal to a sprint
// card or PATHS, which a reader of the tree has no use for.
func TestTimingScriptHeaderSaysHowItRuns(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("timing.go")
	require.NoError(t, err, "the script file is missing: %v", err)

	header := string(data)
	idx := strings.Index(header, "package main")
	require.Greater(t, idx, 0, "timing.go has no package main declaration")
	header = header[:idx]

	// The header says how to run the script from a checkout.
	assert.Contains(t, header, "go run cmd/nova-ci/timing.go",
		"the header must say how to run the script")

	// A card is not a reason a reader of the tree can use: the header must
	// not appeal to a sprint card or PATHS.
	assert.NotContains(t, header, "card",
		"the header must not argue from a sprint card")
	assert.NotContains(t, header, "PATHS",
		"the header must not argue from card PATHS")
}
