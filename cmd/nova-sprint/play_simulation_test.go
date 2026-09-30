package main

import (
	"fmt"
	"strconv"
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
		// the chances its source draws with: down and up are the chance of one tick
		{"--simulation --seed 7 --every 2s", "chances: broken=0.1 fail=0.1 stuck=0.1 cross=0.01 down=0.0199 up=0.19 red=0 seed=7 every=2s"},
		{"--fail 0.3 --down 0.5 --up 0.2 --every 2s", "chances: broken=0.05 fail=0.3 stuck=0.1 cross=0.01 down=0.75 up=0.36 red=0 seed=1 every=2s"},
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

// The flags reach the source that draws: --every scales a chance each second
// to the chance of a tick, and --red is the chance a merge batch turns red.
// The line play prints is what the source draws with, and the draws show it.
func TestTheFlagsReachTheSourceThatDraws(t *testing.T) {
	t.Parallel()
	// 0.001 each second is 97 percent in a tick of an hour, and 0.1 percent
	// in a tick of a second: a machine falls silent in the first tick of the
	// one and not in the other
	for _, c := range []struct {
		every  string
		line   string
		silent bool
	}{
		{"1h", "down=0.972725 ", true},
		{"1s", "down=0.001 ", false},
	} {
		ta := playing(t)
		out := ta.ok("play --ticks 1 --seed 1 --down 0.001 --every " + c.every)
		if line := chancesLine(t, out); !strings.Contains(line, c.line) {
			t.Errorf("--every %s: %s\nwant %q in it", c.every, line, c.line)
		}
		if got := strings.Contains(out, "falls silent"); got != c.silent {
			t.Errorf("--every %s at 0.001 each second: a machine fell silent is %v, want %v\n%s", c.every, got, c.silent, out)
		}
	}
	// --red 1 with nothing else drawn: the first merge batch is red
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	ta.ok("add --stream s1 --count 6")
	ta.ok("start")
	for round := 1; round <= 60; round++ {
		ta.ok("tick")
		out := ta.ok(fmt.Sprintf("play --seed %d --ticks 3 --fail 0 --broken 0 --stuck 0 --cross 0 --red 1", round))
		if line := chancesLine(t, out); !strings.Contains(line, "red=1 ") {
			t.Fatalf("--red 1: %s", line)
		}
		if strings.Contains(out, " --red ") {
			return
		}
		ta.coordinate()
	}
	t.Fatalf("--red 1: no merge batch turned red in 60 rounds")
}

// A play that is refused prints nothing on stdout; one that plays says its
// chances first.
func TestAPlayThatIsRefusedPrintsNothingOnStdout(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	if code, out, errs := ta.do("play --simulation --seed 1 --ticks 1"); code != 2 || out != "" || !strings.Contains(errs, "no machine is running") {
		t.Fatalf("play with the machine stopped: exit %d, stdout %q, stderr %q", code, out, errs)
	}
	ta.ok("start")
	out := ta.ok("play --simulation --seed 1 --ticks 1")
	if !strings.HasPrefix(out, "chances: ") || !strings.Contains(out, "\ntick 1 ") {
		t.Fatalf("a play that plays says its chances before its first tick:\n%s", out)
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
		out := ta.ok(fmt.Sprintf("play --simulation %s --seed %d --ticks 5 --batch 5", flags, round))
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

// The machines of a run fall silent and beat again, and the sprint lands with
// them. Each round of play starts with every machine up and lasts five
// seconds, less than the fifteen of the beat deadline, so the fleet table
// never shows a machine down: this does not show a down machine's work dealt
// to the others, only that the sprint lands while machines fall silent.
func TestPlaySimulationWithMachinesGoingDownAndComingBackLandsEveryStream(t *testing.T) {
	t.Parallel()
	seen := landsUnder(t, "--down 0.3 --up 0.3")
	for _, name := range []string{"a machine went down", "a machine came back"} {
		if seen[name] == 0 {
			t.Errorf("no event of %q in the whole run", name)
		}
	}
}

// moved is the count a verb's printed line reports, and the member it ran as.
func movedLine(l string) (verb, as string, n int) {
	f := strings.Fields(l)
	if len(f) < 4 || f[0] != "nova-sprint" || f[2] != "--as" {
		return "", "", 0
	}
	for _, x := range f {
		if v, ok := strings.CutPrefix(x, "moved="); ok {
			n, _ = strconv.Atoi(v)
		}
	}
	return f[1], f[3], n
}

// The simulation batches (the owner's rulings of 2026-09-30: "we don't move
// one or two cards a turn like this. we batch."; "you should update each row
// in workers in fleet table, per-tick"): in a world tick one take moves every
// machine's whole ready queue, and in the next one finish (the failed in a
// second) moves all they took, each call naming every machine it acts for.
func TestPlaySimulationBatchesOneCallForEveryMachineATick(t *testing.T) {
	t.Parallel()
	members := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	ta := newTestApp(t)
	ta.live = members
	ta.ok("init --readers reader-a,reader-b,reader-c --members " + strings.Join(members, ","))
	ta.ok("add --stream s1 --count 150")
	ta.ok("start")
	ta.ok("tick") // the machine deals the ready queues
	ready := map[string]int{}
	total := 0
	for _, m := range members {
		var q struct {
			Cards []struct{ Col string } `json:"cards"`
		}
		ta.json("queue --as "+m, &q)
		for _, c := range q.Cards {
			if c.Col == "ready" {
				ready[m]++
				total++
			}
		}
	}
	if len(ready) != len(members) {
		t.Fatalf("the machine dealt %v: every machine wants a ready queue", ready)
	}
	ta.live = nil // from here the driver's machines beat
	out := ta.ok("play --simulation --down 0 --seed 1 --ticks 2")
	one, two, _ := strings.Cut(out, "\ntick 2 ")
	all := strings.Join(members, ",")
	takes, taken := 0, 0
	for _, l := range strings.Split(one, "\n") {
		if verb, as, n := movedLine(strings.TrimSpace(l)); verb == "take" {
			takes++
			taken += n
			if as != all {
				t.Errorf("the take names %s, want every machine, %s:\n%s", as, all, l)
			}
		} else if verb != "" {
			t.Errorf("the first world tick ran %s before anything was taken:\n%s", verb, l)
		}
	}
	finishes, finished := 0, 0
	for _, l := range strings.Split(two, "\n") {
		if verb, as, n := movedLine(strings.TrimSpace(l)); verb == "finish" {
			finishes++
			finished += n
			if as != all {
				t.Errorf("a finish names %s, want every machine, %s:\n%s", as, all, l)
			}
		} else if verb == "take" {
			t.Errorf("a machine took with its queue taken:\n%s", l)
		}
	}
	if takes != 1 || taken != total {
		t.Errorf("%d take calls moving %d of the %d ready, want one moving all\n%s", takes, taken, total, out)
	}
	if finishes < 1 || finishes > 2 || finished != taken {
		t.Errorf("%d finish calls moving %d of the %d taken, want one or two moving all\n%s", finishes, finished, taken, out)
	}
	t.Logf("%d ready over %d machines: one take, %d finishes", total, len(members), finishes)
}
