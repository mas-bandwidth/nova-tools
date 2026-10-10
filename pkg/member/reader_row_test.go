package member

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reader whose name is no row of the readers table beats nothing and is asked
// nothing: the coordinator declares readers (init --readers, reader add;
// docs/SPEC-SPRINT.md section 6), and the queue's answer says whether the name is
// one (reader). The loop says so once, naming the verb, and once when the row is
// there (nova-tools#5096 item 23: five readers beat for five minutes with no row,
// and nothing said so).
func TestAReaderWithNoRowSaysSoOnceAndWhenItsRowCame(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "reader-x-2", Width: 2, Reader: true})
	g.s.set("queue", 0, `{"as":"reader-x-2","epoch":7,"cards":[],"reader":false}`)
	for range 3 {
		_, err := g.tick(t)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, strings.Count(g.out.String(), "MEMBER NOT A READER reader-x-2: no row of the readers table, so its queue is no beat and it is asked nothing; the coordinator declares it: nova-sprint reader add reader-x-2"), g.out.String())
	g.s.set("queue", 0, `{"as":"reader-x-2","epoch":7,"cards":[],"reader":true}`)
	for range 2 {
		_, err := g.tick(t)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, strings.Count(g.out.String(), "NOTE reader reader-x-2: the readers table has its row; it beats and is asked reads"), g.out.String())
}

// A server from before the field says nothing of the row, and a reader whose row is
// there from the start says nothing either.
func TestAReaderWithItsRowOrAnOldServerSaysNothing(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{`{"as":"r","epoch":7,"cards":[]}`, `{"as":"r","epoch":7,"cards":[],"reader":true}`} {
		g := newRig(Config{As: "r", Width: 2, Reader: true})
		g.s.set("queue", 0, answer)
		_, err := g.tick(t)
		require.NoError(t, err)
		assert.NotContains(t, g.out.String(), "NOT A READER", answer)
		assert.NotContains(t, g.out.String(), "has its row", answer)
	}
}
