package sprint

import "strings"

// instanceLines is a decision the raising code names per instance as the lines that make
// it (coordinator_pass.go, friend_stall.go): a nova-sprint friend verb as it is named, a
// nova-friend command as it is (another tool's, run as printed), and a pointer to the
// debugging steps as a comment line. nil for any other decision: every decision a judgment
// offers prints a line (TestEveryPrintedDecisionCommandRuns, cmd/nova-sprint).
func instanceLines(d string) []string {
	switch {
	case strings.HasPrefix(d, "friend take ") || strings.HasPrefix(d, "friend down "):
		return []string{"nova-sprint " + d}
	case strings.HasPrefix(d, "nova-friend "):
		return []string{d}
	case strings.HasPrefix(d, "debug: "):
		return []string{"# read " + strings.TrimPrefix(d, "debug: ")}
	}
	return nil
}
