package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimingScriptHeaderSaysHowItRuns pins the header comment of the
// citiming program: a file that is not a verb of any command must say what
// the program is and how to run it from a checkout. The header carries the
// run line and does not appeal to a sprint card or PATHS, which a reader of
// the tree has no use for.
func TestTimingScriptHeaderSaysHowItRuns(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("main.go")
	require.NoError(t, err, "the program file is missing: %v", err)

	header := string(data)
	idx := strings.Index(header, "package main")
	require.Greater(t, idx, 0, "main.go has no package main declaration")
	header = header[:idx]

	assert.NotContains(t, header, "//go:build ignore",
		"the program must not carry a build-ignore tag")

	// The header says how to run the program from a checkout.
	assert.Contains(t, header, "go run tools/citiming/main.go",
		"the header must say how to run the program")

	// A card is not a reason a reader of the tree can use: the header must
	// not appeal to a sprint card or PATHS.
	assert.NotContains(t, header, "card",
		"the header must not argue from a sprint card")
	assert.NotContains(t, header, "PATHS",
		"the header must not argue from card PATHS")
}
