package store

// Cold reader: a tick that fails at every store call, then recovers.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func TestCRTickFailsAtEveryCallAndRecovers(t *testing.T) {
	t.Parallel()
	for k := 1; k <= 400; k++ {
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
		if terr != nil && fired != "kv" && !strings.Contains(line, "last tick failed") {
			t.Errorf("%s: tick error %v but the line says %q", where, terr, line)
		}
		for i := 0; i < 3; i++ {
			h.tick(2 * time.Minute) // past the grace
			if _, err := h.st.Tick(h.ctx); err != nil {
				t.Errorf("%s: recovery tick %d: %v", where, i, err)
			}
		}
		h.clean(where)
		if l := h.st.MachineLine(h.ctx); strings.Contains(l, "failed") {
			t.Errorf("%s: after recovery: %q", where, l)
		}
		s := h.snap()
		// every due move happened: w resolved and dealt, s3 resumed, rv asked, s1 dealt
		if st := s.StateOf("w"); st == sprint.Waiting {
			t.Errorf("%s: w still waiting after recovery", where)
		}
		if st := s.StreamCtl("s3").F("state"); st == sprint.StreamStopped {
			t.Errorf("%s: s3 still stopped after recovery", where)
		}
		if len(s.Readers.Of("rv")) != 2 {
			t.Errorf("%s: rv asked of %d", where, len(s.Readers.Of("rv")))
		}
		if n := h.written(sprint.NResumed); n != 1 {
			t.Errorf("%s: resumed written %d", where, n)
		}
		notes, _, _ := h.m.NotesSince(h.ctx, "", 100000)
		seen := map[string]int{}
		for _, n := range notes {
			if n.Who == sprint.MachineActor {
				seen[n.Type+"|"+strings.Join(n.Subjects(), ",")]++
			}
		}
		for key, v := range seen {
			if v > 1 {
				t.Errorf("%s: the machine wrote %s %d times", where, key, v)
			}
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
	if hb.Failures != 3 {
		t.Errorf("heartbeat failures after three failed ticks in a row: %d (want 3)", hb.Failures)
	}
}
