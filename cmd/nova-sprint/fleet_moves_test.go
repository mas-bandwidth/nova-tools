package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// fleet down and fleet up say where the member's cards went (nova-tools#5096 item 21,
// the coordinator: "`fleet down` and `fleet up` print nothing about where the held
// member's running cards went (15 moved; to whom)"): down names how many moved and to
// which members, and which stayed (withdrawn, no member up having room); up names how
// many the level moved onto the member and from where.
func TestFleetDownAndUpSayWhereTheCardsWent(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.live = []string{"m1", "m2", "m3"}
	ta.ok("init --readers reader-a,reader-b --members m1:1,m2:1,m3:1")
	ta.ok("add --stream s --count 5")
	ta.deal(5) // each member holds two at width 1: m1 2, m2 2, m3 1
	out := ta.ok("fleet down m1")
	assert.Regexp(t, `; m1 held down; moved=1 to m3\(1\); stayed=1 withdrawn: s-\d\n`, out, "fleet down")
	out = ta.ok("fleet up m1")
	assert.Regexp(t, `; m1 up; moved=1 to m1\(1\) from m[23]\(1\)\n`, out, "fleet up")
}
