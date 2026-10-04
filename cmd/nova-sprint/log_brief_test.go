package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// log prints a brief by its size and the card that shows it, never the brief
// itself: a brief is a child's whole brief, and card <id> shows it. --json
// carries it whole.
func TestTheLogSaysABriefWithoutPrintingIt(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	brief := passingBrief("Fix the parser.")
	path := writeNeedsBrief(t, t.TempDir(), "brief", "Fix the parser.", "")
	ta.ok("add --stream s1 --count 1 --brief-file " + path)
	out := ta.ok("log")
	assert.NotContains(t, out, "Fix the parser.", "the brief's words")
	assert.NotContains(t, out, `\x0a`, "an escaped brief")
	assert.Contains(t, out, "\n    brief: ")
	assert.Contains(t, out, " bytes, shown by nova-sprint card s1-1\n")
	assert.Contains(t, ta.ok("card s1-1"), "Fix the parser.", "card shows the brief")
	var log struct {
		Lines []sprint.Line `json:"lines"`
	}
	ta.json("log", &log)
	whole := false
	for _, l := range log.Lines {
		whole = whole || strings.TrimSpace(l.Text["brief"]) == strings.TrimSpace(brief)
	}
	assert.True(t, whole, "--json carries the brief whole")
}
