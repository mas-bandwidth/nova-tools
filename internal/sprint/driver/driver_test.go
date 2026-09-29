package driver

import (
	"bytes"
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

const busy = `{"landed":0,"all":2,"summary":"0/2 0.0% -> ETA","tables":{"fleet":{"m1":{"status":"up"}},"readers":{"reader-a":{}},` +
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
	d := &Driver{Run: w.run, Base: []string{"--prefix", "dev-"}, Facts: facts, Clock: &fakeClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)},
		Out: &out, Config: Config{Every: time.Second, Start: true, Batch: 5}}
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
	for _, want := range []string{"finish --as m1 s1-1.w2@3 --prefix dev-", "take --as m1 s1-4.w1@2 --prefix dev-", "read --as reader-a --ok s1-2.r1.reader-a",
		"merge --stream s1 --batch 5", "start --limit 1000", "resolve", "ask"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	text := out.String()
	for _, want := range []string{"tick 1 03:04:05", "nova-sprint take --as m1 s1-4.w1@2 --prefix dev-", "TAKE OK moved=1 refused=0",
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
		s.Fail, s.Broken, s.Stuck, s.Cross, s.Red, s.Flap = 0.3, 0.3, 0.2, 0.1, 0.1, 0.5
	}
	for i := 0; i < 50; i++ {
		id := fmt.Sprint("c", i)
		ao, ar := a.Work(id)
		bo, br := b.Work(id)
		am := a.Merge("s1", []string{"x", "y", "z"}, []string{"o1", "o2"})
		bm := b.Merge("s1", []string{"x", "y", "z"}, []string{"o1", "o2"})
		au := a.Up(i, []string{"m1", "m2"}, map[string]bool{"m1": true})
		bu := b.Up(i, []string{"m1", "m2"}, map[string]bool{"m1": true})
		if ao != bo || ar != br || am != bm || fmt.Sprint(au) != fmt.Sprint(bu) {
			t.Fatalf("draw %d differs", i)
		}
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
