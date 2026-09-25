// lines.go: the machine, clock, ci and process lines of the table (#3045;
// #2756 2.4 item 5, section 1, 6.4), read the way SprintReader reads the
// whole table: one pipelined round trip per tick once the membership is
// known, never KEYS and never SCAN. Keys, all read-only here:
//
//	benches, friends            SMEMBERS: the registered benches and friends
//	sprint:order                ZRANGE -1 -1 when no sprint is named: the current sprint
//	bench:<b>:desired           HMGET slots machine (written by capacity only)
//	friend:<f>:desired          HMGET slots machine
//	bench:<b>:cards:working     ZCARD, the working children (the one lease ledger,
//	                            #3998); friend:<f>:cards:working the same
//	machine:<m>:ceiling         HGET slots, the machine's child ceiling
//	bench:<m>:beat              HMGET load1 at: the machine's own beat (a
//	                            friend-only machine beats with --presence-only)
//	proc:<name>                 HGET pass_at (ms): reconciler, harvest:<b> per
//	                            bench, ok-to-friend, pr-to-read, hold-to-fix,
//	                            backpressure
//	s:<S>:idx:card:<state>      SMEMBERS for queued, dealt, launched, running,
//	                            ended and harvested
//	s:<S>:card:<label>          HMGET of the ci cards (labels ci-*) in those
//	                            states and of every card ended or harvested
//
// A machine is every name a desired hash's machine field names. Its line is
// the ceiling, the desired slots summed over every bench and friend on it,
// their living children summed, and its load1; desired over the ceiling is a
// red line (the invariant of 2.4 item 3 broken).
//
// The clocks are the enqueue clocks of section 1 the tool bounds, each the
// age of its oldest item, red at or past its bound:
//
//	harvest-start   ended DONE, harvest not started (no harvest_step), from ended_at, < 60 s
//	verified-pr     ended DONE, not yet harvested, from ended_at, < 5 min
//	review-enqueue  harvested, not yet review-ready, from harvested_at, < 60 s
//	read-enqueued   ended DONE or harvested, from ended_at, < 6 min
//
// A clock item with no timestamp is counted, prints its oldest as ? and
// makes the line red: the bound cannot be shown to hold.
//
// The ci line counts the sprint's ci cards as the ci status verb does: cut is
// queued; running is dealt, launched or running; the verdict on the record
// (MISSING when absent) and blocked; oldest is the cut age of the oldest card
// without an OK. A process line is up with the age of its last pass, ? with
// that age when the pass is older than ProcStale, or missing.
package table

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// LinesConfig is what the lines reader is told; everything else is read.
type LinesConfig struct {
	// Sprint names the sprint of the clock and ci lines; empty reads the
	// newest of sprint:order.
	Sprint string
	// ProcStale: a process pass older than this prints ?; zero is 20 s, the
	// bound ns_snapshot uses.
	ProcStale time.Duration
}

// Clock is one pipeline clock of #2756 section 1 and its bound.
type Clock struct {
	Name  string
	Bound time.Duration
}

// Clocks are the enqueue clocks, in the order they print.
var Clocks = []Clock{
	{"harvest-start", 60 * time.Second},
	{"verified-pr", 5 * time.Minute},
	{"review-enqueue", 60 * time.Second},
	{"read-enqueued", 6 * time.Minute},
}

// Procs are the process lines, in the order they print; harvest:<b> for each
// bench follows the reconciler.
var Procs = []string{"reconciler", "ok-to-friend", "pr-to-read", "hold-to-fix", "backpressure"}

// MachineLine is one machine: ceiling, the desired and living sums over every
// bench and friend on it, and its load1 (- no beat, ? a stale beat).
type MachineLine struct {
	Name           string
	Ceiling        int64
	CeilingMissing bool
	Desired        int64
	Living         int64
	Load1          string
}

// Over says the desired sum breaks the machine's ceiling.
func (m MachineLine) Over() bool { return !m.CeilingMissing && m.Desired > m.Ceiling }

// ClockLine is one clock: how many items are on it and the oldest one's age
// in seconds (-1 when no item's age is known).
type ClockLine struct {
	Clock
	N       int64
	Oldest  int64
	Unknown int64 // items with no timestamp
}

// Breach says the oldest item is at or past the bound, or an item's age is
// unknown.
func (c ClockLine) Breach() bool {
	return c.Unknown > 0 || (c.Oldest >= 0 && c.Oldest*int64(time.Second) >= int64(c.Bound))
}

// CILine is the sprint's ci cards (#2756 6.4, 4.8).
type CILine struct {
	Cut, Running, Pending, OK, Fail, Flaky, Missing, Blocked int64
	Oldest                                                   int64 // seconds; -1 when every card is OK
}

// ProcLine is one process: State is up, ? (stale) or missing; Age is the
// seconds since its last pass.
type ProcLine struct {
	Name  string
	State string
	Age   int64
}

// Lines is one tick's machine, clock, ci and process lines.
type Lines struct {
	Sprint   string
	Machines []MachineLine
	Clocks   []ClockLine
	CI       CILine
	Procs    []ProcLine
	// RoundTrips is how many pipelines the read took: 1 in steady state.
	RoundTrips int
}

// cardStates are the fine card states the reader lists.
var cardStates = []string{"queued", "dealt", "launched", "running", "ended", "harvested"}

// cardFields are the record fields read per listed card, in reply order.
var cardFields = []string{"outcome", "ended_at", "where_at", "harvested_at", "harvest_step", "verdict", "blocked", "cut_at"}

// linesMembers is the membership one tick's values are read against.
type linesMembers struct {
	benches, friends, machines []string
	sprint                     string
	labels                     map[string][]string // state -> labels whose record is read
}

func (m linesMembers) equal(o linesMembers) bool {
	if !slices.Equal(m.benches, o.benches) || !slices.Equal(m.friends, o.friends) ||
		!slices.Equal(m.machines, o.machines) || m.sprint != o.sprint || len(m.labels) != len(o.labels) {
		return false
	}
	for s, l := range m.labels {
		if !slices.Equal(l, o.labels[s]) {
			return false
		}
	}
	return true
}

// LinesReader reads the lines, keeping membership across ticks.
type LinesReader struct {
	Client  redis.UniversalClient
	Config  LinesConfig
	members linesMembers
	primed  bool
}

// NewLinesReader is a reader with no membership yet: its first Read learns the
// registries, then the machines and cards, then reads the values.
func NewLinesReader(client redis.UniversalClient, cfg LinesConfig) *LinesReader {
	return &LinesReader{Client: client, Config: cfg}
}

// Read is one tick at now.
func (r *LinesReader) Read(ctx context.Context, now time.Time) (*Lines, error) {
	for trips := 1; trips <= 5; trips++ {
		lines, changed, err := r.readOnce(ctx, now)
		if err != nil {
			return nil, err
		}
		if !changed {
			lines.RoundTrips = trips
			return lines, nil
		}
	}
	return nil, errors.New("lines: membership changed on five reads in a row")
}

type desiredCmds struct {
	desired *redis.SliceCmd
	living  *redis.IntCmd
}

func (r *LinesReader) readOnce(ctx context.Context, now time.Time) (*Lines, bool, error) {
	m := r.members
	pipe := r.Client.Pipeline()
	benchSet := pipe.SMembers(ctx, "benches")
	friendSet := pipe.SMembers(ctx, "friends")
	var newest *redis.StringSliceCmd
	if r.Config.Sprint == "" {
		newest = pipe.ZRange(ctx, "sprint:order", -1, -1)
	}
	benches := make([]desiredCmds, len(m.benches))
	harvest := make([]*redis.StringCmd, len(m.benches))
	for i, b := range m.benches {
		benches[i] = desiredCmds{pipe.HMGet(ctx, "bench:"+b+":desired", "slots", "machine"), pipe.ZCard(ctx, "bench:"+b+":cards:working")}
		harvest[i] = pipe.HGet(ctx, "proc:harvest:"+b, "pass_at")
	}
	friends := make([]desiredCmds, len(m.friends))
	for i, f := range m.friends {
		friends[i] = desiredCmds{pipe.HMGet(ctx, "friend:"+f+":desired", "slots", "machine"), pipe.ZCard(ctx, "friend:"+f+":cards:working")}
	}
	ceilings := make([]*redis.StringCmd, len(m.machines))
	beats := make([]*redis.SliceCmd, len(m.machines))
	for i, name := range m.machines {
		ceilings[i] = pipe.HGet(ctx, "machine:"+name+":ceiling", "slots")
		beats[i] = pipe.HMGet(ctx, "bench:"+name+":beat", "load1", "at")
	}
	procs := make([]*redis.StringCmd, len(Procs))
	for i, p := range Procs {
		procs[i] = pipe.HGet(ctx, "proc:"+p, "pass_at")
	}
	idx := map[string]*redis.StringSliceCmd{}
	records := map[string][]*redis.SliceCmd{}
	if m.sprint != "" {
		for _, s := range cardStates {
			idx[s] = pipe.SMembers(ctx, "s:"+m.sprint+":idx:card:"+s)
			for _, l := range m.labels[s] {
				records[s] = append(records[s], pipe.HMGet(ctx, "s:"+m.sprint+":card:"+l, cardFields...))
			}
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, false, fmt.Errorf("lines pipeline: %w", err)
	}
	gotBenches, err := benchSet.Result()
	if err != nil {
		return nil, false, fmt.Errorf("smembers benches: %w", err)
	}
	gotFriends, _ := friendSet.Result()
	sort.Strings(gotBenches)
	sort.Strings(gotFriends)
	got := linesMembers{benches: gotBenches, friends: gotFriends, sprint: r.Config.Sprint, labels: map[string][]string{}}
	if newest != nil {
		if names, err := newest.Result(); err == nil && len(names) == 1 {
			got.sprint = names[0]
		}
	}
	type onMachine struct {
		machine       string
		slots, living int64
	}
	var consumers []onMachine
	seen := map[string]bool{}
	for _, c := range append(slices.Clone(benches), friends...) {
		vals, err := c.desired.Result()
		if err != nil || len(vals) != 2 {
			continue
		}
		name := pipeValue(vals[1])
		if name == "" {
			continue
		}
		slots, _ := strconv.ParseInt(pipeValue(vals[0]), 10, 64)
		consumers = append(consumers, onMachine{name, slots, c.living.Val()})
		if !seen[name] {
			seen[name] = true
			got.machines = append(got.machines, name)
		}
	}
	sort.Strings(got.machines)
	for s, cmd := range idx {
		labels, _ := cmd.Result()
		keep := []string{}
		for _, l := range labels {
			if strings.HasPrefix(l, "ci-") || s == "ended" || s == "harvested" {
				keep = append(keep, l)
			}
		}
		sort.Strings(keep)
		got.labels[s] = keep
	}
	if got.sprint != m.sprint {
		got.labels = map[string][]string{}
	}
	changed := !r.primed || !got.equal(m)
	r.primed = true
	if changed {
		r.members = got
		return nil, true, nil
	}

	nowMS := now.UnixMilli()
	lines := &Lines{Sprint: m.sprint, CI: CILine{Oldest: -1}}
	for i, name := range m.machines {
		line := MachineLine{Name: name, Load1: "-"}
		if v, err := ceilings[i].Result(); err == nil {
			line.Ceiling, _ = strconv.ParseInt(v, 10, 64)
		} else {
			line.CeilingMissing = true
		}
		for _, c := range consumers {
			if c.machine == name {
				line.Desired += c.slots
				line.Living += c.living
			}
		}
		if vals, err := beats[i].Result(); err == nil && len(vals) == 2 {
			load, at := sanitize(pipeValue(vals[0])), pipeValue(vals[1])
			if atMS, err := strconv.ParseInt(at, 10, 64); err == nil && nowMS-atMS <= hostBeatStale.Milliseconds() && load != "" {
				line.Load1 = load
			} else if at != "" || load != "" {
				line.Load1 = "?"
			}
		}
		lines.Machines = append(lines.Machines, line)
	}
	stale := r.Config.ProcStale
	if stale <= 0 {
		stale = 20 * time.Second
	}
	proc := func(name string, cmd *redis.StringCmd) ProcLine {
		at, err := strconv.ParseInt(cmd.Val(), 10, 64)
		if err != nil || cmd.Err() != nil {
			return ProcLine{Name: name, State: "missing", Age: -1}
		}
		age := max(0, (nowMS-at)/1000)
		if time.Duration(age)*time.Second > stale {
			return ProcLine{Name: name, State: "?", Age: age}
		}
		return ProcLine{Name: name, State: "up", Age: age}
	}
	lines.Procs = append(lines.Procs, proc(Procs[0], procs[0]))
	for i, b := range m.benches {
		lines.Procs = append(lines.Procs, proc("harvest:"+b, harvest[i]))
	}
	for i := 1; i < len(Procs); i++ {
		lines.Procs = append(lines.Procs, proc(Procs[i], procs[i]))
	}
	clocks := make([]ClockLine, len(Clocks))
	for i, c := range Clocks {
		clocks[i] = ClockLine{Clock: c, Oldest: -1}
	}
	onClock := func(i int, at int64) {
		clocks[i].N++
		if at <= 0 {
			clocks[i].Unknown++
			return
		}
		if age := max(0, (nowMS-at)/1000); age > clocks[i].Oldest {
			clocks[i].Oldest = age
		}
	}
	for _, s := range cardStates {
		for j, l := range m.labels[s] {
			vals, err := records[s][j].Result()
			if err != nil || len(vals) != len(cardFields) {
				continue
			}
			f := map[string]string{}
			for k, name := range cardFields {
				f[name] = pipeValue(vals[k])
			}
			if strings.HasPrefix(l, "ci-") {
				lines.CI.add(s, f, nowMS)
				continue
			}
			ended := msField(f["ended_at"])
			if ended <= 0 {
				ended = msField(f["where_at"])
			}
			switch {
			case s == "ended" && f["outcome"] == "DONE":
				if f["harvest_step"] == "" {
					onClock(0, ended)
				}
				onClock(1, ended)
				onClock(3, ended)
			case s == "harvested":
				onClock(2, msField(f["harvested_at"]))
				onClock(3, ended)
			}
		}
	}
	lines.Clocks = clocks
	return lines, false, nil
}

// add counts one ci card in state s with record fields f (the rule of the ci
// status verb, internal/nsprint/ci ReadStatus).
func (c *CILine) add(s string, f map[string]string, nowMS int64) {
	switch s {
	case "queued":
		c.Cut++
	case "dealt", "launched", "running":
		c.Running++
	}
	verdict := f["verdict"]
	switch verdict {
	case "OK":
		c.OK++
	case "PENDING":
		c.Pending++
	case "FAIL":
		c.Fail++
	case "FLAKY":
		c.Flaky++
	default:
		c.Missing++
	}
	if f["blocked"] != "" {
		c.Blocked++
	}
	if cut := msField(f["cut_at"]); verdict != "OK" && cut > 0 {
		if age := max(0, (nowMS-cut)/1000); age > c.Oldest {
			c.Oldest = age
		}
	}
}

func msField(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Render prints the lines: the machine block, the clocks, the ci line and the
// process lines. A red line starts with RED, as the table's error lines do.
func (l *Lines) Render() string {
	var b strings.Builder
	b.WriteString("machine | ceiling | desired | living | load1\n")
	for _, m := range l.Machines {
		ceiling := strconv.FormatInt(m.Ceiling, 10)
		if m.CeilingMissing {
			ceiling = "missing"
		}
		line := fmt.Sprintf("machine:%s | %s | %d | %d | %s", m.Name, ceiling, m.Desired, m.Living, m.Load1)
		if m.Over() {
			line = "RED " + line + fmt.Sprintf(" | over: %d/%d", m.Desired, m.Ceiling)
		}
		b.WriteString(line + "\n")
	}
	for _, c := range l.Clocks {
		oldest := "-"
		switch {
		case c.Oldest >= 0:
			oldest = strconv.FormatInt(c.Oldest, 10) + "s"
		case c.N > 0:
			oldest = "?"
		}
		line := fmt.Sprintf("clock %s | bound %s | n %d | oldest %s", c.Name, boundText(c.Bound), c.N, oldest)
		if c.Breach() {
			line = "RED " + line
		}
		b.WriteString(line + "\n")
	}
	oldest := "-"
	if l.CI.Oldest >= 0 {
		oldest = strconv.FormatInt(l.CI.Oldest, 10) + "s"
	}
	fmt.Fprintf(&b, "ci: cut %d, running %d, PENDING %d, OK %d, FAIL %d, FLAKY %d, MISSING-at-head %d, blocked-no-alternate-bench %d, oldest %s\n",
		l.CI.Cut, l.CI.Running, l.CI.Pending, l.CI.OK, l.CI.Fail, l.CI.Flaky, l.CI.Missing, l.CI.Blocked, oldest)
	for _, p := range l.Procs {
		if p.State == "missing" {
			fmt.Fprintf(&b, "proc %s missing\n", p.Name)
			continue
		}
		fmt.Fprintf(&b, "proc %s %s %ds\n", p.Name, p.State, p.Age)
	}
	return b.String()
}

func boundText(d time.Duration) string {
	if d%time.Minute == 0 && d >= 2*time.Minute {
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	}
	return strconv.FormatInt(int64(d/time.Second), 10) + "s"
}
