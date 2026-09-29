package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// coordinate is a coordinator by rule: accept what two readers passed, rework
// what came back failed or broken, resume a stopped stream. It is what the
// person at the inbox does, typed as the same verbs.
func (ta *testApp) coordinate() {
	ta.t.Helper()
	_, _, _ = ta.do("accept --read-ok")
	var in struct{ Groups []sprint.Group }
	ta.json("inbox", &in)
	for _, g := range in.Groups {
		if g.Kind != sprint.Judgment {
			continue
		}
		switch {
		case g.Type == sprint.NWorkFailed || g.Type == sprint.NReadBroken:
			ta.ok(fmt.Sprintf("rework --group %d --fix 'the fix'", g.N))
			return // the numbering moves: read the inbox again next round
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
		ta.ok("tick") // the machine: every mechanical move
		out := ta.ok(fmt.Sprintf("play --seed %d --ticks 1 --every 1s --fail 0.1 --broken 0.05 --stuck 0.1 --cross 0 --batch 10 --take 20 --reads 20", round))
		if round == 1 && !strings.Contains(out, "tick 1") {
			t.Fatalf("the first tick:\n%s", out)
		}
		for _, bad := range []string{"nova-sprint accept", "nova-sprint rework", "nova-sprint resume", "nova-sprint drop", "nova-sprint rank",
			"nova-sprint start", "nova-sprint resolve", "nova-sprint ask", "nova-sprint tick"} {
			if strings.Contains(out, bad) {
				t.Fatalf("the driver ran a coordinator verb:\n%s", out)
			}
		}
		ta.clean()
		if strings.Contains(out, "every stream has landed") {
			var w whereView
			ta.json("where", &w)
			if w.Landed != 45 || w.All != 45 {
				t.Fatalf("landed %d of %d", w.Landed, w.All)
			}
			return
		}
		ta.coordinate()
		ta.clean()
	}
	t.Fatalf("not landed after 300 rounds: %s", ta.ok("where"))
}

func TestPlaySaysWhatWaitsForTheCoordinator(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	if code, _, errs := ta.do("play --seed 1 --ticks 1"); code != 2 || !strings.Contains(errs, "no machine is running") {
		t.Fatalf("play with the machine stopped: %d %s", code, errs)
	}
	ta.ok("start")
	out := ""
	for round := 1; round <= 3; round++ {
		ta.ok("tick")
		out = ta.ok(fmt.Sprintf("play --seed %d --ticks 1 --fail 1 --every 1m", round))
	}
	if !strings.Contains(out, "waits for the coordinator: work came back failed s1 x3") || !strings.Contains(out, "PLAY OK stopped=ticks") {
		t.Fatalf("play:\n%s", out)
	}
	var in struct{ Groups []sprint.Group }
	ta.json("inbox", &in)
	if len(in.Groups) == 0 || in.Groups[0].Type != sprint.NWorkFailed {
		t.Fatalf("inbox: %+v", in.Groups)
	}
	if b, _ := json.Marshal(in.Groups[0]); strings.Contains(string(b), `"overdue":true`) {
		t.Fatalf("overdue before its deadline: %s", b)
	}
	ta.a.sleep(20 * time.Minute)
	ta.json("inbox", &in)
	if b, _ := json.Marshal(in.Groups[0]); !strings.Contains(string(b), `"overdue":true`) {
		t.Fatalf("not overdue at read time past its deadline: %s", b)
	}
}
