package swarm

import (
	"strings"
)

// CopiedResult is the name a published report is kept under.
const CopiedResult = "RESULT.md"

func dashOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return Dash
	}
	return s
}
