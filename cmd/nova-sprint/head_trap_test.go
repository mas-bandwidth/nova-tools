package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The head trap: a finish without --head records the card's id as its head,
// which land cannot merge. The worker's packet names --head <commit> on its
// report line, so the command it pastes names the commit.
func TestThePacketsReportLineNamesTheHead(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	out := ta.ok("take --as m1 s1-1.w1@1")
	assert.Contains(t, out, "  report it: nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --branch sprint/s1-1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]")
	ta.clean()
}
