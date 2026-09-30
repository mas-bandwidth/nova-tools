package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time        { return c.now }
func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

// world is a scripted entry point: the read verbs answer from a script, the
// write verbs are recorded.
type world struct {
	where  []string // the where --json answers, in turn
	queue  map[string]string
	inbox  string
	ran    [][]string
	wheres int
}

func (w *world) run(args []string, stdout, stderr io.Writer) int {
	w.ran = append(w.ran, args)
	json := strings.Contains(strings.Join(args, " "), "--json")
	switch {
	case args[0] == "where" && json:
		i := min(w.wheres, len(w.where)-1)
		w.wheres++
		fmt.Fprintln(stdout, w.where[i])
	case args[0] == "queue" && json:
		fmt.Fprintln(stdout, w.queue[args[2]])
	case args[0] == "inbox" && json:
		fmt.Fprintln(stdout, w.inbox)
	default:
		fmt.Fprintf(stdout, "%s OK moved=1 refused=0 notes=0\n", strings.ToUpper(args[0]))
	}
	return 0
}

const busy = `{"landed":0,"all":2,"summary":"0/2 0.0% -> ETA","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"}},"readers":{"reader-a":{}},` +
	`"merge":{"s1":{"state":"merging","queued":"1"}},"work":{"s1":{"merging":"1","working":"1"}}},"streams":[{"Stream":"s1","State":"merging"}]}`
const done = `{"landed":2,"all":2,"summary":"2/2 100.0% -> ETA","tables":{},"streams":[{"Stream":"s1","State":"landed"}]}`

func TestTheLoopPlaysTheWorldThroughVerbsOnly(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy, busy, done}, queue: map[string]string{
		"m1":       `{"cards":[{"id":"s1-1.w2","col":"working","gen":3},{"id":"s1-4.w1","col":"ready","gen":2}]}`,
		"reader-a": `{"cards":[{"id":"s1-2.r1.reader-a","col":"asked"}]}`,
		"s1":       `{"cards":[{"id":"s1-3","col":"queued"}]}`,
	}, inbox: `{"groups":[{"kind":"judgment","type":"work came back failed","stream":"s1","count":2,"oldest":"2030-01-02T03:00:00Z"}]}`}
	var out bytes.Buffer
	facts := NewSeeded(7)
	d := &Driver{Run: w.run, Base: []string{"--redis", "127.0.0.1:1"}, Facts: facts, Clock: &fakeClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)},
		Out: &out, Config: Config{Every: time.Second, Batch: 5}}
	why, err := d.Loop()
	if err != nil || why != "landed" {
		t.Fatalf("loop: %s %v\n%s", why, err, out.String())
	}
	var lines []string
	for _, a := range w.ran {
		if coordinatorVerbs[a[0]] {
			t.Fatalf("the driver ran the coordinator's verb: %v", a)
		}
		lines = append(lines, strings.Join(a, " "))
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"finish --as m1 --epoch 0 s1-1.w2@3 --redis 127.0.0.1:1", "take --as m1 --epoch 0 s1-4.w1@2 --redis 127.0.0.1:1", "read --as reader-a --ok --epoch 0 s1-2.r1.reader-a",
		"merge --stream s1 --batch 5"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	for _, not := range []string{"start", "resolve", "ask ", "tick", "fleet level"} {
		for _, l := range lines {
			if strings.HasPrefix(l, not) {
				t.Errorf("the driver ran the machine's move %q", l)
			}
		}
	}
	text := out.String()
	for _, want := range []string{"tick 1 03:04:05", "nova-sprint take --as m1 --epoch 0 s1-4.w1@2 --redis 127.0.0.1:1", "TAKE OK moved=1 refused=0",
		"waits for the coordinator: work came back failed s1 x2 4m5s", "every stream has landed: 2/2 100.0% -> ETA"} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
}

func TestTheSeedPlaysTheSameFacts(t *testing.T) {
	t.Parallel()
	a, b := NewSeeded(42), NewSeeded(42)
	for _, s := range []*Seeded{a, b} {
		s.Fail, s.Broken, s.Stuck, s.Cross, s.Red, s.Down, s.Back = 0.3, 0.3, 0.2, 0.1, 0.1, 0.5, 0.5
	}
	for i := 0; i < 50; i++ {
		id := fmt.Sprint("c", i)
		ao, ar := a.Work(id)
		bo, br := b.Work(id)
		others := func() []string { return []string{"o1", "o2"} }
		am := a.Merge("s1", []string{"x", "y", "z"}, others)
		bm := b.Merge("s1", []string{"x", "y", "z"}, others)
		au := a.Up(i, []string{"m1", "m2"}, map[string]bool{"m1": true})
		bu := b.Up(i, []string{"m1", "m2"}, map[string]bool{"m1": true})
		if ao != bo || ar != br || fmt.Sprint(am) != fmt.Sprint(bm) || fmt.Sprint(au) != fmt.Sprint(bu) {
			t.Fatalf("draw %d differs", i)
		}
	}
}

func TestTheDriverRefusesToPlayWithNoMachineRunning(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{strings.Replace(busy, "machine: running", "machine: STOPPED", 1)}}
	var out bytes.Buffer
	d := &Driver{Run: w.run, Facts: NewSeeded(1), Clock: &fakeClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}, Out: &out, Header: "chances: a header"}
	if _, err := d.Loop(); err == nil || !strings.Contains(err.Error(), "no machine is running (machine: STOPPED)") {
		t.Fatalf("a driver with the machine stopped: %v", err)
	}
	if len(w.ran) != 1 {
		t.Fatalf("it ran more than the read: %v", w.ran)
	}
	if out.Len() != 0 {
		t.Fatalf("a driver that refuses to play printed %q", out.String())
	}
}

// The header is printed once, when the loop may play, before its first tick.
func TestTheLoopPrintsItsHeaderOnceBeforeTheFirstTick(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy}, queue: map[string]string{
		"m1":       `{"cards":[]}`,
		"reader-a": `{"cards":[]}`,
		"s1":       `{"cards":[]}`,
	}, inbox: `{"groups":[]}`}
	var out bytes.Buffer
	d := &Driver{Run: w.run, Facts: NewSeeded(1), Clock: &fakeClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}, Out: &out,
		Header: "chances: a header", Config: Config{Every: time.Second, Ticks: 3}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	if text := out.String(); !strings.HasPrefix(text, "chances: a header\ntick 1 ") || strings.Count(text, "chances: a header") != 1 {
		t.Fatalf("the header, once, before tick 1:\n%s", text)
	}
}

func TestTheDriverRefusesToRunACoordinatorVerb(t *testing.T) {
	t.Parallel()
	d := &Driver{Run: (&world{}).run, Out: io.Discard}
	defer func() {
		if recover() == nil {
			t.Fatalf("the driver ran accept")
		}
	}()
	d.run(false, "accept", "--read-ok")
}

func TestCommandLinesPasteAsTyped(t *testing.T) {
	t.Parallel()
	got := commandLine([]string{"finish", "--as", "m1", "--report", "the tests went red", "a'b"})
	want := `nova-sprint finish --as m1 --report 'the tests went red' 'a'\''b'`
	if got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}
}

// scripted is facts by script: every work and read ok, a cross fact for the
// streams named, members as the script says.
type scripted struct {
	cross map[string]bool
	down  map[string]bool
	asked []string // the streams whose facts read the other queues
}

func (f *scripted) Work(string) (bool, string) { return true, "" }
func (f *scripted) Read(string) (bool, string) { return true, "" }
func (f *scripted) Merge(stream string, batch []string, others func() []string) Outcome {
	if !f.cross[stream] {
		return Outcome{}
	}
	f.asked = append(f.asked, stream)
	if o := others(); len(o) > 0 {
		return Outcome{Cross: batch[0] + "=" + o[0]}
	}
	return Outcome{}
}
func (f *scripted) Up(tick int, members []string, up map[string]bool) map[string]bool {
	next := map[string]bool{}
	for _, m := range members {
		next[m] = !f.down[m]
	}
	return next
}

const twoStreams = `{"landed":0,"all":4,"summary":"0/4 0.0% -> ETA","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"}},"readers":{},` +
	`"merge":{"s1":{"state":"merging","queued":"1"},"s2":{"state":"merging","queued":"1"}},"work":{"s1":{"merging":"1"},"s2":{"merging":"1"}}},` +
	`"streams":[{"Stream":"s1","State":"merging"},{"Stream":"s2","State":"merging"}]}`

func index(ran [][]string, from int, want string) int {
	for i := from; i < len(ran); i++ {
		if strings.HasPrefix(strings.Join(ran[i], " "), want) {
			return i
		}
	}
	return -1
}

// I6: the other streams' queues are read just before the merge step whose
// fact needs them, after every step before it, and not otherwise.
func TestTheOtherQueuesAreReadWhenAFactNeedsThem(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{twoStreams}, queue: map[string]string{
		"m1": `{"cards":[]}`, "s1": `{"cards":[{"id":"s1-1","col":"queued"}]}`, "s2": `{"cards":[{"id":"s2-1","col":"queued"}]}`,
	}, inbox: `{"groups":[]}`}
	f := &scripted{cross: map[string]bool{"s2": true}}
	d := &Driver{Run: w.run, Facts: f, Clock: &fakeClock{}, Out: io.Discard, Config: Config{Every: time.Second, Batch: 5, Ticks: 1}}
	if _, err := d.Loop(); err != nil {
		t.Fatal(err)
	}
	m1 := index(w.ran, 0, "merge --stream s1")
	m2 := index(w.ran, 0, "merge --stream s2 --batch 5 --cross s2-1=s1-1")
	read := index(w.ran, m1, "queue --stream s1")
	if m1 < 0 || m2 < 0 || read < 0 || read > m2 {
		t.Fatalf("s1's queue is not read between s1's step and s2's: merge s1 at %d, read at %d, merge s2 at %d\n%v", m1, read, m2, w.ran)
	}
	if index(w.ran, 0, "queue --stream s2") > m2 || strings.Join(f.asked, ",") != "s2" {
		t.Fatalf("a queue read with no fact needing it: %v %v", f.asked, w.ran)
	}
	if first := index(w.ran, 0, "queue --stream s2"); first < m1 {
		t.Fatalf("s2's queue read before s1's step: %v", w.ran)
	}
}

// I6: a member the driver took down is brought up before it stops.
func TestAMemberTakenDownIsBroughtUpBeforeTheDriverStops(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy}, queue: map[string]string{"m1": `{"cards":[]}`, "reader-a": `{"cards":[]}`, "s1": `{"cards":[]}`}, inbox: `{"groups":[]}`}
	d := &Driver{Run: w.run, Facts: &scripted{down: map[string]bool{"m1": true}}, Clock: &fakeClock{}, Out: io.Discard, Config: Config{Every: time.Second, Ticks: 2, Hold: true}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	down := index(w.ran, 0, "fleet down m1")
	up := index(w.ran, down, "fleet up m1")
	if down < 0 || up < 0 || up != len(w.ran)-1 {
		t.Fatalf("m1 left down: %v", w.ran)
	}
}

// I6: flap brings a down member up with the chance it takes an up one down.
func TestFlapIsTheSameChanceBothWays(t *testing.T) {
	t.Parallel()
	s := NewSeeded(9)
	c, err := Set(false, map[string]float64{"flap": 0.2})
	if err != nil {
		t.Fatal(err)
	}
	s.Use(c, time.Second)
	downs, ups := 0, 0
	for i := 0; i < 20000; i++ {
		n := s.Up(i, []string{"a", "b"}, map[string]bool{"a": true})
		if !n["a"] {
			downs++
		}
		if n["b"] {
			ups++
		}
	}
	if d, u := float64(downs)/20000, float64(ups)/20000; d < 0.18 || d > 0.22 || u < 0.18 || u > 0.22 {
		t.Fatalf("down %.3f up %.3f", d, u)
	}
}

// On red the seeded facts name one suspect of the batch, and the driver passes
// it to the merge step.
func TestRedNamesASuspectOfTheBatch(t *testing.T) {
	t.Parallel()
	s := NewSeeded(3)
	s.Red = 1
	out := s.Merge("s1", []string{"a", "b", "c"}, func() []string { return nil })
	if !out.Red || len(out.Suspects) != 1 || !strings.Contains("abc", out.Suspects[0]) {
		t.Fatalf("red: %+v", out)
	}
}

// Every primary on the table landed is landed, whatever streams have no
// primaries (a new epoch keeps every stream, empty).
func TestLandedIgnoresAStreamWithNoPrimaries(t *testing.T) {
	t.Parallel()
	var w where
	if err := json.Unmarshal([]byte(`{"landed":4,"all":4,"streams":[{"Stream":"s1","State":"landed"},{"Stream":"s3","State":"waiting"}]}`), &w); err != nil {
		t.Fatal(err)
	}
	if !landed(w) {
		t.Fatal("a sprint whose every primary landed is not landed")
	}
	if landed(where{Landed: 3, All: 4}) || landed(where{}) {
		t.Fatal("landed with a primary to go, or with none")
	}
}

// writes is every verb the driver ran that writes, as typed.
func writes(ran [][]string) []string {
	var out []string
	for _, a := range ran {
		if a[0] == "where" || a[0] == "queue" || a[0] == "inbox" || len(a) > 1 && a[0] == "fleet" && a[1] == "beat" {
			continue // reads, and beats: a machine's record, the sprint's, never an epoch's
		}
		out = append(out, strings.Join(a, " "))
	}
	return out
}

// C4: every verb the driver writes with holds the epoch it read at its start;
// when the sprint's epoch differs from it, the driver stops without writing
// and brings up no member it took down.
func TestTheDriverStopsAtAClearWithoutWriting(t *testing.T) {
	t.Parallel()
	cleared := strings.Replace(busy, `{"landed"`, `{"epoch":1,"landed"`, 1)
	w := &world{where: []string{busy, busy, busy, cleared}, queue: map[string]string{
		"m1": `{"cards":[{"id":"s1-4.w1","col":"ready","gen":2}]}`, "reader-a": `{"cards":[]}`, "s1": `{"cards":[{"id":"s1-3","col":"queued"}]}`,
	}, inbox: `{"groups":[]}`}
	var out bytes.Buffer
	d := &Driver{Run: w.run, Facts: &scripted{down: map[string]bool{"m1": true}}, Clock: &fakeClock{}, Out: &out, Config: Config{Every: time.Second}}
	why, err := d.Loop()
	if err != nil || why != "cleared" || !strings.Contains(out.String(), "holds epoch 0, which the sprint has left") {
		t.Fatalf("%s %v\n%s", why, err, out.String())
	}
	ws := writes(w.ran)
	if len(ws) == 0 {
		t.Fatalf("the first tick wrote nothing: %v", w.ran)
	}
	for _, l := range ws {
		if !strings.Contains(l, "--epoch 0") {
			t.Errorf("a write without the driver's epoch: %s", l)
		}
		if strings.HasPrefix(l, "fleet up") {
			t.Errorf("the driver brought up a member after the clear: %s", l)
		}
	}
	if w.wheres != 4 || strings.Join(w.ran[len(w.ran)-1], " ") != "where --json" {
		t.Fatalf("the driver ran on after it saw the clear: %v", w.ran)
	}
}

// C4: a verb refused because the sprint was cleared stops the driver's pass
// there: nothing more is run, and no member is brought up.
func TestAVerbRefusedAsClearedStopsThePass(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy}, queue: map[string]string{
		"m1": `{"cards":[{"id":"s1-4.w1","col":"ready","gen":2}]}`, "reader-a": `{"cards":[{"id":"s1-2.r1.reader-a","col":"asked"}]}`, "s1": `{"cards":[]}`,
	}, inbox: `{"groups":[]}`}
	run := func(args []string, stdout, stderr io.Writer) int {
		if args[0] == "take" {
			w.ran = append(w.ran, args)
			fmt.Fprintln(stderr, "REFUSED epoch 0: the sprint was cleared at 2030-01-02T03:04:05Z: its epoch is now 1")
			return 1
		}
		return w.run(args, stdout, stderr)
	}
	d := &Driver{Run: run, Facts: &scripted{down: map[string]bool{"m1": false}}, Clock: &fakeClock{}, Out: io.Discard, Config: Config{Every: time.Second}}
	why, err := d.Loop()
	if err != nil || why != "cleared" {
		t.Fatalf("%s %v", why, err)
	}
	if last := strings.Join(w.ran[len(w.ran)-1], " "); !strings.HasPrefix(last, "take --as m1 --epoch 0") {
		t.Fatalf("the driver ran on after the refusal: %v", w.ran)
	}
}

// Without Hold, a member the facts take down falls silent: its machine stops
// beating, no fleet verb is run for it, and it beats again when it comes back.
func TestAMemberTakenDownFallsSilent(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy}, queue: map[string]string{"m1": `{"cards":[]}`, "reader-a": `{"cards":[]}`, "s1": `{"cards":[]}`}, inbox: `{"groups":[]}`}
	d := &Driver{Run: w.run, Facts: &scripted{down: map[string]bool{"m1": true}}, Clock: &fakeClock{}, Out: io.Discard, Config: Config{Every: time.Second, Ticks: 2}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	if index(w.ran, 0, "fleet down") >= 0 || index(w.ran, 0, "fleet up") >= 0 || index(w.ran, 0, "fleet beat m1") >= 0 {
		t.Fatalf("a silent member: %v", w.ran)
	}
}

// --silent: a member stops beating for a while, then beats again.
func TestASilenceStopsTheBeatsForAWhile(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy}, queue: map[string]string{"m1": `{"cards":[]}`, "reader-a": `{"cards":[]}`, "s1": `{"cards":[]}`}, inbox: `{"groups":[]}`}
	d := &Driver{Run: w.run, Facts: &scripted{}, Clock: &fakeClock{}, Out: io.Discard,
		Config: Config{Every: time.Second, Ticks: 5, Silent: []Silence{{Member: "m1", From: time.Second, For: 2 * time.Second}}}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	beats := 0
	for _, a := range w.ran {
		if strings.Join(a, " ") == "fleet beat m1 --load 0" {
			beats++
		}
	}
	if beats != 3 {
		t.Fatalf("m1 beat %d times in five ticks, two of them silent: %v", beats, w.ran)
	}
}

// A --silent window ends: the member beats again, with the seeded facts too
// (whose Up keeps a member as the driver gave it).
func TestASilenceEndsWithTheSeededFacts(t *testing.T) {
	t.Parallel()
	w := &world{where: []string{busy}, queue: map[string]string{"m1": `{"cards":[]}`, "reader-a": `{"cards":[]}`, "s1": `{"cards":[]}`}, inbox: `{"groups":[]}`}
	d := &Driver{Run: w.run, Facts: NewSeeded(1), Clock: &fakeClock{}, Out: io.Discard,
		Config: Config{Every: time.Second, Ticks: 6, Silent: []Silence{{Member: "m1", From: time.Second, For: 2 * time.Second}}}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	var beats []int
	tick := 0
	for _, a := range w.ran {
		if a[0] == "where" {
			tick++
		}
		if strings.Join(a, " ") == "fleet beat m1 --load 0" {
			beats = append(beats, tick)
		}
	}
	if len(beats) != 4 {
		t.Fatalf("m1 beat at ticks %v in six ticks, two of them silent: %v", beats, w.ran)
	}
}
