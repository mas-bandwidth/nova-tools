//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/redis/go-redis/v9"
)

// The drive: the new command against a real store, as the owner drives the
// present build by hand (init, readers, an eight-member fleet, three streams
// of a hundred cards with a gate after every ten, cross-stream needs and a
// loop through the gates refused), then the machine ticking while a
// simulated world takes and finishes work (one in ten failed), reads (one in
// ten broken), merges, and the coordinator answers the inbox, until every
// card lands or the time runs out. What it asserts is the owner's: every
// card lands; the deal goes round the fleet (every member's placements,
// first attempts and redeals together on one rolling index, within 2 of each
// other) and the ask round the readers (the two readers of a pair alike, the
// pairs within the reworked attempts); the gates release in order; the
// machine stops itself at done and says so; where, the view and the inbox
// agree; the exit codes are 0, 2 and 3; no step of the machine is refused; a
// tick is one round trip idle (a tick after the stop, which writes nothing)
// and at most three busy (E8, on the connection); no judgment is left open;
// teardown leaves only the lifecycle receipts.

// driveStreams, driveMembers and driveReaders are the drive's sprint.
var (
	driveStreams = []string{"a", "b", "c"}
	driveMembers = []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	driveReaders = []string{"reader-a", "reader-b", "reader-c", "reader-d"}
)

// driveTrips counts the round trips of the command's sprint client on its
// connection: every command or pipeline the client sends and waits for,
// once; a connection's setup (sent with a context the hook has marked) is
// not counted, as testredis.RoundTrips defines it.
type driveTrips struct{ n atomic.Int64 }

type driveMark struct{}

func (d *driveTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (d *driveTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if ctx.Value(driveMark{}) != nil {
			return next(ctx, cmd)
		}
		d.n.Add(1)
		return next(context.WithValue(ctx, driveMark{}, true), cmd)
	}
}

func (d *driveTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if ctx.Value(driveMark{}) != nil || len(cmds) == 0 {
			return next(ctx, cmds)
		}
		d.n.Add(1)
		return next(context.WithValue(ctx, driveMark{}, true), cmds)
	}
}

// driveLine is a log line as the drive reads it.
type driveLine struct {
	Seq    string         `json:"seq"`
	Kind   string         `json:"kind"`
	Table  string         `json:"table"`
	From   string         `json:"from"`
	To     string         `json:"to"`
	IDs    []string       `json:"ids"`
	About  []string       `json:"about"`
	Shared map[string]any `json:"shared"`
	Meta   map[string]any `json:"meta"`
}

// driveWhere is what the drive reads of where --json.
type driveWhere struct {
	Landed  int64                                   `json:"landed"`
	All     int64                                   `json:"all"`
	Summary string                                  `json:"summary"`
	Tables  map[string]map[string]map[string]string `json:"tables"`
	Epoch   uint64                                  `json:"epoch"`
	Machine string                                  `json:"machine"`
}

type driveQueue struct {
	Cards []struct {
		ID  string `json:"id"`
		Col string `json:"col"`
		Gen int    `json:"gen"`
	} `json:"cards"`
}

type driveGroup struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Type      string   `json:"type"`
	Count     int      `json:"count"`
	Primaries []string `json:"primaries"`
	Notes     []string `json:"notes"`
	What      string   `json:"what"`
}

type driveInbox struct {
	Groups  []driveGroup `json:"groups"`
	Machine string       `json:"machine"`
}

// driveTick is what the drive reads of tick --json (IT17's Report).
type driveTick struct {
	RoundTrips int
	Running    bool
	Lines      int
	Keys       int
	Applied    int
	Refused    map[string]int
	Short      []string
	Dealt      []string
}

// drive is the world and what it counted.
type drive struct {
	t     *testing.T
	a     *app
	trips func() int64
	rng   *rand.Rand

	codes    map[int]int      // exit code -> verbs
	codeBy   map[string][]int // verb -> exit codes other than 0
	lastErr  map[string]string
	seq      uint64
	first    map[string]int      // member -> first attempts dealt
	redeal   map[string]int      // member -> later attempts dealt
	reads    map[string]int      // reader -> read cards asked
	askedOf  map[string][]string // primary's attempt (<p>.r<n>) -> the readers asked of it
	asked    map[string]map[string]bool
	gates    map[string][]string // stream -> gates in the order they landed
	released map[string][]string // stream -> gates in the order released
	judged   map[string]int      // judgment type -> notes seen
	happened map[string]int
	done     bool // "the sprint is done" seen in the log
	failed   int  // finish --failed
	broken   int  // read --broken
	answers  map[string]int
	tickLog  []driveTick
	tickRaw  []string       // the last ticks' reports, as printed
	texts    map[string]int // "type: text" of every judgment opened
	connIdle []int64        // connection trips of each idle tick
	connBusy []int64
	checks   int
}

func (d *drive) run(line string) (int, string, string) {
	d.t.Helper()
	var out, errb bytes.Buffer
	code := d.a.run(split(line), &out, &errb)
	d.codes[code]++
	verb := strings.Fields(line)[0]
	if f := strings.Fields(line); len(f) > 1 && newGroups[f[0]] {
		verb += " " + f[1]
	}
	if code != 0 {
		d.codeBy[verb] = append(d.codeBy[verb], code)
		d.lastErr[verb] = strings.TrimSpace(out.String() + errb.String())
	}
	return code, out.String(), errb.String()
}

func (d *drive) ok(line string) string {
	d.t.Helper()
	code, out, errs := d.run(line)
	if code != 0 {
		d.t.Fatalf("%s: exit %d\n%s%s", line, code, out, errs)
	}
	return out
}

func (d *drive) json(line string, v any) {
	d.t.Helper()
	out := d.ok(line + " --json")
	if err := json.Unmarshal([]byte(out), v); err != nil {
		d.t.Fatalf("%s --json: %v\n%s", line, err, out)
	}
}

// follow reads the log after the last seq read and counts what the drive
// asserts on: the deal by member and attempt, the asks by reader, the gates
// landing, the notes.
func (d *drive) follow() {
	d.t.Helper()
	for {
		var v struct {
			Lines     []json.RawMessage `json:"lines"`
			Next      uint64            `json:"next"`
			Exhausted bool              `json:"exhausted"`
		}
		d.json(fmt.Sprintf("log --since %d", d.seq), &v)
		for _, raw := range v.Lines {
			var l driveLine
			if err := json.Unmarshal(raw, &l); err != nil {
				d.t.Fatalf("a log line: %v\n%s", err, raw)
			}
			if n, err := strconv.ParseUint(l.Seq, 10, 64); err == nil && n > d.seq {
				d.seq = n
			}
			d.line(l)
		}
		if v.Exhausted || len(v.Lines) == 0 {
			return
		}
	}
}

func driveStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func (d *drive) line(l driveLine) {
	switch {
	case l.Kind == "create" && l.Table == sprint.Fleet && strings.HasSuffix(l.To, ":ready") && driveStr(l.Shared, "kind") == "work":
		m := strings.TrimSuffix(l.To, ":ready")
		if driveStr(l.Shared, "attempt") == "1" {
			d.first[m] += len(l.IDs)
		} else {
			d.redeal[m] += len(l.IDs)
		}
	case l.Kind == "create" && l.Table == sprint.Readers && strings.HasSuffix(l.To, ":asked"):
		r := strings.TrimSuffix(l.To, ":asked")
		d.reads[r] += len(l.IDs)
		for _, id := range l.IDs {
			if i := strings.LastIndex(id, "."); i > 0 {
				d.askedOf[id[:i]] = append(d.askedOf[id[:i]], r)
			}
		}
		if d.asked[r] == nil {
			d.asked[r] = map[string]bool{}
		}
		for _, id := range l.IDs {
			d.asked[r][id] = true
		}
	case l.Table == sprint.Readers && strings.HasSuffix(l.From, ":asked"):
		r := strings.TrimSuffix(l.From, ":asked")
		for _, id := range l.IDs {
			delete(d.asked[r], id)
		}
	case l.Kind == "move" && l.Table == sprint.Work && strings.HasSuffix(l.To, ":landed"):
		s := strings.TrimSuffix(l.To, ":landed")
		for _, id := range l.IDs {
			if strings.Contains(id, "-gate-") {
				d.gates[s] = append(d.gates[s], id)
			}
		}
	case l.Kind == "note":
		meta := func(k string) string { return driveStr(l.Meta, k) }
		switch meta("kind") {
		case sprint.Judgment:
			if meta("op") == "open" || meta("op") == "" {
				d.judged[meta("type")]++
				d.texts[meta("type")+": "+meta("text")]++
			}
		case sprint.Happened:
			d.happened[meta("type")]++
		}
		if meta("type") == sprint.NSprintDone {
			d.done = true
		}
	}
}

// tick runs one tick of the machine and counts its round trips, the tick's
// own count and the connection's.
func (d *drive) tick() driveTick {
	d.t.Helper()
	before := d.trips()
	var r driveTick
	out := d.ok("tick --json")
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		d.t.Fatalf("tick --json: %v\n%s", err, out)
	}
	if d.tickRaw = append(d.tickRaw, strings.TrimSpace(out)); len(d.tickRaw) > 3 {
		d.tickRaw = d.tickRaw[1:]
	}
	conn := d.trips() - before
	if int64(r.RoundTrips) != conn {
		d.t.Errorf("a tick counted %d round trips and its connection %d", r.RoundTrips, conn)
	}
	if r.Lines == 0 && r.Applied == 0 {
		d.connIdle = append(d.connIdle, conn)
	} else {
		d.connBusy = append(d.connBusy, conn)
	}
	d.tickLog = append(d.tickLog, r)
	return r
}

// world plays the outside actors for one tick: every member beats, finishes
// what it took (one in ten failed) and takes what is ready; every reader
// reads what it was asked (one in ten broken); each stream with cards queued
// merges them.
func (d *drive) world(w driveWhere) {
	d.t.Helper()
	d.ok("fleet beat " + strings.Join(driveMembers, " ") + " --load 0")
	for _, m := range driveMembers {
		var q driveQueue
		d.json("queue --as "+m, &q)
		var good, bad, ready []string
		for _, c := range q.Cards {
			word := c.ID + "@" + strconv.Itoa(c.Gen)
			switch c.Col {
			case "working":
				if d.rng.Float64() < 0.1 {
					bad = append(bad, word)
				} else {
					good = append(good, word)
				}
			case "ready":
				ready = append(ready, word)
			}
		}
		if len(good) > 0 {
			d.run("finish --as " + m + " " + strings.Join(good, " "))
		}
		if len(bad) > 0 {
			d.failed += len(bad)
			d.run("finish --as " + m + " --failed --report red " + strings.Join(bad, " "))
		}
		if len(ready) > 0 {
			d.run("take --as " + m + " " + strings.Join(ready, " "))
		}
	}
	for _, r := range driveReaders {
		var ids []string
		for id := range d.asked[r] {
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			continue
		}
		sort.Strings(ids)
		d.run("read --as " + r + " --begin " + strings.Join(ids, " "))
		var good, bad []string
		for _, id := range ids {
			if d.rng.Float64() < 0.1 {
				bad = append(bad, id)
			} else {
				good = append(good, id)
			}
		}
		if len(good) > 0 {
			d.run("read --as " + r + " --ok " + strings.Join(good, " "))
		}
		if len(bad) > 0 {
			d.broken += len(bad)
			d.run("read --as " + r + " --broken --finding a-hole " + strings.Join(bad, " "))
		}
	}
	for _, s := range driveStreams {
		if n, _ := strconv.Atoi(w.Tables["merge"][s]["queued"]); n > 0 {
			d.run("merge --stream " + s + " --batch 100")
		}
	}
}

// coordinator answers the inbox: releases the gates reached, reworks what is
// held on a bound or on its reads, resumes stopped streams, acks the
// machine's refused steps, and waits on the rest. It returns the judgment
// groups it found.
func (d *drive) coordinator() []driveGroup {
	d.t.Helper()
	var in driveInbox
	d.json("inbox", &in)
	var open []driveGroup
	for _, g := range in.Groups {
		if g.Kind != sprint.Judgment {
			continue
		}
		open = append(open, g)
		d.answers[g.Type]++
		switch {
		case g.Type == "sentinel reached":
			for _, p := range g.Primaries {
				s, _, _ := strings.Cut(p, "-gate-")
				if code, _, _ := d.run("release " + p + " --reason looked"); code == 0 {
					d.released[s] = append(d.released[s], p)
				}
			}
		case g.Type == "a card reached its bound" || g.Type == "reads exhausted" || g.Type == "stranded in review" || g.Type == "returned to review":
			d.run("rework " + strings.Join(g.Primaries, " ") + " --fix again")
		case strings.HasPrefix(g.Type, "stream stopped"):
			d.run("resume --stream " + strings.Join(g.Primaries, ",") + " --did fixed")
		case g.Type == "the machine's step was refused":
			d.run("ack " + strings.Join(g.Notes, " ") + " --reason seen")
		case len(g.Notes) > 0:
			d.run("wait " + strings.Join(g.Notes, " ") + " --for 1m --reason later")
		}
	}
	return open
}

// agree reads where (text and --json) and the inbox back to back and checks
// they agree: the view's landed and all are its work table's sums, the
// line's summary is the view's, and the inbox's machine line is the view's.
func (d *drive) agree(stage string) driveWhere {
	d.t.Helper()
	d.checks++
	var w driveWhere
	d.json("where", &w)
	text := d.ok("where")
	var in driveInbox
	d.json("inbox", &in)
	var landed, all int64
	for _, row := range w.Tables["work"] {
		for col, v := range row {
			n, _ := strconv.ParseInt(v, 10, 64)
			all += n
			if col == "landed" {
				landed += n
			}
		}
	}
	if landed != w.Landed || all != w.All {
		d.t.Errorf("%s: where --json says %d/%d, its work table %d/%d", stage, w.Landed, w.All, landed, all)
	}
	if got, want := driveFrameWork(text), driveViewWork(w); got != want {
		d.t.Errorf("%s: where's work totals %q, the view's %q:\n%s", stage, got, want, text)
	}
	if driveWord(in.Machine) != driveWord(w.Machine) {
		d.t.Errorf("%s: the inbox's machine %q, the view's %q", stage, in.Machine, w.Machine)
	}
	return w
}

// driveFrameWork is the totals row of where's work table, as its numbers.
func driveFrameWork(text string) string {
	in := false
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(strings.ReplaceAll(l, "|", " "))
		switch {
		case len(f) > 0 && f[0] == "work":
			in = true
		case in && strings.HasPrefix(strings.TrimSpace(l), "|"):
			return strings.Join(f, " ")
		}
	}
	return ""
}

// driveViewWork is the view's work table summed by column, in the frame's
// order.
func driveViewWork(w driveWhere) string {
	var out []string
	for _, col := range []string{"waiting", "ready", "working", "review", "merging", "landed"} {
		n := 0
		for _, row := range w.Tables["work"] {
			v, _ := strconv.Atoi(row[col])
			n += v
		}
		out = append(out, strconv.Itoa(n))
	}
	return strings.Join(out, " ")
}

func driveWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return strings.ToUpper(strings.TrimSuffix(f[0], ";"))
}

func driveSpread(m map[string]int, keys []string) (lo, hi int) {
	lo = -1
	for _, k := range keys {
		v := m[k]
		if lo < 0 || v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}

func driveCounts(m map[string]int, keys []string) string {
	var out []string
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(out, " ")
}

// TestNewPathDrive: the drive, on a real store holding this build's sprint
// library.
func TestNewPathDrive(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	raw := redis.NewClient(&redis.Options{Addr: addr})
	defer raw.Close()
	ctx := context.Background()
	if err := fn.LoadTSet(ctx, raw, fn.TSetSprint); err != nil {
		t.Fatalf("FUNCTION LOAD of the sprint profile: %v", err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	trips := &driveTrips{}
	a.sprintClient = func(ctx context.Context, addr string, names sprint.Names) (sprintfn.Client, func() error, error) {
		c, closer, err := a.newPathClient(ctx, addr, names)
		if err != nil {
			return c, closer, err
		}
		r, ok := c.(*sprintfn.Redis)
		if !ok {
			t.Fatalf("the new path's client is %T, not sprintfn.Redis", c)
		}
		r.AddHookForTest(trips)
		return c, closer, nil
	}
	driveOn(t, a, "coordinator", trips.n.Load, func() []string {
		keys, err := raw.Keys(ctx, layerNamespace+"*").Result()
		if err != nil {
			t.Fatal(err)
		}
		return keys
	})
}

// driveOn is the drive through the app's entry point; keys lists the
// namespace's keys after teardown.
func driveOn(t *testing.T, a *app, coord string, trips func() int64, keys func() []string) {
	t.Helper()
	start := time.Now()
	d := &drive{t: t, a: a, trips: trips, rng: rand.New(rand.NewPCG(1, 2)), codes: map[int]int{}, codeBy: map[string][]int{}, lastErr: map[string]string{},
		first: map[string]int{}, redeal: map[string]int{}, reads: map[string]int{}, askedOf: map[string][]string{}, asked: map[string]map[string]bool{},
		gates: map[string][]string{}, released: map[string][]string{}, judged: map[string]int{}, texts: map[string]int{}, happened: map[string]int{}, answers: map[string]int{}}

	// The owner's init line: --readers and --members are refused naming the
	// verbs that do them now (the grammar decisions, 9 to 12).
	for _, c := range []struct{ line, want string }{
		{"init --coordinator " + coord + " --readers " + strings.Join(driveReaders, ","), "reader add"},
		{"init --coordinator " + coord + " --members " + strings.Join(driveMembers, ","), "fleet up"},
	} {
		if code, out, errs := d.run(c.line); code != exitRefused || !strings.Contains(out+errs, c.want) {
			t.Fatalf("%s: exit %d, want 2 naming %s\n%s%s", c.line, code, c.want, out, errs)
		}
	}
	d.ok("init --coordinator " + coord)
	d.ok("reader add " + strings.Join(driveReaders, " "))
	d.ok("fleet up " + strings.Join(driveMembers, " "))
	d.ok("fleet beat " + strings.Join(driveMembers, " "))
	for _, s := range driveStreams {
		for k := 1; k <= 10; k++ {
			d.ok(fmt.Sprintf("add --stream %s --count 10", s))
			d.ok(fmt.Sprintf("add --stream %s --sentinel %s-gate-%d", s, s, k))
		}
	}
	// Two cross-stream needs, each placed in line; then one that would close
	// a loop through the gates: c-dep-1 before c-gate-2 needing a-dep-1,
	// which needs c-25, which waits on c-gate-2, which needs c-dep-1.
	deps := 0
	for _, line := range []string{"add --stream a a-dep-1 --needs c-25 --after a-40", "add --stream b b-dep-1 --needs a-55 --after b-30"} {
		if code, out, errs := d.run(line); code != 0 {
			t.Errorf("%s: exit %d\n%s%s", line, code, out, errs)
		} else {
			deps++
		}
	}
	if deps == 2 {
		if code, out, errs := d.run("add --stream c c-dep-1 --needs a-dep-1 --after c-5"); code != exitRefused ||
			!strings.Contains(out+errs, "a-dep-1") || !strings.Contains(out+errs, "c-gate-") {
			t.Errorf("the add that closes a loop through the gates: exit %d, want 2 naming the loop\n%s%s", code, out, errs)
			if code == 0 {
				// Admitted, the loop would hold stream c at c-gate-1 for
				// ever: the drive drops it and goes on.
				d.ok("drop c-dep-1 --reason loop")
			}
		} else {
			t.Logf("the loop refused: %s", strings.TrimSpace(out+errs))
		}
	} else {
		t.Errorf("the loop through the gates was not tried: its cross-stream needs were not admitted")
	}
	w := d.agree("added")
	want := int64(3*(100+10) + deps)
	if w.All != want {
		t.Errorf("added: where says %d cards, want %d", w.All, want)
	}
	// accept --read-ok is on the new path (the 4806 read, M5): nothing is in
	// review yet, so it accepts nothing
	if code, out, errs := d.run("accept --read-ok"); code != 0 {
		t.Errorf("accept --read-ok: exit %d\n%s%s", code, out, errs)
	} else {
		t.Logf("accept --read-ok on the new path: %s", strings.TrimSpace(out+errs))
	}
	d.ok("start")
	if code, out, _ := d.run("start"); code != 0 || !strings.Contains(out, "already") {
		t.Errorf("start twice: exit %d\n%s", code, out)
	}

	limit := 20 * time.Minute
	if dl, ok := t.Deadline(); ok && time.Until(dl)-90*time.Second < limit {
		limit = time.Until(dl) - 90*time.Second
	}
	ticks, stoppedAt, stalled := 0, 0, 0
	var lastOpen []driveGroup
	lastSeq := d.seq
	end := start.Add(limit)
	for time.Now().Before(end) {
		ticks++
		r := d.tick()
		if len(r.Short) > 0 {
			t.Errorf("tick %d: a short read: %v", ticks, r.Short)
		}
		d.follow()
		w = d.agree(fmt.Sprintf("tick %d", ticks))
		if w.All > 0 && w.Landed == w.All && !r.Running {
			stoppedAt = ticks
			break
		}
		d.world(w)
		lastOpen = d.coordinator()
		d.follow()
		// A world where nothing is written for 200 ticks is stuck: the drive
		// stops and says where.
		if d.seq == lastSeq {
			stalled++
		} else {
			stalled, lastSeq = 0, d.seq
		}
		if stalled == 200 {
			t.Errorf("stuck at tick %d: nothing written for 200 ticks; %s  %s", ticks, w.Summary, w.Machine)
			t.Logf("the view:\n%s", d.ok("where"))
			t.Logf("the inbox:\n%s", d.ok("inbox"))
			t.Logf("the last ticks:\n%s", strings.Join(d.tickRaw, "\n"))
			break
		}
		if ticks%50 == 0 {
			t.Logf("tick %d at %s: %s  %s", ticks, time.Since(start).Round(time.Second), w.Summary, w.Machine) // wall-ok: a progress line, no bound
		}
	}
	wall := time.Since(start)
	// One idle tick after the machine stopped itself, the world still: one
	// round trip, and nothing written.
	if stoppedAt > 0 {
		seq := d.seq
		d.tick()
		d.follow()
		if d.seq != seq {
			t.Errorf("the idle tick after the stop wrote lines %d..%d", seq+1, d.seq)
		}
	}
	w = d.agree("end")
	d.follow()

	// The numbers first.
	t.Logf("DRIVE: %d ticks, wall %s, %s, machine %q, stopped by itself at tick %d", ticks, wall.Round(time.Second), w.Summary, w.Machine, stoppedAt)
	t.Logf("first attempts by member: %s", driveCounts(d.first, driveMembers))
	t.Logf("redeals by member:        %s", driveCounts(d.redeal, driveMembers))
	t.Logf("reads asked by reader:    %s", driveCounts(d.reads, driveReaders))
	t.Logf("finished failed %d, reads broken %d", d.failed, d.broken)
	for _, s := range driveStreams {
		t.Logf("stream %s: gates landed %v; released %v", s, d.gates[s], d.released[s])
	}
	var idleMax, busyMax int64
	for _, n := range d.connIdle {
		idleMax = max(idleMax, n)
	}
	for _, n := range d.connBusy {
		busyMax = max(busyMax, n)
	}
	t.Logf("round trips a tick on the connection: %d idle ticks (max %d), %d busy ticks (max %d)", len(d.connIdle), idleMax, len(d.connBusy), busyMax)
	t.Logf("exit codes: %v; non-zero by verb: %v", d.codes, d.codeBy)
	for v, e := range d.lastErr {
		t.Logf("  last refusal of %s: %s", v, e)
	}
	t.Logf("judgments opened by type: %v", d.judged)
	for k, n := range d.texts {
		t.Logf("  opened x%d: %s", n, k)
	}
	t.Logf("judgment groups answered by type: %v", d.answers)
	t.Logf("happened by type: %v", d.happened)
	t.Logf("where/view/inbox agreement checked %d times", d.checks)

	// The asserts.
	if w.All != want || w.Landed != w.All {
		t.Errorf("not every card landed: %d of %d (%d on the table)", w.Landed, want, w.All)
	}
	if stoppedAt == 0 || !strings.HasPrefix(strings.ToUpper(w.Machine), "STOPPED") {
		t.Errorf("the machine did not stop itself at done: %q", w.Machine)
	}
	if !d.done {
		t.Errorf("no \"the sprint is done\" note in the log")
	}
	if d.judged[sprint.NSprintDone] > 0 {
		t.Errorf("the sprint is done was raised as a judgment %d times; it is a notice (the grammar decisions, done)", d.judged[sprint.NSprintDone])
	}
	// One rolling index for the fleet (errata 3 amendment 5, the owner's rule):
	// every placement, a first attempt or a redeal, moves it, so the deal is
	// round over both together and neither kind alone; an avoid skip (the
	// member that failed an attempt) can leave a member one behind.
	placed := map[string]int{}
	reworked := 0
	for _, m := range driveMembers {
		placed[m] = d.first[m] + d.redeal[m]
		reworked += d.redeal[m]
	}
	if lo, hi := driveSpread(placed, driveMembers); hi-lo > 2 {
		t.Errorf("placements (first attempts and redeals) by member spread %d..%d: %s", lo, hi, driveCounts(placed, driveMembers))
	}
	// The ask goes round the readers two at a time, and R8 asks a reworked
	// attempt of the readers already named on the primary (the same pair), so
	// the readers of a pair read alike and the pairs differ by at most the
	// reworked attempts.
	pairOf := map[string]string{}
	pairs := map[string]int{}
	for _, rs := range d.askedOf {
		p := strings.Join(slices.Sorted(slices.Values(rs)), "+")
		for _, r := range rs {
			if q, ok := pairOf[r]; ok && q != p {
				t.Errorf("reader %s was asked in the pairs %s and %s", r, q, p)
			}
			pairOf[r] = p
		}
		pairs[p] += len(rs)
	}
	for p := range pairs {
		rs := strings.Split(p, "+")
		for _, r := range rs[1:] {
			if d.reads[r] != d.reads[rs[0]] {
				t.Errorf("the readers of the pair %s read %d and %d", p, d.reads[rs[0]], d.reads[r])
			}
		}
	}
	lo, hi := -1, 0
	for _, n := range pairs {
		if lo < 0 || n < lo {
			lo = n
		}
		hi = max(hi, n)
	}
	if hi-lo > reworked {
		t.Errorf("the pairs read %v: they differ by %d, past the %d reworked attempts", pairs, hi-lo, reworked)
	}
	t.Logf("placements by member: %s; read pairs %v; reworked attempts %d", driveCounts(placed, driveMembers), pairs, reworked)
	for _, s := range driveStreams {
		var wantGates []string
		for k := 1; k <= 10; k++ {
			wantGates = append(wantGates, fmt.Sprintf("%s-gate-%d", s, k))
		}
		if !slices.Equal(d.gates[s], wantGates) {
			t.Errorf("stream %s: the gates landed %v, want %v", s, d.gates[s], wantGates)
		}
		if !slices.Equal(d.released[s], wantGates) {
			t.Errorf("stream %s: the gates released %v, want %v", s, d.released[s], wantGates)
		}
	}
	for code := range d.codes {
		if code != 0 && code != 2 && code != 3 {
			t.Errorf("exit code %d seen", code)
		}
	}
	if d.codes[exitBug] > 0 {
		t.Errorf("%d verbs exited 3 (a bug refusal)", d.codes[exitBug])
	}
	if len(d.connIdle) == 0 {
		t.Errorf("no idle tick was measured")
	}
	if n := d.judged[sprint.NStepRefused]; n > 0 {
		t.Errorf("the machine's step was refused %d times: %v", n, d.texts)
	}
	if idleMax > 1 || busyMax > 3 {
		t.Errorf("round trips a tick: idle at most %d (want 1), busy at most %d (want 3)", idleMax, busyMax)
	}
	var in driveInbox
	d.json("inbox", &in)
	for _, g := range in.Groups {
		if g.Kind == sprint.Judgment {
			t.Errorf("a judgment left open: %s %s x%d %v %s", g.ID, g.Type, g.Count, g.Primaries, g.What)
		}
	}
	_ = lastOpen
	if !strings.HasPrefix(strings.ToUpper(w.Machine), "STOPPED") {
		// A drive that did not finish stops the machine, so teardown is still
		// driven.
		d.ok("stop")
	}
	if code, out, errs := d.run("teardown --confirm sprint"); code != 0 {
		t.Errorf("teardown: exit %d\n%s%s", code, out, errs)
	}
	if k := keys(); len(k) != 1 || k[0] != layerNamespace+"sprint:lifecycle" {
		t.Errorf("after teardown the namespace holds %v, want its lifecycle receipts only", k)
	}
}
