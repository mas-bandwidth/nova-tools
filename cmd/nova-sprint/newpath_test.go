package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	spverbs "github.com/mas-bandwidth/nova-tools/internal/sprint/verbs"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The new path's tests (item IT23) drive the command's entry point, app.run,
// with app.newPath set, over the composed twin (sprintfn.Twin over tset.Mem,
// with the stack's X, derivation, J, parts and queries) through a recording
// sprintfn.Client, and nova-config as config.Mem naming the coordinator.

// npPrefix is Layer 1's namespace on the new path, the command's constant
// (there is no --prefix).
const npPrefix = layerNamespace

var npNames = pathNames

// npColumns are the set columns of the four tables, as the write path's and
// the verbs' own tests define them.
var npColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

// recorder is the client the command is given: the twin, each call's items
// and results kept, so a test can say what the command sent and what applied.
type recorder struct {
	c     sprintfn.Client
	mu    sync.Mutex
	calls [][]sprintfn.Item
	outs  [][]sprintfn.Result
}

func (r *recorder) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	out, err := r.c.Pipeline(ctx, items)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, items)
	r.outs = append(r.outs, out)
	return out, err
}

// since is the calls from the n-th on, and how many steps among them applied
// (a step that wrote: not refused, not a replay).
func (r *recorder) since(n int) (calls int, applied int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := n; i < len(r.calls); i++ {
		for j, it := range r.calls[i] {
			if it.Step == nil || j >= len(r.outs[i]) {
				continue
			}
			res := r.outs[i][j]
			if res.Err == nil && res.Refusal == nil && res.Step != nil && !res.Step.Reply.Replay {
				applied++
			}
		}
	}
	return len(r.calls) - n, applied
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// npApp is the command on the new path over one twin.
type npApp struct {
	t    *testing.T
	a    *app
	m    *tset.Mem
	tw   *sprintfn.Twin
	log  *sprintfn.MemLog
	rec  *recorder
	mu   sync.Mutex
	now  time.Time
	dir  string // the test's own directory, for files a line names
	olds int    // calls of the present path's store: none is the rule
}

func newNPApp(t *testing.T) *npApp {
	t.Helper()
	m := tset.NewMem()
	for _, table := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		if err := m.DefineTable(npPrefix, table, tset.TableDefinition{Columns: npColumns[table],
			MemberPrefix: npNames.TSetMemberPrefix(table), EpochKey: npNames.EpochKey(), EpochField: "n"}); err != nil {
			t.Fatalf("define %s: %v", table, err)
		}
	}
	na := &npApp{t: t, m: m, log: sprintfn.NewMemLog(), now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), dir: t.TempDir()}
	na.tw = sprintfn.NewTwin(m, na.log, npNames)
	na.tw.UseQueries()
	na.tw.UseIntents()
	na.tw.SetClock(na.clock)
	na.rec = &recorder{c: na.tw}
	cfg := config.NewMem()
	ctx := context.Background()
	if _, err := cfg.Insert(ctx, config.KindFriend, config.Row{Name: "coord", Fields: map[string]string{"slots": "1", "tiers": "pro"}}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.Update(ctx, config.KindSprint, config.KindSprint, map[string]string{"coordinator": "coord"}, "test"); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": "twin:0", "NOVA_SPRINT_ACTOR": "coord"}
	na.a = newApp(func(k string) string { return env[k] })
	if !na.a.newPath {
		t.Fatal("the switch is off: newApp runs the present path")
	}
	// run and where --watch end at an interrupt: here, the first one.
	na.a.notify = func(ctx context.Context) (context.Context, context.CancelFunc) {
		c, cancel := context.WithCancel(ctx)
		cancel()
		return c, cancel
	}
	na.a.noteStream = func(string) (spverbs.NoteStream, func() error, error) { return spverbs.NewMemStream(), nil, nil }
	na.a.now = na.clock
	na.a.sleep = func(d time.Duration) { na.mu.Lock(); na.now = na.now.Add(d); na.mu.Unlock() }
	na.a.sprintClient = func(context.Context, string, sprint.Names) (sprintfn.Client, func() error, error) {
		return na.rec, nil, nil
	}
	na.a.configRows = func(context.Context, string) (spverbs.ConfigRows, func() error, error) { return cfg, nil, nil }
	na.a.lifecycle = func(context.Context, string) (tset.Lifecycle, func() error, error) { return na.m, nil, nil }
	na.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) {
		na.mu.Lock()
		na.olds++
		na.mu.Unlock()
		return nil, errors.New("the new path reached the present path's store")
	}
	return na
}

func (na *npApp) clock() time.Time {
	na.mu.Lock()
	defer na.mu.Unlock()
	return na.now
}

// do runs a command line through the entry point; {dir} is the test's own
// directory.
func (na *npApp) do(line string) (int, string, string) {
	var out, errb bytes.Buffer
	code := na.a.run(split(strings.ReplaceAll(line, "{dir}", na.dir)), &out, &errb)
	na.mu.Lock()
	na.now = na.now.Add(time.Second)
	na.mu.Unlock()
	return code, out.String(), errb.String()
}

func (na *npApp) ok(line string) string {
	na.t.Helper()
	code, out, errs := na.do(line)
	if code != 0 {
		na.t.Fatalf("%s: exit %d\n%s%s", line, code, out, errs)
	}
	return out
}

// image is the twin's whole state: the tables, the sprint's keys, the log.
func (na *npApp) image() string {
	na.t.Helper()
	snap, err := na.m.Snapshot(npPrefix)
	if err != nil {
		na.t.Fatal(err)
	}
	var lines [][]json.RawMessage
	for e := 0; e < 4; e++ {
		lines = append(lines, na.log.Lines(npPrefix, tset.Decimal(strconv.Itoa(e))))
	}
	b, err := json.Marshal(struct {
		T any
		K any
		L any
	}{snap, na.tw.SprintKeys(), lines})
	if err != nil {
		na.t.Fatal(err)
	}
	return string(b)
}

// npCase is one line through the entry point: the lines before it (each must
// exit 0), the exit code it must give, and what its output (stdout and stderr
// together) must hold.
type npCase struct {
	verb  string // the table's verb the case covers
	setup []string
	line  string
	code  int
	want  []string
}

// stub is the case of a verb no item builds yet: refused, naming what it
// waits for.
func stub(verb, line, item string) npCase {
	return npCase{verb: verb, line: line, code: exitRefused, want: []string{codeNotOnNewPath, "not on the new path yet (" + item}}
}

var npInit = []string{"init"}

var npRunning = []string{"init", "start"}

// npWorld is a sprint with two readers, two fleet members, a stream of three
// primaries and its machine running (never ticked: the machine's deal is the
// tick loop's, IT17, which these cases do not run).
var npWorld = []string{"init", "reader add r1 r2", "fleet up m1 m2", "add --stream s1 --count 3", "start"}

// npWith is npWorld and more lines after it.
func npWith(more ...string) []string { return append(append([]string{}, npWorld...), more...) }

// npTicking is a sprint whose machine has ticked twice (the heartbeat's
// tick_at is written by the second tick's first step, A3), with a stream of
// six primaries added after the ticks: the view reads it running.
var npTicking = []string{"init", "reader add reader-a reader-b", "fleet up m1 m2", "fleet beat m1 m2", "start", "tick", "tick", "add --stream s1 --count 6"}

func npCases() []npCase {
	return []npCase{
		// IT18: the machine's verbs.
		{verb: "init", line: "init", code: 0, want: []string{"INIT OK", "epoch=0", "trips=2", "the sprint is made, STOPPED, with coordinator coord"}},
		{verb: "init", setup: npInit, line: "init", code: exitRefused, want: []string{"INIT FAIL", "code=MACHINESTATE changed=no", "already initialised"}},
		{verb: "init", line: "init --coordinator someone", code: 0, want: []string{"INIT OK", "with coordinator someone"}},
		{verb: "init", setup: npInit, line: "init --coordinator coord", code: 0, want: []string{"INIT OK", "the coordinator is coord"}},
		{verb: "init", setup: npInit, line: "init --coordinator other", code: exitRefused, want: []string{"code=NOTCOORD"}},
		{verb: "init", line: "init --pg config", code: 0, want: []string{"INIT OK", "with coordinator coord"}},
		{verb: "init", line: "init --pg config --coordinator other", code: exitRefused, want: []string{"nova-config names coord as the coordinator, and --coordinator names other"}},
		{verb: "init", line: "init --readers r1,r2", code: exitRefused, want: []string{"run nova-sprint reader add after init"}},
		{verb: "init", line: "init --members m1", code: exitRefused, want: []string{"run nova-sprint fleet up after init"}},
		{verb: "init", line: "init --prefix t:", code: exitRefused, want: []string{noPrefix}},
		{verb: "start", setup: npInit, line: "start", code: 0, want: []string{"START OK", "before=STOPPED after=RUNNING changed", "start: the machine runs", "NOT TICKING"}},
		{verb: "start", setup: npRunning, line: "start", code: 0, want: []string{"START OK", "before=RUNNING after=RUNNING unchanged: the machine is RUNNING already", "the machine is RUNNING already; nothing was written"}},
		{verb: "start", setup: npInit, line: "start --json", code: 0, want: []string{`"verb":"start"`, `"before":"STOPPED"`, `"after":"RUNNING"`, `"changed":true`, `"moved":[]`, `"exit":0`}},
		{verb: "start", line: "start", code: exitRefused, want: []string{"there is no sprint: run init"}},
		{verb: "stop", setup: npRunning, line: "stop", code: 0, want: []string{"STOP OK", "before=RUNNING after=STOPPED changed"}},
		{verb: "stop", setup: npInit, line: "stop", code: 0, want: []string{"STOP OK", "unchanged: the machine is STOPPED already"}},
		{verb: "stop", setup: []string{"init", "start", "stop --op op-stop-1"}, line: "stop --op op-stop-1", code: 0, want: []string{"STOP OK", "op=op-stop-1 replay=yes", "already applied at epoch 0; nothing was written"}},
		{verb: "clear", setup: npInit, line: "clear --confirm sprint", code: 0, want: []string{"CLEAR OK", "epoch=0->1", "clear: the sprint is at epoch 1, STOPPED"}},
		{verb: "clear", setup: npRunning, line: "clear --confirm sprint", code: exitRefused, want: []string{"code=MACHINESTATE", "run stop first"}},
		{verb: "clear", setup: npInit, line: "clear --confirm other", code: exitRefused, want: []string{"wants --confirm sprint"}},
		// The write path does not carry goals yet: the store refuses the step
		// REQUEST, a bug code (1.3.5), and the verb exits 3.
		{verb: "goal set", setup: npInit, line: "goal set p1 --file {dir}/goal.txt", code: exitBug, want: []string{"GOAL-SET FAIL", "code=REQUEST", "the write path does not carry goals yet"}},
		{verb: "goal set", setup: npInit, line: "goal set p1 --file {dir}/goal.txt --to file:/x", code: exitRefused, want: []string{"--to is not on the new path"}},
		{verb: "goal show", setup: npInit, line: "goal show p1", code: exitRefused, want: []string{"GOAL-SHOW FAIL", "code=REQUEST", "no read of a person's goal yet"}},
		{verb: "goal show", line: "goal show", code: exitRefused, want: []string{"takes one name"}},
		{verb: "goal drop", setup: npInit, line: "goal drop p1", code: exitBug, want: []string{"GOAL-DROP FAIL", "code=REQUEST"}},

		// IT17: the machine's loop.
		{verb: "run", setup: npRunning, line: "run", code: 0, want: []string{"RUN OK", "run: interrupted; the loop stopped ticking"}},
		{verb: "tick", setup: npRunning, line: "tick", code: 0, want: []string{"TICK OK", "tick: RUNNING, lease held true"}},
		{verb: "tick", setup: []string{"init", "start", "tick"}, line: "tick", code: 0, want: []string{"TICK OK", "steps applied, 0 refused"}},

		// IT19: add, release, rank.
		{verb: "add", setup: npInit, line: "add --stream s1 --count 3", code: 0, want: []string{"MOVED s1-1", "MOVED s1-3", "ADD OK moved=3", "parts=1", "STOPPED  0/3"}},
		{verb: "add", setup: npInit, line: "add --stream s1 --sentinel g1 --op op-add-1", code: 0, want: []string{"MOVED g1", "ADD OK moved=1", "op=op-add-1"}},
		{verb: "add", line: "add --stream s1", code: exitRefused, want: []string{"wants --stream and either ids, --count <n> or --sentinel <id>"}},
		{verb: "release", setup: []string{"init", "add --stream s1 --sentinel g1"}, line: "release g1 --reason looked", code: 0, want: []string{"MOVED g1", "RELEASE OK moved=1", "release: g1 landed"}},
		{verb: "release", setup: npWorld, line: "release s1-2 --reason looked", code: exitRefused, want: []string{"code=REQUEST", "s1-2 is not a sentinel"}},
		{verb: "release", setup: npWith("add --stream s1 --sentinel g1"), line: "release g1 --reason looked", code: exitRefused, want: []string{"3 open cards of stream s1 sort before g1"}},
		{verb: "rank", setup: npWorld, line: "rank s1-2 --after s1-3", code: 0, want: []string{"MOVED s1-2", "RANK OK moved=1", "rank: s1-2 rescored"}},
		{verb: "rank", setup: npWorld, line: "rank s1-2 --first", code: exitRefused, want: []string{"--first is not on the new path's rank yet"}},

		// IT20: the workers' verbs and the fleet's.
		{verb: "take", setup: npWith("fleet beat m1 m2"), line: "take --as m1", code: 0, want: []string{"take: m1 has nothing ready; nothing was written"}},
		{verb: "take", setup: npWorld, line: "take --as m1 s1-1.w1@1", code: exitRefused, want: []string{"TAKE FAIL", "code=REQUEST", "no work card is placed"}},
		{verb: "take", line: "take --as m1 s1-1", code: exitRefused, want: []string{"<card>@<gen>"}},
		{verb: "finish", setup: npWorld, line: "finish --as m1 s1-1.w1@1 --failed --report red", code: exitRefused, want: []string{"FINISH FAIL", "no work card is placed"}},
		{verb: "finish", setup: npWith("stop", "clear --confirm sprint"), line: "finish --as m1 s1-1.w1@1 --epoch 0", code: exitRefused, want: []string{"code=STALE", "the sprint was cleared at epoch 1: this verb holds epoch 0"}},
		{verb: "read", setup: npWorld, line: "read --as r1 --ok s1-1.r1.r1", code: exitRefused, want: []string{"READ FAIL", "no read card is placed"}},
		{verb: "read", setup: npWorld, line: "read --as r1 --ok --limit 1", code: exitRefused, want: []string{"--limit is not on the new path's read yet"}},
		{verb: "queue", setup: npWorld, line: "queue --stream s1", code: 0, want: []string{"queue: 3 cards", "s1-1 ready", "s1-3 ready"}},
		{verb: "queue", setup: npWorld, line: "queue --as m1 --json", code: 0, want: []string{`{"cards":[]}`}},
		{verb: "fleet beat", setup: npWorld, line: "fleet beat m1 m2", code: 0, want: []string{"FLEET-BEAT OK", "fleet beat: 2 members"}},
		{verb: "fleet up", setup: npInit, line: "fleet up m1 m2", code: 0, want: []string{"MOVED ctl-m1", "MOVED ctl-m2", "FLEET-UP OK moved=2", "fleet up: down until they beat: m1, m2"}},
		{verb: "fleet down", setup: npWorld, line: "fleet down m1", code: 0, want: []string{"MOVED ctl-m1", "FLEET-DOWN OK moved=1", "held m1"}},
		{verb: "reader add", setup: npInit, line: "reader add r1 r2", code: 0, want: []string{"READER-ADD OK", "reader add: r1, r2"}},

		// IT21: review, merge, drop.
		{verb: "ask", setup: npWorld, line: "ask s1-1 --another", code: exitRefused, want: []string{"ASK FAIL", "not in review"}},
		{verb: "ask", setup: npWorld, line: "ask --stream s1", code: exitRefused, want: []string{"--stream is not on the new path's ask yet"}},
		{verb: "accept", setup: npWorld, line: "accept s1-1", code: exitRefused, want: []string{"ACCEPT FAIL", "not in review"}},
		{verb: "accept", setup: npWorld, line: "accept --stream s1", code: 0, want: []string{"ACCEPT OK", "finished in 1 parts"}},
		{verb: "rework", setup: npWorld, line: "rework s1-1 --fix 'handle the empty case'", code: exitRefused, want: []string{"REWORK FAIL", "not review"}},
		{verb: "return", setup: npWorld, line: "return s1-1", code: exitRefused, want: []string{"RETURN FAIL", "not merging"}},
		{verb: "return", setup: npWorld, line: "return s1-1 --reason suspect", code: exitRefused, want: []string{"--reason is not on the new path's return yet"}},
		{verb: "drop", setup: npWorld, line: "drop s1-3 --reason obsolete", code: 0, want: []string{"MOVED s1-3", "DROP OK moved=1", "0/2"}},
		{verb: "drop", setup: npWorld, line: "drop --stream s1 --col ready --reason obsolete", code: 0, want: []string{"MOVED s1-1", "DROP OK moved=3"}},
		{verb: "drop", setup: npWorld, line: "drop --abort --op op-drop-1", code: exitRefused, want: []string{"there is nothing to abort"}},
		{verb: "drop", line: "drop --abort", code: exitRefused, want: []string{"drop --abort takes --op <op> alone"}},
		{verb: "ci", setup: npWorld, line: "ci s1-1 --red", code: 0, want: []string{"MOVED s1-1", "CI OK moved=1 refused=0 notes=1"}},
		{verb: "ci", setup: npWorld, line: "ci s1-1 --red --run 812", code: exitRefused, want: []string{"--run is not on the new path's ci yet"}},
		{verb: "merge", setup: npWorld, line: "merge --stream s1 --batch 100 --red --suspect s1-2 s1-3", code: exitRefused, want: []string{"MERGE FAIL", "nothing is queued in stream s1"}},
		{verb: "resume", setup: npWorld, line: "resume --stream s1 --did rebased", code: exitRefused, want: []string{"RESUME FAIL", "not stopped"}},

		// IT22: judgments and reads.
		{verb: "ack", setup: npWith("ci s1-1 --red"), line: "ack n11 --reason flaky", code: 0, want: []string{"ACK OK", "ack: 1 notes answered"}},
		{verb: "wait", setup: npWith("ci s1-1 --red"), line: "wait n11 --for 30m --reason later", code: 0, want: []string{"WAIT OK", "wait: 1 notes wait 30m0s"}},
		{verb: "inbox", setup: npWith("ci s1-1 --red"), line: "inbox", code: 0, want: []string{"JUDGMENT n11 ci red on a primary", "the machine is not ticking", "HAPPENED"}},
		{verb: "inbox", setup: npWorld, line: "inbox --json", code: 0, want: []string{`"groups":[`, `"machine":"NOT TICKING"`}},
		{verb: "inbox", setup: npWorld, line: "inbox --read", code: 0, want: []string{"INBOX OK", "INBOX CURSOR"}},
		{verb: "inbox", setup: npWorld, line: "inbox --read --after 0", code: exitRefused, want: []string{"--read moves the coordinator's stored cursor and --after reads from one of the caller's own"}},
		{verb: "inbox", setup: npWorld, line: "inbox --read --wait", code: exitRefused, want: []string{"--read and --wait are two calls"}},
		{verb: "inbox", setup: npWorld, line: "inbox --actor someone --read", code: exitRefused, want: []string{"NOTCOORD"}},
		{verb: "card", setup: npWorld, line: "card s1-1", code: 0, want: []string{"card s1-1 at s1:ready"}},
		{verb: "log", setup: npWorld, line: "log --stream s1", code: 0, want: []string{`"kind":"create"`, `"s1-1","s1-2","s1-3"`}},
		{verb: "where", setup: npWorld, line: "where --json", code: 0, want: []string{`"all":3`, `"machine":"NOT TICKING"`}},
		{verb: "where", setup: npWorld, line: "where --watch --every 2s", code: 0, want: []string{"SPRINT TABLE", "NOT TICKING"}},

		// teardown: Layer 1's lifecycle (TestInitDefinesAndTeardownDeletesTheNamespace).
		{verb: "teardown", setup: npWorld, line: "teardown --confirm other", code: exitRefused, want: []string{"wants --confirm sprint"}},
		// check: rule 11, the cycles of needs (errata 3 amendment 7)
		{verb: "check", setup: npWorld, line: "check", code: 0, want: []string{"no cycle of needs in 1 streams"}},
		// No item builds these yet: the remove after; repair; not in section 3:
		// resolve, fleet level (kept as present).
		stub("remove", "remove --stream s1 --confirm sprint", "IT27"),
		stub("remove", "remove --abort --op op-rm-1", "IT27"),
		stub("repair", "repair", "AL7"),
		stub("resolve", "resolve s1-1", "no item"),
		stub("fleet level", "fleet level", "no item"),
		// play: R8's driver over the entry point, on the new path.
		{verb: "play", setup: npTicking, line: "play --ticks 1 --simulation", code: 0, want: []string{"chances:", "tick 1", "PLAY OK stopped=ticks"}},
		{verb: "play", setup: npWorld, line: "play --ticks 1 --simulation", code: exitRefused, want: []string{"no machine is running (NOT TICKING)"}},
	}
}

// TestEveryVerbOnNewPath (IT23): every verb of the table, through the
// command's entry point on the new path against the twin. Each verb has a
// case; the machine's verbs (IT18) run on the twin and the stubbed ones are
// refused naming their item, their words and flags parsed as they will be.
func TestEveryVerbOnNewPath(t *testing.T) {
	t.Parallel()
	cases := npCases()
	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.verb] = true
	}
	var missing []string
	for _, v := range newVerbNamesSorted() {
		if !covered[v] {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("verbs of the table with no case: %v", missing)
	}
	for i, c := range cases {
		t.Run(strconv.Itoa(i)+" "+c.line, func(t *testing.T) {
			t.Parallel()
			na := newNPApp(t)
			if err := os.WriteFile(filepath.Join(na.dir, "goal.txt"), []byte("keep the sprint moving"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, s := range c.setup {
				na.ok(s)
			}
			code, out, errs := na.do(c.line)
			all := out + errs
			if code != c.code {
				t.Fatalf("%s: exit %d, want %d\n%s", c.line, code, c.code, all)
			}
			for _, w := range c.want {
				if !strings.Contains(all, w) {
					t.Errorf("%s: output lacks %q\n%s", c.line, w, all)
				}
			}
			if na.olds != 0 {
				t.Errorf("%s: the present path's store was opened %d times", c.line, na.olds)
			}
		})
	}
}

// TestNewPathGrammar: the entry point's own words on the new path: help,
// version, an unknown verb, a group with no subverb, a verb's --help.
func TestNewPathGrammar(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line string
		code int
		want string
	}{
		{"help", 0, "exit codes: 0 done, 2 refused, 3 a bug refusal"},
		{"version", 0, "nova-sprint"},
		{"", exitRefused, "no verb; available: init"},
		{"frobnicate", exitRefused, "unknown verb frobnicate"},
		{"fleet", exitRefused, "unknown or missing subverb"},
		{"help fleet", 0, "nova-sprint fleet up <member>..."},
		{"help add", 0, "-sentinel-every"},
		{"add --help", 0, "-count"},
		{"start --actor ''", exitRefused, "--actor <name> is required"},
	} {
		na := newNPApp(t)
		code, out, errs := na.do(c.line)
		if code != c.code || !strings.Contains(out+errs, c.want) {
			t.Errorf("%q: exit %d, want %d and %q\n%s%s", c.line, code, c.code, c.want, out, errs)
		}
	}
}

// TestNewPathExitCodes: exitOf by the kind of error (8.1, IT23; 1.3.5).
func TestNewPathExitCodes(t *testing.T) {
	t.Parallel()
	ref := func(code string, local bool) error {
		return &spverbs.Refused{Verb: "v", Local: local, Refusal: &sprintfn.Refusal{Code: code}}
	}
	for _, c := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{ref("LIMIT", false), exitBug},
		{ref("REQUEST", false), exitBug},
		{ref("OPCONFLICT", false), exitBug},
		{ref("REQUEST", true), exitRefused},       // the verb's own, from its read
		{ref("MACHINESTATE", false), exitRefused}, // the sprint's refusal
		{ref("NOTCOORD", false), exitRefused},
		{ref("REVISION", false), exitRefused},                               // a race past its retries
		{ref("EXISTS", false), exitRefused},                                 // a race on a derived id (1.0)
		{ref("DRIFT", false), exitRefused},                                  // a card the lower layers refuse
		{&spverbs.Unknown{Verb: "v", Err: tset.ErrOutcomeUnknown}, exitBug}, // an unconfirmed write (the grammar decisions, 26)
		{usage("words"), exitRefused},
		{errors.New("the store did not answer"), exitRefused},
	} {
		if got := exitOf(c.err); got != c.want {
			t.Errorf("exitOf(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

// TestCommandFCALLOnly (E7, IT23): the command on the new path writes only
// through the verbs' one write, a Step item of sprintfn.Client (FCALL
// ns_sprint_step on the store; sprintfn's TestComposeFCALLOnly holds the
// client to that). Over a suite of every verb, the twin's whole image changes
// on a line only when a step of that line applied (a step may apply and
// change nothing: init --coordinator naming the coordinator the sprint has),
// and a replay applies nothing; the present path's store
// is never opened; and the new path's files import no Redis client, no
// present-path store and no table function library, and build their client
// only with sprintfn.NewRedis. The store's half is
// TestCommandFCALLOnlyOnTheStore.
func TestCommandFCALLOnly(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	if err := os.WriteFile(filepath.Join(na.dir, "goal.txt"), []byte("keep the sprint moving"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := []string{"init", "init", "init --coordinator", "start", "start", "stop", "stop --op op-f-1", "start", "stop --op op-f-1",
		"clear --confirm sprint", "stop", "goal set p1 --file {dir}/goal.txt", "goal show p1", "goal drop p1"}
	for _, c := range npCases() {
		if c.setup == nil && c.code == exitRefused {
			lines = append(lines, c.line)
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "play") {
			continue
		}
		before, n := na.image(), na.rec.count()
		code, out, errs := na.do(line)
		calls, applied := na.rec.since(n)
		changed := na.image() != before
		if changed && applied == 0 {
			t.Errorf("%s (exit %d, %d calls, %d steps applied): the twin changed %v\n%s%s", line, code, calls, applied, changed, out, errs)
		}
		if code == 0 && strings.Contains(out, "replay=yes") && applied != 0 {
			t.Errorf("%s: a replay applied %d steps", line, applied)
		}
	}
	if na.olds != 0 {
		t.Errorf("the present path's store was opened %d times", na.olds)
	}

	// Layer 1's lifecycle is the one other route to the store, and it lives in
	// newpath_lifecycle.go alone: define and teardown through tset.Lifecycle,
	// on tset.NewRedis once, with the build from fn (the lifecycle amendment).
	refused := []string{"github.com/redis/go-redis", "internal/sprint/store", "internal/redisconn", "internal/nsprint/fn", "internal/ntable", "internal/tset"}
	fset := gotoken.NewFileSet()
	lf, err := parser.ParseFile(fset, "newpath_lifecycle.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range lf.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		for _, r := range refused {
			if strings.Contains(path, r) && !strings.HasSuffix(path, "/internal/tset") && !strings.HasSuffix(path, "/internal/nsprint/fn") {
				t.Errorf("newpath_lifecycle.go imports %s: Layer 1's lifecycle needs tset and the build only", path)
			}
		}
	}
	if src, err := os.ReadFile("newpath_lifecycle.go"); err != nil || strings.Count(string(src), "NewRedis(") != 1 {
		t.Errorf("newpath_lifecycle.go: %v, want one tset.NewRedis", err)
	}
	for _, name := range []string{"newpath.go", "newpath_verbs.go", "newpath_calls.go"} {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, r := range refused {
				if strings.Contains(path, r) {
					t.Errorf("%s imports %s: the new path reaches the store only through sprintfn.Client", name, path)
				}
			}
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(src), "NewRedis("); name == "newpath.go" && n != 1 || name != "newpath.go" && n != 0 {
			t.Errorf("%s: %d calls of NewRedis; the new path's one client is sprintfn.NewRedis, once", name, n)
		}
	}
}

// TestCommandFCALLOnlyOnTheStore: the same suite on the store's client, with
// testredis.OnlyFCALL on it. sprintfn builds its client itself and takes no
// hook from a caller (redis.go: newRedisWithClient is its own tests' seam),
// so the command's half on the store waits for G0 and the store in the
// container, with sprintfn's TestComposeFCALLOnlyOnTheStore.
func TestCommandFCALLOnlyOnTheStore(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) and the sprint profile loaded in a container; sprintfn takes no hook from a caller")
}

// TestPlaySimulationOnNewPath (IT23): R8's driver, run by play --simulation
// through the entry point on the new path, reads where --json, queue --json
// and inbox --json as before and plays its ticks. The machine ticks twice
// before play (the driver plays only the outside actors, and reads the
// machine running), and the stream is added after those ticks.
func TestPlaySimulationOnNewPath(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	for _, l := range npTicking {
		na.ok(l)
	}
	out := na.ok("play --simulation --ticks 3")
	if !strings.Contains(out, "PLAY OK stopped=ticks") || !strings.Contains(out, "tick 3 ") {
		t.Fatalf("play:\n%s", out)
	}
	var w struct {
		Epoch   uint64 `json:"epoch"`
		All     int64  `json:"all"`
		Machine string `json:"machine"`
	}
	if err := json.Unmarshal([]byte(na.ok("where --json")), &w); err != nil || w.All != 6 || w.Epoch != 0 {
		t.Fatalf("where --json after play: %+v, %v", w, err)
	}
	if na.olds != 0 {
		t.Fatalf("the present path's store was opened %d times", na.olds)
	}
}

// TestNewTableCoversThePresentVerbs: every verb of the present command is in
// the new path's table (the switch keeps the grammar), and the table's verbs
// beyond them are section 3's.
func TestNewTableCoversThePresentVerbs(t *testing.T) {
	t.Parallel()
	inNew := map[string]bool{}
	for _, v := range newVerbs {
		inNew[v.name] = true
	}
	var missing []string
	for _, v := range verbs {
		if !inNew[v.name] {
			missing = append(missing, v.name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("present verbs not in the new path's table: %v", missing)
	}
	present := map[string]bool{}
	for _, v := range verbs {
		present[v.name] = true
	}
	var added []string
	for _, v := range newVerbs {
		if !present[v.name] {
			added = append(added, v.name)
		}
	}
	if strings.Join(added, ",") != "remove" {
		t.Errorf("verbs the new path adds: %v, want [remove] (section 3)", added)
	}
}

// TestDealOnNewPath: the tick deals through the command on the twin (2.3 R6;
// errata 3 amendments 4 and 5). R6's read names every stream's front, from the
// streams the tick's first read found, so the second tick deals, and never
// stops the process on a front its read did not load (the repro of the
// integration's gap 1, with the members beating so that they are up). One
// stream of three goes m1, m2, m1; two streams go in turns, s1 then s2, round
// the fleet, every card in one deal (the room is each member's width, 64 by
// default: errata 3 amendment 9).
func TestDealOnNewPath(t *testing.T) {
	t.Parallel()
	dealt := func(na *npApp) []string {
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(na.ok("log")), "\n") {
			var line struct {
				Kind, Table, To string
				IDs             []string
			}
			if err := json.Unmarshal([]byte(l), &line); err != nil {
				t.Fatalf("log line %q: %v", l, err)
			}
			if line.Kind == "create" && line.Table == sprint.Fleet && strings.HasSuffix(line.To, ":ready") {
				for _, id := range line.IDs {
					out = append(out, id+">"+strings.TrimSuffix(line.To, ":ready"))
				}
			}
		}
		return out
	}
	for _, c := range []struct {
		add  []string
		want string
	}{
		{[]string{"add --stream s1 --count 3"}, "s1-1.w1>m1 s1-2.w1>m2 s1-3.w1>m1"},
		{[]string{"add --stream s1 --count 3", "add --stream s2 --count 2"}, "s1-1.w1>m1 s2-1.w1>m2 s1-2.w1>m1 s2-2.w1>m2 s1-3.w1>m1"},
	} {
		na := newNPApp(t)
		for _, l := range append(append([]string{"init", "reader add r1 r2", "fleet up m1 m2", "fleet beat m1 m2"}, c.add...), "start", "tick", "tick") {
			na.ok(l)
		}
		if got := strings.Join(dealt(na), " "); got != c.want {
			t.Fatalf("%v: dealt %s, want %s", c.add, got, c.want)
		}
	}
}

// bugOnce refuses the first step it is armed for with a bug code, as a store
// that refuses a verb's step CONFIG would, and passes every other item on.
type bugOnce struct {
	c     sprintfn.Client
	mu    sync.Mutex
	armed bool
}

func (b *bugOnce) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	b.mu.Lock()
	armed := b.armed && len(items) == 1 && items[0].Step != nil
	if armed {
		b.armed = false
	}
	b.mu.Unlock()
	if armed {
		return []sprintfn.Result{{Refusal: &sprintfn.Refusal{Code: "CONFIG", Message: "CONFIG: the store's definition disagrees; nothing was changed",
			Detail: sprintfn.RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "entries"}}}}}, nil
	}
	return b.c.Pipeline(ctx, items)
}

// TestExitThreeWritesTheJudgment: a verb whose step the store refuses with a
// bug code exits 3 and writes "the machine's step was refused" (1.3.5: "a
// verb that receives such a refusal prints it as an error (exit 3) and writes
// the same judgment, so the coordinator sees it whoever ran the verb"; the
// grammar decisions, 31): one step of notes only, open on the verb, its cause
// the code, its text naming the verb, the code and the bound. A refusal the
// verb makes itself (exit 2) writes none.
func TestExitThreeWritesTheJudgment(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	bug := &bugOnce{c: na.rec}
	na.a.sprintClient = func(context.Context, string, sprint.Names) (sprintfn.Client, func() error, error) {
		return bug, nil, nil
	}
	na.ok("init")
	bug.mu.Lock()
	bug.armed = true
	bug.mu.Unlock()
	code, out, errs := na.do("reader add r1")
	if code != exitBug || !strings.Contains(errs, `judgment "the machine's step was refused" open on reader add, cause CONFIG`) {
		t.Fatalf("reader add refused CONFIG: exit %d\n%s%s", code, out, errs)
	}
	judged := func() int {
		n := 0
		for _, l := range strings.Split(na.ok("log"), "\n") {
			if strings.Contains(l, `"type":"the machine's step was refused"`) && strings.Contains(l, `"cause":"CONFIG"`) {
				n++
			}
		}
		return n
	}
	if n := judged(); n != 1 {
		t.Fatalf("%d judgments in the log after the bug:\n%s", n, na.ok("log"))
	}
	if text := na.ok("inbox"); !strings.Contains(text, "verb reader add, code CONFIG, bound entries") {
		t.Fatalf("the inbox does not name the verb, the code and the bound:\n%s", text)
	}
	if code, out, errs := na.do("start --op op-x --epoch 9"); code != exitRefused || judged() != 1 {
		t.Fatalf("a refusal of the verb's own: exit %d, %d judgments\n%s%s", code, judged(), out, errs)
	}
}

// TestInitDefinesAndTeardownDeletesTheNamespace: init defines the namespace
// through Layer 1's lifecycle before its clock step (the four tables of
// spverbs.TableColumns, set columns, and the view sprint, with this build),
// and init on a namespace already defined goes on to the clock step;
// teardown --confirm sprint deletes it through the lifecycle, and refuses
// RUNNING while the machine runs, NOSPACE when there is none, and any other
// confirmation before anything is sent.
func TestInitDefinesAndTeardownDeletesTheNamespace(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	fresh := tset.NewMem()
	build, err := fn.TSetBuild(fn.TSetSprint)
	if err != nil {
		t.Fatal(err)
	}
	fresh.SetBuild(build)
	na.a.lifecycle = func(context.Context, string) (tset.Lifecycle, func() error, error) { return fresh, nil, nil }
	na.ok("init")
	if v := fresh.View(npPrefix); v != "sprint" {
		t.Fatalf("init defined the view %q, want sprint", v)
	}
	receipts := fresh.LifecycleReceipts(npPrefix)
	if len(receipts) != 1 || receipts[0].Fn != "define" || receipts[0].Build != build ||
		!slices.Equal(receipts[0].Tables, []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}) {
		t.Fatalf("the define: %+v", receipts)
	}
	if code, out, errs := na.do("init"); code != exitRefused || !strings.Contains(out+errs, "already initialised") {
		t.Fatalf("init again: exit %d\n%s%s", code, out, errs)
	}
	if code, out, errs := na.do("teardown --confirm other"); code != exitRefused || len(fresh.LifecycleReceipts(npPrefix)) != 1 {
		t.Fatalf("teardown with another name: exit %d\n%s%s", code, out, errs)
	}
	fresh.SetRunning(npPrefix, true)
	if code, out, errs := na.do("teardown --confirm sprint"); code != exitRefused || !strings.Contains(out+errs, "RUNNING") {
		t.Fatalf("teardown while running: exit %d\n%s%s", code, out, errs)
	}
	fresh.SetRunning(npPrefix, false)
	if out := na.ok("teardown --confirm sprint"); !strings.Contains(out, "teardown: the sprint is gone") || fresh.View(npPrefix) != "" {
		t.Fatalf("teardown: %s, the view %q", out, fresh.View(npPrefix))
	}
	if code, out, errs := na.do("teardown --confirm sprint"); code != exitRefused || !strings.Contains(out+errs, "NOSPACE") {
		t.Fatalf("teardown of no sprint: exit %d\n%s%s", code, out, errs)
	}
}
