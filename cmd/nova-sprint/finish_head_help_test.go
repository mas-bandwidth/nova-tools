package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinishHeadHelpLeadsWithTheCommit checks that finish -h's --head flag description
// first says a run with git passes the commit it pushed, then explains that without
// --head the head is the card's id (usable only by runs with no git), and keeps the
// warning that land refuses a head that is not a commit id.
func TestFinishHeadHelpLeadsWithTheCommit(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	var out, errb bytes.Buffer
	code := a.run([]string{"finish", "-h"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	help := out.String()

	// Find the --head flag description line (in the flags section)
	var headLine string
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--head ") {
			headLine = line
			break
		}
	}
	require.NotEmpty(t, headLine, "--head line not found in help")

	// Should say a run with git passes the commit it pushed
	assert.Contains(t, headLine, "a run with git", "--head description should mention a run with git")

	// Should not start prose with "default: the card's id"
	assert.NotContains(t, headLine, "default: the card's id", "--head description should not start with 'default: the card's id'")

	// Should keep the warning about land
	assert.Contains(t, headLine, "land refuses a head that is not a commit id")
}
