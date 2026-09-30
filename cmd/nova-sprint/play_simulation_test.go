package main

import (
	"fmt"
	"strings"
	"testing"
)

// chancesLine is the chances play prints before its first tick.
func chancesLine(t *testing.T, out string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "chances: ") {
			return l
		}
	}
	t.Fatalf("play printed no chances:\n%s", out)
	return ""
}

// playing is a sprint with its machine running and one stream, ready to play.
func playing(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	ta.ok("add --stream s1 --count 6")
	ta.ok("start")
	return ta
}

func TestPlaySaysItsChances(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ args, want string }{
		{"", "chances: broken=0.05 fail=0.1 stuck=0.1 cross=0.01 down=0 up=0 red=0 seed=1 every=1s"},
		{"--simulation", "chances: broken=0.1 fail=0.1 stuck=0.1 cross=0.01 down=0.01 up=0.1 red=0 seed=1 every=1s"},
		{"--simulation --seed 7 --every 2s", "chances: broken=0.1 fail=0.1 stuck=0.1 cross=0.01 down=0.01 up=0.1 red=0 seed=7 every=2s"},
		{"--simulation --broken 0.5", "chances: broken=0.5 fail=0.1 stuck=0.1 cross=0.01 down=0.01 up=0.1 red=0 seed=1 every=1s"},
		{"--broken 0.5 --simulation", "chances: broken=0.5 fail=0.1 stuck=0.1 cross=0.01 down=0.01 up=0.1 red=0 seed=1 every=1s"},
		{"--simulation --fail 0 --down 0.5 --up 1", "chances: broken=0.1 fail=0 stuck=0.1 cross=0.01 down=0.5 up=1 red=0 seed=1 every=1s"},
		{"--simulation --stuck 0.3 --cross 0.2 --up 0.4", "chances: broken=0.1 fail=0.1 stuck=0.3 cross=0.2 down=0.01 up=0.4 red=0 seed=1 every=1s"},
		{"--simulation --red 0.2", "chances: broken=0.1 fail=0.1 stuck=0.1 cross=0.01 down=0.01 up=0.1 red=0.2 seed=1 every=1s"},
		{"--flap 0.2", "chances: broken=0.05 fail=0.1 stuck=0.1 cross=0.01 down=0.2 up=0.2 red=0 seed=1 every=1s"},
		{"--simulation --flap 0.2 --up 0.5", "chances: broken=0.1 fail=0.1 stuck=0.1 cross=0.01 down=0.2 up=0.5 red=0 seed=1 every=1s"},
		{"--fail 0.3 --down 0.02", "chances: broken=0.05 fail=0.3 stuck=0.1 cross=0.01 down=0.02 up=0 red=0 seed=1 every=1s"},
	} {
		ta := playing(t)
		out := ta.ok("play --ticks 1 " + c.args)
		if got := chancesLine(t, out); got != c.want {
			t.Errorf("play %s:\n%s\nwant %s", c.args, got, c.want)
		}
		if !strings.Contains(out, "PLAY OK stopped=ticks") {
			t.Errorf("play %s:\n%s", c.args, out)
		}
	}
}

func TestPlayRefusesAChanceOutsideZeroToOne(t *testing.T) {
	t.Parallel()
	ta := playing(t)
	for _, flag := range []string{"broken", "fail", "stuck", "cross", "down", "up", "red", "flap"} {
		for _, simulation := range []string{"", "--simulation "} {
			line := fmt.Sprintf("play --ticks 1 %s--%s 1.5", simulation, flag)
			if code, out, errs := ta.do(line); code != 2 || out != "" || !strings.Contains(errs, "--"+flag+" wants a chance from 0 to 1, found 1.5") {
				t.Errorf("%s: exit %d %q %q", line, code, out, errs)
			}
		}
	}
}

// simulationEvents are what the simulation plays, each the words it leaves in
// play's output.
var simulationEvents = map[string]string{
	"work came back not ok":    " --failed ",
	"a reader found it broken": " --broken ",
	"a merge needed help":      " --conflict ",
	"a machine went down":      "falls silent",
	"a machine came back":      "beats again",
}

// landsUnder plays the simulation with the flags given, a round at a time (a
// tick of the machine, five ticks of play with the round's seed, the
// coordinator's rule), until every stream has landed, and returns how often each of the
// events played. Every line play typed is one of the outside actors' verbs.
func landsUnder(t *testing.T, flags string) map[string]int {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2,m3")
	for _, s := range []string{"s1", "s2", "s3"} {
		ta.ok("add --stream " + s + " --count 20")
	}
	ta.ok("start")
	ta.beat()
	ta.live = nil // from here the driver's machines beat, and a silent one is silent
	seen := map[string]int{}
	for round := 1; round <= 600; round++ {
		ta.ok("tick")
		out := ta.ok(fmt.Sprintf("play --simulation %s --seed %d --ticks 5 --batch 5 --take 20 --reads 20", flags, round))
		for _, l := range strings.Split(out, "\n") {
			if verb, ok := strings.CutPrefix(l, "  nova-sprint "); ok {
				switch v := strings.Fields(verb)[0]; v {
				case "take", "finish", "read", "merge", "fleet":
				default:
					t.Fatalf("the simulation ran %s:\n%s", v, out)
				}
			}
		}
		for name, words := range simulationEvents {
			seen[name] += strings.Count(out, words)
		}
		ta.clean()
		if strings.Contains(out, "every stream has landed") {
			t.Logf("play --simulation %s landed after %d rounds; events %v", flags, round, seen)
			return seen
		}
		ta.coordinate()
		ta.clean()
	}
	t.Fatalf("play --simulation %s: not landed after 600 rounds: %s", flags, ta.ok("where"))
	return nil
}

func TestPlaySimulationLandsEveryStream(t *testing.T) {
	t.Parallel()
	seen := landsUnder(t, "")
	for _, name := range []string{"work came back not ok", "a reader found it broken", "a merge needed help"} {
		if seen[name] == 0 {
			t.Errorf("no event of %q in the whole run", name)
		}
	}
}

// The machines of a run go down and come back, and the sprint lands with them
// (a down machine's work is dealt to the others by the machine's tick).
func TestPlaySimulationWithMachinesGoingDownAndComingBackLandsEveryStream(t *testing.T) {
	t.Parallel()
	seen := landsUnder(t, "--down 0.3 --up 0.3")
	for _, name := range []string{"a machine went down", "a machine came back"} {
		if seen[name] == 0 {
			t.Errorf("no event of %q in the whole run", name)
		}
	}
}
