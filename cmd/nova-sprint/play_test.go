package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// tickDone is the machine's tick, and whether it found the sprint done: the
// merge queues the last landings for the next tick's pump, which drains them,
// and the same tick's done part stops the machine, so the driver (it plays
// only while a machine runs) is not run after it.
func (ta *testApp) tickDone() bool {
	ta.t.Helper()
	return strings.Contains(ta.ok("tick"), "the sprint is done")
}

// coordinate is a coordinator by rule: rework what came back failed or broken,
// resume a stopped stream, and leave a stream stopped for another stream's card
// to the machine, which resumes it when that card lands (T7): resume refuses it
// while the card is not landed. The machine accepts what two readers passed
// (the pump's accept: "accept is mechanical"). It is what the person at the
// inbox does, typed as the same verbs.
func (ta *testApp) coordinate() {
	ta.t.Helper()
	var in struct{ Groups []sprint.Group }
	ta.json("inbox", &in)
	for _, g := range in.Groups {
		if g.Kind != sprint.Judgment {
			continue
		}
		switch {
		case g.Type == sprint.NWorkFailed || g.Type == sprint.NReadBroken || g.Type == sprint.NBound:
			// the bound's judgment: the second identical failure too (rule 2), reworked with a fix
			ta.ok(fmt.Sprintf("rework --group %s --expect %d --fix 'the fix'", g.ID, g.Size))
			return // read the inbox again next round
		case g.Type == sprint.NCross:
			continue
		case strings.HasPrefix(g.Type, "stream stopped"):
			ta.ok("resume --stream " + g.Stream + " --did 'resolved'")
			return
		}
	}
}

func TestPlayWithACoordinatorLandsEveryStream(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	for _, s := range []string{"s1", "s2", "s3"} {
		ta.ok("add --stream " + s + " --count 15")
	}
	ta.ok("start")
	for round := 1; round <= 300; round++ {
		if ta.tickDone() { // the machine: every mechanical move
			var w whereView
			ta.json("where", &w)
			// the epoch's: the tick archives each stream as its last card lands
			landed, all := epochCounts(w)
			require.EqualValues(t, 45, landed, "done with %d landed of %d", landed, all)
			require.EqualValues(t, 45, all, "done with %d landed of %d", landed, all)
			return
		}
		out := ta.ok(fmt.Sprintf("play --seed %d --ticks 1 --every 1s --fail 0.1 --broken 0.05 --stuck 0.1 --cross 0 --batch 10 --take 20 --reads 20", round))
		require.False(t, round == 1 && !strings.Contains(out, "tick 1"), "the first tick:\n%s", out)
		for _, bad := range []string{"nova-sprint accept", "nova-sprint rework", "nova-sprint resume", "nova-sprint drop", "nova-sprint rank",
			"nova-sprint start", "nova-sprint resolve", "nova-sprint ask", "nova-sprint tick"} {
			require.NotContains(t, out, bad, "the driver ran a coordinator verb")
		}
		ta.clean()
		if strings.Contains(out, "every stream has landed") {
			var w whereView
			ta.json("where", &w)
			// the epoch's: the tick archives each stream as its last card lands
			landed, all := epochCounts(w)
			require.EqualValues(t, 45, landed, "landed %d of %d", landed, all)
			require.EqualValues(t, 45, all, "landed %d of %d", landed, all)
			return
		}
		ta.coordinate()
		ta.clean()
	}
	t.Fatalf("not landed after 300 rounds: %s\n%s\n%s", ta.ok("where"), ta.ok("inbox"), ta.ok("card s3-5"))
}

func TestPlaySaysWhatWaitsForTheCoordinator(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	code, _, errs := ta.do("play --seed 1 --ticks 1")
	require.Equal(t, 2, code, "play with the machine stopped: %d %s", code, errs)
	require.Contains(t, errs, "no machine is running", "play with the machine stopped: %d %s", code, errs)
	ta.ok("start")
	out := ""
	for round := 1; round <= 3; round++ {
		ta.ok("tick")
		out = ta.ok(fmt.Sprintf("play --seed %d --ticks 1 --fail 1 --every 1m", round))
	}
	require.Contains(t, out, "work came back failed s1 x3", "play:\n%s", out)
	require.Contains(t, out, "PLAY OK stopped=ticks", "play:\n%s", out)
	var in struct{ Groups []sprint.Group }
	failed := func() sprint.Group {
		t.Helper()
		ta.json("inbox", &in)
		for _, g := range in.Groups {
			if g.Type == sprint.NWorkFailed {
				return g
			}
		}
		t.Fatalf("inbox: %+v", in.Groups)
		return sprint.Group{}
	}
	b, _ := json.Marshal(failed())
	require.NotContains(t, string(b), `"overdue":true`, "overdue before its deadline")
	ta.a.sleep(20 * time.Minute)
	b, _ = json.Marshal(failed())
	require.Contains(t, string(b), `"overdue":true`, "not overdue at read time past its deadline")
}
