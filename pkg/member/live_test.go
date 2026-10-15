package member

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

type endedChild struct{ done bool }

func (c endedChild) Done() bool     { return c.done }
func (c endedChild) Result() Result { return Result{} }

// The member's beat carries its live set (fleet beat --live; v1.2.6, tla/LiveRuns.tla): a
// launch whose child runs at its generation, one whose child has ended held (its report is
// the card's, sent again at that generation once the machine runs), a spent one none.
func TestTheLiveSetNamesRunsAndHeldReports(t *testing.T) {
	t.Parallel()
	m := New(Config{As: "m1"}, nil, nil, nil, io.Discard)
	assert.Equal(t, "-", m.liveWords(), "nothing running: the empty set")
	m.running["a"] = launch{gen: 2, child: endedChild{}}
	m.running["b"] = launch{gen: 1, child: endedChild{done: true}}
	m.running["c"] = launch{gen: 3, res: &Result{}}
	m.running["d"] = launch{gen: 1, spent: true}
	m.running["e"] = launch{gen: 1, child: endedChild{done: true}, stopped: true}
	assert.Equal(t, "a@2,b@1:held,c@3:held,e@1", m.liveWords())
	assert.Equal(t, "a@2,b@1:held,c@3:held,e@1", *m.live.Load(), "the beat reads what the pass left")
}

func TestAReportRefusedForTheStopIsKept(t *testing.T) {
	t.Parallel()
	assert.True(t, stoppedRefusal([]byte("REFUSED finish: the machine is STOPPED: a late work or read report cannot finish\n")))
	assert.False(t, stoppedRefusal([]byte("REFUSED finish: x: stale: generation 1 is not the live one (2)\n")))
}
