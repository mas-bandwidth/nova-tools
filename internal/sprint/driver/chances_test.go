package driver

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// locked is the owner's simulation, typed here once more and apart from the
// table, so that a change to a row of the table is a red test until the
// owner's word is changed with it.
var locked = Chances{Broken: 0.10, Fail: 0.10, Stuck: 0.10, Cross: 0.01, Down: 0.01, Up: 0.10}

func TestTheSimulationIsTheSixLockedChances(t *testing.T) {
	t.Parallel()
	got, err := Set(true, nil)
	if err != nil || got != locked {
		t.Fatalf("simulation: %+v %v\nwant %+v", got, err, locked)
	}
	if len(ChanceRows) != 6 {
		t.Fatalf("%d chances in the table, not six", len(ChanceRows))
	}
	seen := map[string]bool{}
	for _, r := range ChanceRows {
		if seen[r.Flag] || Valid(r.Flag, r.Plain) != nil || Valid(r.Flag, r.Locked) != nil || r.Usage == "" {
			t.Errorf("row %s is a repeat, out of range or unexplained", r.Flag)
		}
		seen[r.Flag] = true
	}
}

func TestWithoutTheSimulationTheChancesAreTheirPlainValues(t *testing.T) {
	t.Parallel()
	got, err := Set(false, nil)
	want := Chances{Broken: 0.05, Fail: 0.10, Stuck: 0.10, Cross: 0.01}
	if err != nil || got != want {
		t.Fatalf("plain: %+v %v\nwant %+v", got, err, want)
	}
}

// A chance given beside --simulation sets that one chance and no other, and a
// chance of 0 given is 0.
func TestAFlagBesideTheSimulationSetsThatOneChance(t *testing.T) {
	t.Parallel()
	for _, r := range ChanceRows {
		for _, v := range []float64{0.5, 0, 1} {
			got, err := Set(true, map[string]float64{r.Flag: v})
			if err != nil {
				t.Fatal(err)
			}
			want := locked
			*r.At(&want) = v
			if got != want {
				t.Errorf("--simulation --%s %v: %+v\nwant %+v", r.Flag, v, got, want)
			}
		}
	}
	got, _ := Set(true, map[string]float64{"broken": 0.5})
	if got != (Chances{Broken: 0.5, Fail: 0.10, Stuck: 0.10, Cross: 0.01, Down: 0.01, Up: 0.10}) {
		t.Fatalf("--simulation --broken 0.5: %+v", got)
	}
}

func TestFlapIsDownAndUpUnlessTheyAreNamed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		simulation bool
		given      map[string]float64
		down, up   float64
	}{
		{false, map[string]float64{"flap": 0.2}, 0.2, 0.2},
		{false, map[string]float64{"flap": 0.2, "down": 0.5}, 0.5, 0.2},
		{false, map[string]float64{"flap": 0.2, "up": 0.5}, 0.2, 0.5},
		{true, map[string]float64{"flap": 0.3}, 0.3, 0.3},
		{true, map[string]float64{"flap": 0.3, "down": 0, "up": 1}, 0, 1},
		{true, nil, 0.01, 0.10},
	} {
		got, err := Set(c.simulation, c.given)
		if err != nil || got.Down != c.down || got.Up != c.up {
			t.Errorf("%v %v: down %v up %v (%v), want %v and %v", c.simulation, c.given, got.Down, got.Up, err, c.down, c.up)
		}
	}
}

func TestAChanceOutsideZeroToOneIsRefusedByName(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"broken", "fail", "stuck", "cross", "down", "up", "flap"} {
		for _, v := range []float64{1.5, -0.1, math.NaN(), math.Inf(1)} {
			for _, simulation := range []bool{false, true} {
				_, err := Set(simulation, map[string]float64{flag: v})
				if err == nil || !strings.Contains(err.Error(), "--"+flag+" wants a chance from 0 to 1") {
					t.Errorf("--%s %v (simulation %v): %v", flag, v, simulation, err)
				}
			}
		}
	}
	if err := Valid("red", 2); err == nil || !strings.Contains(err.Error(), "--red") {
		t.Errorf("--red 2: %v", err)
	}
}

func TestTheChancesPrintAsTheFlagsThatSetThem(t *testing.T) {
	t.Parallel()
	if got, want := locked.String(), "broken=0.1 fail=0.1 stuck=0.1 cross=0.01 down=0.01 up=0.1"; got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}
}

// A chance each second is the chance of a tick as long as a second, and a
// longer or shorter tick draws the chance of at least one event in its time.
func TestAChanceEachSecondIsScaledToTheTick(t *testing.T) {
	t.Parallel()
	for _, p := range []float64{0, 0.01, 0.10, 0.5, 1} {
		if got := PerTick(p, time.Second); got != p {
			t.Errorf("a second's tick of %v: %v", p, got)
		}
		if got := PerTick(p, 0); got != p {
			t.Errorf("a tick of no time of %v: %v", p, got)
		}
		half := PerTick(p, 500*time.Millisecond)
		if got := 1 - (1-half)*(1-half); math.Abs(got-p) > 1e-12 {
			t.Errorf("two ticks of half a second of %v make %v", p, got)
		}
		if got := PerTick(p, time.Minute); got < p || got > 1 || p == 0 && got != 0 || p == 1 && got != 1 {
			t.Errorf("a minute's tick of %v: %v", p, got)
		}
	}
	if got := PerTick(0.01, time.Minute); got < 0.44 || got > 0.46 {
		t.Errorf("1%% each second is %v in a minute's tick, about 45%%", got)
	}
}

// draws is the seeded facts' draws at a chance each: how often each came up.
func draws(s *Seeded, n int) map[string]float64 {
	hits := map[string]int{}
	others := func() []string { return []string{"o1", "o2"} }
	batch := []string{"a", "b", "c"}
	for i := 0; i < n; i++ {
		if ok, _ := s.Work("c"); !ok {
			hits["fail"]++
		}
		if ok, _ := s.Read("c"); !ok {
			hits["broken"]++
		}
		switch out := s.Merge("s1", batch, others); {
		case out.Conflict != "":
			hits["stuck"]++
		case out.Cross != "":
			hits["cross"]++
		}
		next := s.Up(i, []string{"a", "b"}, map[string]bool{"a": true})
		if !next["a"] {
			hits["down"]++
		}
		if next["b"] {
			hits["up"]++
		}
	}
	out := map[string]float64{}
	for k, v := range hits {
		out[k] = float64(v) / float64(n)
	}
	return out
}

// near says a rate over n draws is within one percentage point of its
// setting p, and also within five standard deviations of it: at 1 percent a
// rate of 0 is within a point, and is not the chance.
func near(rate, p float64, n int) bool {
	return math.Abs(rate-p) <= min(0.01, 5*math.Sqrt(p*(1-p)/float64(n)))
}

// Over 100,000 draws with a fixed seed each of the six chances comes up
// within one percentage point of its setting.
func TestEveryChanceIsWithinAPointOfItsSetting(t *testing.T) {
	t.Parallel()
	const n = 100000
	s := NewSeeded(20260929)
	c, err := Set(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Use(c, time.Second)
	got := draws(s, n)
	want := map[string]float64{"broken": 0.10, "fail": 0.10, "stuck": 0.10, "cross": 0.01, "down": 0.01, "up": 0.10}
	for flag, p := range want {
		if !near(got[flag], p, n) {
			t.Errorf("%s: %.4f in %d draws, its setting is %v", flag, got[flag], n, p)
		}
		t.Logf("%-6s %.4f (setting %v)", flag, got[flag], p)
	}
}

// distinct is the six chances each with a value of its own, no two within two
// points of each other. The locked values repeat 0.10 three times, so a draw
// made against another of those three chances' value passes the test of the
// locked values; with these it is a rate that is not the chance's setting.
var distinct = map[string]float64{"fail": 0.10, "broken": 0.20, "stuck": 0.30, "cross": 0.04, "down": 0.07, "up": 0.40}

// The same holds for six chances given beside the simulation, each a different
// value: every chance is drawn against its own value and no other's.
func TestAChanceGivenBesideTheSimulationDrawsAsGiven(t *testing.T) {
	t.Parallel()
	const n = 100000
	for a, p := range distinct {
		for b, q := range distinct {
			if a != b && math.Abs(p-q) < 0.02 {
				t.Fatalf("--%s %v and --%s %v are too near for the test to tell them apart", a, p, b, q)
			}
		}
	}
	s := NewSeeded(5)
	c, err := Set(true, distinct)
	if err != nil {
		t.Fatal(err)
	}
	s.Use(c, time.Second)
	got := draws(s, n)
	for flag, p := range distinct {
		if !near(got[flag], p, n) {
			t.Errorf("%s: %.4f in %d draws, its setting is %v", flag, got[flag], n, p)
		}
	}
}

// A chance of 0 draws nothing from the seed: a plain run (no --down and no
// --up, so both are 0) draws exactly the sequence it drew before those flags
// existed, when nothing was drawn for a member at a chance of 0. The
// reference is the same source that is never asked which members are up.
func TestAPlainRunDrawsTheSequenceItDrewBeforeDownAndUpExisted(t *testing.T) {
	t.Parallel()
	plain, err := Set(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	played, before := NewSeeded(31), NewSeeded(31)
	played.Use(plain, time.Second)
	before.Use(plain, time.Second)
	members := []string{"a", "b", "c"}
	up := map[string]bool{"a": true, "b": false, "c": true}
	others := func() []string { return []string{"o1", "o2"} }
	batch := []string{"x", "y", "z"}
	for i := 0; i < 2000; i++ {
		wp, rp := played.Work("c")
		wb, rb := before.Work("c")
		fp, np := played.Read("c")
		fb, nb := before.Read("c")
		mp, mb := played.Merge("s1", batch, others), before.Merge("s1", batch, others)
		next := played.Up(i, members, up)
		if wp != wb || rp != rb || fp != fb || np != nb || fmt.Sprint(mp) != fmt.Sprint(mb) {
			t.Fatalf("tick %d: the plain run drew another sequence than before", i)
		}
		for _, m := range members {
			if next[m] != up[m] {
				t.Fatalf("tick %d: %s changed with a chance of 0", i, m)
			}
		}
	}
	if a, b := played.rng.Float64(), before.rng.Float64(); a != b {
		t.Fatalf("the plain run spent draws that the run before did not: %v against %v", a, b)
	}
}

// ticked is the seeded facts that say which tick they are asked about.
type ticked struct {
	*Seeded
	tick int
}

func (f *ticked) Up(tick int, members []string, up map[string]bool) map[string]bool {
	f.tick = tick
	return f.Seeded.Up(tick, members, up)
}

// A machine that is down takes no work, and does not beat; a machine that
// comes back beats again and takes work again. With a chance of 1 each way the
// machine is down on the odd ticks and back on the even ones.
func TestAMachineThatIsDownTakesNoWorkAndOneThatComesBackTakesWorkAgain(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy}, queue: map[string]string{
		"m1":       `{"cards":[{"id":"s1-4.w1","col":"ready","gen":2}]}`,
		"reader-a": `{"cards":[]}`,
		"s1":       `{"cards":[]}`,
	}, inbox: `{"groups":[]}`}
	f := &ticked{Seeded: NewSeeded(1)}
	f.Use(Chances{Down: 1, Up: 1}, time.Second)
	takes, beats := map[int]int{}, map[int]int{}
	run := func(args []string, stdout, stderr io.Writer) int {
		switch strings.Join(args[:min(len(args), 3)], " ") {
		case "take --as m1":
			takes[f.tick]++
		case "fleet beat m1":
			beats[f.tick]++
		}
		return w.run(args, stdout, stderr)
	}
	var out bytes.Buffer
	d := &Driver{Run: run, Facts: f, Clock: &fakeClock{}, Out: &out, Config: Config{Every: time.Second, Ticks: 6}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	for tick := 1; tick <= 6; tick++ {
		want := tick % 2 // 1 if the machine is up this tick
		want = 1 - want
		if takes[tick] != want || beats[tick] != want {
			t.Errorf("tick %d: %d takes and %d beats, want %d of each\n%s", tick, takes[tick], beats[tick], want, out.String())
		}
	}
	text := out.String()
	if strings.Count(text, "(m1 falls silent: its machine stops beating)") != 3 || strings.Count(text, "(m1 beats again)") != 3 {
		t.Errorf("the machine's downs and returns:\n%s", text)
	}
}

// A machine held down by the coordinator's hold takes no work either, and
// takes work again when it is released.
func TestAMachineHeldDownTakesNoWorkAndTakesWorkAgainWhenReleased(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy}, queue: map[string]string{
		"m1":       `{"cards":[{"id":"s1-4.w1","col":"ready","gen":2}]}`,
		"reader-a": `{"cards":[]}`,
		"s1":       `{"cards":[]}`,
	}, inbox: `{"groups":[]}`}
	f := &ticked{Seeded: NewSeeded(1)}
	f.Use(Chances{Down: 1, Up: 1}, time.Second)
	takes := map[int]int{}
	run := func(args []string, stdout, stderr io.Writer) int {
		if args[0] == "take" {
			takes[f.tick]++
		}
		if args[0] == "where" {
			// the table as the holds of the ticks before left it: the member is
			// held after an odd tick, and up (released) after an even one
			status := "up"
			if f.tick%2 == 1 {
				status = "held"
			}
			fmt.Fprintln(stdout, strings.Replace(busy, `"status":"up"`, `"status":"`+status+`"`, 1))
			return 0
		}
		return w.run(args, stdout, stderr)
	}
	d := &Driver{Run: run, Facts: f, Clock: &fakeClock{}, Out: io.Discard, Config: Config{Every: time.Second, Ticks: 4, Hold: true}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	for tick := 1; tick <= 4; tick++ {
		if want := 1 - tick%2; takes[tick] != want {
			t.Errorf("tick %d: %d takes, want %d", tick, takes[tick], want)
		}
	}
	if index(w.ran, 0, "fleet down m1") < 0 || index(w.ran, 0, "fleet up m1") < 0 {
		t.Errorf("no hold and release: %v", w.ran)
	}
}

// eventful is a world with members, readers and streams whose queues never
// empty, so that every tick draws every kind of fact.
func eventful() *world {
	const cards = `{"cards":[{"id":"s1-1.w1","col":"working","gen":1},{"id":"s1-2.w1","col":"working","gen":1},{"id":"s1-3.w1","col":"ready","gen":1}]}`
	where := strings.Replace(twoStreams, `"fleet":{"m1":{"status":"up"}},"readers":{}`,
		`"fleet":{"m1":{"status":"up"},"m2":{"status":"up"},"m3":{"status":"up"}},"readers":{"reader-a":{}}`, 1)
	return &world{where: []string{where},
		queue: map[string]string{
			"m1": cards, "m2": cards, "m3": cards,
			"reader-a": `{"cards":[{"id":"s1-1.r1.reader-a","col":"asked"},{"id":"s1-2.r1.reader-a","col":"reading"},{"id":"s1-3.r1.reader-a","col":"reading"}]}`,
			"s1":       `{"cards":[{"id":"s1-5","col":"queued"},{"id":"s1-6","col":"queued"}]}`,
			"s2":       `{"cards":[{"id":"s2-5","col":"queued"},{"id":"s2-6","col":"queued"}]}`,
		}, inbox: `{"groups":[]}`}
}

// play is the simulation for ticks ticks against the eventful world, as the
// lines it ran and what it printed.
func play(t *testing.T, seed uint64, ticks int) (ran, out string) {
	t.Helper()
	w := eventful()
	var text bytes.Buffer
	f := NewSeeded(seed)
	c, err := Set(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.Use(c, time.Second)
	d := &Driver{Run: w.run, Facts: f, Clock: &fakeClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}, Out: &text, Config: Config{Every: time.Second, Ticks: ticks}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	var lines []string
	for _, a := range w.ran {
		lines = append(lines, strings.Join(a, " "))
	}
	return strings.Join(lines, "\n"), text.String()
}

// The same seed plays the same events, verb for verb, and another seed plays
// others.
func TestTheSameSeedPlaysTheSameEventsAndAnotherSeedOthers(t *testing.T) {
	t.Parallel()
	ranA, outA := play(t, 11, 300)
	ranB, outB := play(t, 11, 300)
	if ranA != ranB || outA != outB {
		t.Fatalf("the same seed played two runs")
	}
	ranC, _ := play(t, 12, 300)
	if ranA == ranC {
		t.Fatalf("another seed played the same run")
	}
	for _, want := range []string{"finish --as", "--failed", "read --as reader-a --broken", "--conflict", "--cross", "(m1 falls silent", "(m1 beats again)"} {
		if !strings.Contains(ranA+outA, want) {
			t.Errorf("300 ticks of the simulation never played %q", want)
		}
	}
	for _, l := range strings.Split(ranA, "\n") {
		if coordinatorVerbs[strings.Fields(l)[0]] {
			t.Fatalf("the simulation ran the coordinator's verb: %s", l)
		}
	}
}

// The driver has no way to write a table but the verbs it is given: it
// imports nothing outside the standard library, so it holds no store and no
// core to write with.
func TestTheDriverImportsOnlyTheStandardLibrary(t *testing.T) {
	t.Parallel()
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		seen++
		parsed, err := parser.ParseFile(token.NewFileSet(), f.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range parsed.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				t.Errorf("%s imports %s: the driver reaches the tables only through verbs", f.Name(), path)
			}
		}
	}
	if seen < 3 {
		t.Fatalf("read %d files of the driver", seen)
	}
}
