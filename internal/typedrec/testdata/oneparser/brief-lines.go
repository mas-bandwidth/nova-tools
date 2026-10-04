package oneparser

import (
	"fmt"
	"strings"
)

// briefLines produces a brief: the labels are output, spliced with runtime
// values and joined, so no line is a parser's token table.
func briefLines(base, repo string) string {
	lines := []string{
		"BASE: " + base,
		"REPO: " + repo,
		fmt.Sprintf("BRANCH: %s", repo),
	}
	return strings.Join(lines, "\n")
}
