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
		"reader-a": `{"cards":[{"id":"s1-2.r1.reader-a","col":"asked"},{"id":"s1-5.r1.reader-a","col":"reading"}]}`,
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
	for _, want := range []string{"finish --as m1 --epoch 0 s1-1.w2@3 --redis 127.0.0.1:1", "take --as m1 --limit 64 --epoch 0 --redis 127.0.0.1:1",
		"read --as reader-a --ok --epoch 0 s1-5.r1.reader-a --redis", "read --as reader-a --begin --epoch 0 s1-2.r1.reader-a --redis",
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
	for _, want := range []string{"tick 1 03:04:05", "nova-sprint take --as m1 --limit 64 --epoch 0 --redis 127.0.0.1:1", "TAKE OK moved=1 refused=0",
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
	if last := strings.Join(w.ran[len(w.ran)-1], " "); !strings.HasPrefix(last, "take --as m1 --limit 64 --epoch 0") {
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

// A tick is one call a verb for each machine and reader (the owner's ruling
// of 2026-09-30): one take of the ready queue up to the member's width, one
// finish of what it took, the failed in a second, one read --begin and one
// report; a batch over three cards prints its count and first three, never
// a line per card.
func TestEveryMachineMovesItsCardsInOneBatchATick(t *testing.T) {
	t.Parallel()
	var working, ready, asked, reading []string
	for i := 1; i <= 40; i++ {
		working = append(working, fmt.Sprintf(`{"id":"s1-%d.w1","col":"working","gen":1}`, i))
		ready = append(ready, fmt.Sprintf(`{"id":"s1-%d.w1","col":"ready","gen":1}`, 100+i))
		asked = append(asked, fmt.Sprintf(`{"id":"s1-%d.r1.reader-a","col":"asked"}`, i))
		reading = append(reading, fmt.Sprintf(`{"id":"s1-%d.r1.reader-a","col":"reading"}`, 100+i))
	}
	mine := `{"cards":[` + strings.Join(append(ready, working...), ",") + `]}`
	where := strings.Replace(busy, `"fleet":{"m1":{"status":"up"}}`, `"fleet":{"m1":{"status":"up","width":"128"},"m2":{"status":"up"}}`, 1)
	w := &world{where: []string{where}, queue: map[string]string{
		"m1": mine, "m2": mine, "reader-a": `{"cards":[` + strings.Join(append(asked, reading...), ",") + `]}`, "s1": `{"cards":[]}`,
	}, inbox: `{"groups":[]}`}
	f := NewSeeded(3)
	f.Fail, f.Broken = 0.5, 0.5
	var out bytes.Buffer
	d := &Driver{Run: w.run, Facts: f, Clock: &fakeClock{}, Out: &out, Config: Config{Every: time.Second, Ticks: 1}}
	if why, err := d.Loop(); err != nil || why != "ticks" {
		t.Fatalf("%s %v", why, err)
	}
	calls := map[string]int{}
	finished := 0
	for _, a := range w.ran {
		l := strings.Join(a, " ")
		verb := strings.Join(a[:min(3, len(a))], " ")
		switch {
		case strings.HasPrefix(l, "finish ") && strings.Contains(l, " --failed "):
			verb += " --failed"
		case strings.HasPrefix(l, "take ") || strings.HasPrefix(l, "read ") || strings.HasPrefix(l, "finish "):
			if strings.HasPrefix(l, "take ") || strings.HasPrefix(l, "read ") {
				verb = strings.Join(a[:min(5, len(a))], " ")
			}
		default:
			continue
		}
		calls[verb]++
		if strings.HasPrefix(l, "finish --as m1,m2 ") {
			for _, x := range a {
				if strings.HasSuffix(x, "@1") {
					finished++
				}
			}
		}
	}
	// one finish of every member's cards (the failed in a second call), one
	// take for each width, one of each read: every member's row in one step
	for _, verb := range []string{"take --as m1 --limit 128", "take --as m2 --limit 64", "finish --as m1,m2", "finish --as m1,m2 --failed",
		"read --as reader-a --begin --epoch", "read --as reader-a --ok --epoch", "read --as reader-a --broken --finding"} {
		if calls[verb] != 1 {
			t.Errorf("%q ran %d times in a tick, want once", verb, calls[verb])
		}
	}
	if len(calls) != 7 {
		t.Errorf("the calls of a tick: %v", calls)
	}
	if finished != 80 {
		t.Errorf("m1 and m2 finished %d of their 80 working cards in the two calls", finished)
	}
	text := out.String()
	if !strings.Contains(text, "read --as reader-a --begin --epoch 0 [40 cards: s1-1.r1.reader-a s1-2.r1.reader-a s1-3.r1.reader-a ...]") {
		t.Errorf("a batch prints its count and first three:\n%s", text)
	}
	for _, l := range strings.Split(text, "\n") {
		n := 0
		for _, x := range strings.Fields(l) {
			if strings.HasSuffix(x, "@1") || strings.HasSuffix(x, ".reader-a") {
				n++
			}
		}
		if n > 3 {
			t.Errorf("a line names %d cards, more than three:\n%s", n, l)
		}
	}
}

// E20: The world driver's turn is aligned to the machine's tick (the world
// acts once per machine tick, every machine and reader in batches; today it
// runs on its own clock). While the machine tick has not advanced, the world
// waits without acting; when the machine tick advances, the world acts once.
func TestWorldTurnAlignedToMachineTick(t *testing.T) {
	t.Parallel()
	tick1 := `{"ticks":1,"landed":0,"all":2,"summary":"0/2 0.0% -> ETA","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"}},"readers":{"reader-a":{}},` +
		`"merge":{"s1":{"state":"merging","queued":"1"}},"work":{"s1":{"merging":"1","working":"1"}}},"streams":[{"Stream":"s1","State":"merging"}]}`
	tick2 := `{"ticks":2,"landed":0,"all":2,"summary":"0/2 0.0% -> ETA","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"}},"readers":{"reader-a":{}},` +
		`"merge":{"s1":{"state":"merging","queued":"1"}},"work":{"s1":{"merging":"1","working":"1"}}},"streams":[{"Stream":"s1","State":"merging"}]}`
	landed3 := `{"ticks":3,"landed":2,"all":2,"summary":"2/2 100.0% -> ETA","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"}},"readers":{"reader-a":{}},` +
		`"merge":{"s1":{"state":"landed"}},"work":{}},"streams":[{"Stream":"s1","State":"landed"}]}`

	w := &world{
		where: []string{
			tick1, tick1, tick1, tick1, tick1,
			tick2, tick2, tick2, tick2,
			landed3,
		},
		queue: map[string]string{
			"m1":       `{"cards":[{"id":"s1-1.w2","col":"working","gen":3},{"id":"s1-4.w1","col":"ready","gen":2}]}`,
			"reader-a": `{"cards":[{"id":"s1-2.r1.reader-a","col":"asked"},{"id":"s1-5.r1.reader-a","col":"reading"}]}`,
			"s1":       `{"cards":[{"id":"s1-3","col":"queued"}]}`,
		},
		inbox: `{"groups":[]}`,
	}
	var out bytes.Buffer
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	clk := &fakeClock{now: start}
	d := &Driver{
		Run:   w.run,
		Facts: NewSeeded(7),
		Clock: clk,
		Out:   &out,
		Config: Config{
			Every: 100 * time.Millisecond,
		},
	}
	why, err := d.Loop()
	if err != nil || why != "landed" {
		t.Fatalf("loop: %s %v\n%s", why, err, out.String())
	}

	takes, finishes, reads, merges := 0, 0, 0, 0
	for _, a := range w.ran {
		switch a[0] {
		case "take":
			takes++
		case "finish":
			finishes++
		case "read":
			reads++
		case "merge":
			merges++
		}
	}

	// Exactly 2 world turns were taken (one for machine tick 1, one for machine tick 2).
	// In each turn, operations run in batches:
	// - 1 finish call
	// - 1 take call
	// - 2 read calls (1 report, 1 begin)
	// - 1 merge call
	if takes != 2 {
		t.Errorf("takes: got %d, want 2 (one per machine tick)", takes)
	}
	if finishes != 2 {
		t.Errorf("finishes: got %d, want 2 (one per machine tick)", finishes)
	}
	if merges != 2 {
		t.Errorf("merges: got %d, want 2 (one per machine tick)", merges)
	}
	if reads != 4 {
		t.Errorf("reads: got %d, want 4 (two per machine tick)", reads)
	}

	// Verify clock advanced during waits (step = 50ms min(c.Every, 50ms))
	if !clk.now.After(start.Add(100 * time.Millisecond)) {
		t.Errorf("clock only advanced to %v from %v, expected at least 150ms of sleep during waits", clk.now, start)
	}

	text := out.String()
	if !strings.Contains(text, "tick 1 03:04:05") {
		t.Errorf("missing tick 1 in output:\n%s", text)
	}
	if !strings.Contains(text, "tick 2 ") {
		t.Errorf("missing tick 2 in output:\n%s", text)
	}
	if strings.Contains(text, "tick 3 ") {
		t.Errorf("tick 3 should not have run as world turn because landed occurred at tick 3:\n%s", text)
	}
	if !strings.Contains(text, "every stream has landed: 2/2 100.0% -> ETA") {
		t.Errorf("missing landed summary in output:\n%s", text)
	}
}

// When the machine ticks during the world's turn (e.g. run woke on the log
// lines written by finish/take), after.Ticks already shows the new tick, so
// the next turn begins immediately without sleeping.
func TestWorldTurnAlignedAdvancesImmediatelyWhenMachineAlreadyTicked(t *testing.T) {
	t.Parallel()
	tick1 := `{"ticks":1,"landed":0,"all":2,"summary":"0/2 0.0% -> ETA","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"}},"readers":{"reader-a":{}},` +
		`"merge":{"s1":{"state":"merging","queued":"1"}},"work":{"s1":{"merging":"1","working":"1"}}},"streams":[{"Stream":"s1","State":"merging"}]}`
	tick2 := `{"ticks":2,"landed":0,"all":2,"summary":"0/2 0.0% -> ETA","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"}},"readers":{"reader-a":{}},` +
		`"merge":{"s1":{"state":"merging","queued":"1"}},"work":{"s1":{"merging":"1","working":"1"}}},"streams":[{"Stream":"s1","State":"merging"}]}`
	landed3 := `{"ticks":3,"landed":2,"all":2,"summary":"2/2 100.0% -> ETA","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"}},"readers":{"reader-a":{}},` +
		`"merge":{"s1":{"state":"landed"}},"work":{}},"streams":[{"Stream":"s1","State":"landed"}]}`

	w := &world{
		where: []string{
			tick1,
			tick1,
			tick2,
			tick2,
			landed3,
		},
		queue: map[string]string{
			"m1":       `{"cards":[{"id":"s1-1.w2","col":"working","gen":3}]}`,
			"reader-a": `{"cards":[]}`,
			"s1":       `{"cards":[]}`,
		},
		inbox: `{"groups":[]}`,
	}
	var out bytes.Buffer
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	clk := &fakeClock{now: start}
	d := &Driver{
		Run:   w.run,
		Facts: NewSeeded(1),
		Clock: clk,
		Out:   &out,
		Config: Config{
			Every: time.Second,
		},
	}
	why, err := d.Loop()
	if err != nil || why != "landed" {
		t.Fatalf("loop: %s %v\n%s", why, err, out.String())
	}
	if !clk.now.Equal(start) {
		t.Errorf("clock advanced to %v, expected no sleep (%v)", clk.now, start)
	}
}

