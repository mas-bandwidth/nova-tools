package store

// Cold reader: a tick that fails at every store call, then recovers.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
)

func TestCRTickFailsAtEveryCallAndRecovers(t *testing.T) {
	t.Parallel()
	for k := 1; k <= 400; k += crScale.CallStride {
		h := raceScene(t)
		h.startMachine()
		calls := 0
		fired := ""
		h.m.Fail = func(p string) error {
			calls++
			if calls == k {
				fired = p
				return errors.New("the store went away")
			}
			return nil
		}
		_, terr := h.st.Tick(h.ctx)
		h.m.Fail = nil
		if fired == "" {
			t.Logf("the tick makes %d failable calls", calls)
			break
		}
		where := fmt.Sprintf("fail %q (call %d)", fired, k)
		line := h.st.MachineLine(h.ctx)
		if strings.Contains(line, "failed") || strings.Contains(line, "(") {
			assert.Fail(t, fmt.Sprintf("%s: tick error %v and the line carries a suffix: %q", where, terr, line))
		}
		for i := 0; i < 3; i++ {
			h.tick(2 * time.Minute) // past the grace
			_, err := h.st.Tick(h.ctx)
			assert.NoError(t, err, "%s: recovery tick %d: %v", where, i, err)
		}
		h.clean(where)
		l := h.st.MachineLine(h.ctx)
		assert.NotContains(t, l, "failed", "%s: after recovery: %q", where, l)
		s := h.snap()
		// every due move happened: w resolved and dealt, s3 resumed, rv asked, s1 dealt
		assert.NotEqual(t, sprint.Waiting, s.StateOf("w"), "%s: w still waiting after recovery", where)
		assert.NotEqual(t, string(sprint.StreamStopped), s.StreamCtl("s3").F("state"), "%s: s3 still stopped after recovery", where)
		assert.Len(t, s.Readers.Of("rv"), 2, "%s: rv asked of %d", where, len(s.Readers.Of("rv")))
		n := h.written(sprint.NResumed)
		assert.Equal(t, 1, n, "%s: resumed written %d", where, n)
		notes, _, _ := h.m.NotesSince(h.ctx, "", 100000)
		seen := map[string]int{}
		for _, n := range notes {
			// a failed tick's note and the recovery's each wake the coordinator
			// with a tick end of their own (TestRecoveryAddsOneNoteWithTheFailedCount)
			if n.Who == sprint.MachineActor && n.Type != sprint.NTickEnd {
				seen[n.Type+"|"+strings.Join(n.Subjects(), ",")]++
			}
		}
		for key, v := range seen {
			assert.LessOrEqual(t, v, 1, "%s: the machine wrote %s %d times", where, key, v)
		}
	}
	_, hb, _ := func() (Machine, Heartbeat, error) { h := newHarness(t); return h.st.Machine(h.ctx) }()
	_ = hb
}

// The heartbeat's count of failures in a row.
func TestCRFailuresInARow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.m.Fail = func(p string) error {
		if p == "fence" {
			return errors.New("down")
		}
		return nil
	}
	for i := 0; i < 3; i++ {
		_, _ = h.st.Tick(h.ctx)
		h.tick(time.Second)
	}
	h.m.Fail = nil
	_, hb, _ := h.st.Machine(h.ctx)
	assert.Equal(t, 3, hb.Failures, "heartbeat failures after three failed ticks in a row: %d (want 3)", hb.Failures)
}
