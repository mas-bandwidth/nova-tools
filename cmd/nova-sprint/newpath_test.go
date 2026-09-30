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
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
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
	na.a.newPath = true
	na.a.now = na.clock
	na.a.sleep = func(d time.Duration) { na.mu.Lock(); na.now = na.now.Add(d); na.mu.Unlock() }
	na.a.sprintClient = func(context.Context, string, sprint.Names) (sprintfn.Client, func() error, error) {
		return na.rec, nil, nil
	}
	na.a.configRows = func(context.Context, string) (spverbs.ConfigRows, func() error, error) { return cfg, nil, nil }
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
// together) must hold. A stubbed verb's case wants exit 2 and its item named;
// when the item lands, the case's code and want change and nothing else.
type npCase struct {
	verb  string // the table's verb the case covers
	setup []string
	line  string
	code  int
	want  []string
}

// stub is a stubbed verb's case: refused, naming the item.
func stub(verb, line, item string) npCase {
	return npCase{verb: verb, line: line, code: exitRefused, want: []string{codeNotOnNewPath, "not on the new path yet (" + item}}
}

var npInit = []string{"init"}

var npRunning = []string{"init", "start"}

func npCases() []npCase {
	return []npCase{
		// IT18: the machine's verbs, on the twin.
		{verb: "init", line: "init", code: 0, want: []string{"INIT OK", "epoch=0", "trips=2", "the sprint is made, STOPPED, with coordinator coord"}},
		{verb: "init", setup: npInit, line: "init", code: exitRefused, want: []string{"INIT FAIL code=MACHINESTATE changed=no", "already initialised"}},
		{verb: "init", setup: npInit, line: "init --coordinator", code: 0, want: []string{"INIT OK", "the coordinator is coord"}},
		{verb: "init", setup: npInit, line: "init --coordinator --actor other", code: exitRefused, want: []string{"code=NOTCOORD"}},
		{verb: "init", line: "init --coordinator someone", code: exitRefused, want: []string{"--coordinator takes no name on the new path"}},
		{verb: "init", line: "init --readers r1,r2", code: exitRefused, want: []string{"run nova-sprint reader add after init"}},
		{verb: "init", line: "init --members m1", code: exitRefused, want: []string{"run nova-sprint fleet up after init"}},
		{verb: "start", setup: npInit, line: "start", code: 0, want: []string{"START OK before=STOPPED after=RUNNING changed", "start: the machine runs"}},
		{verb: "start", setup: npRunning, line: "start", code: exitRefused, want: []string{"START FAIL before=RUNNING after=RUNNING unchanged: the machine is RUNNING already code=MACHINESTATE"}},
		{verb: "start", setup: npInit, line: "start --json", code: 0, want: []string{`"verb":"start"`, `"before":"STOPPED"`, `"after":"RUNNING"`, `"changed":true`, `"moved":[]`, `"exit":0`}},
		{verb: "start", line: "start", code: exitRefused, want: []string{"there is no sprint: run init"}},
		{verb: "stop", setup: npRunning, line: "stop", code: 0, want: []string{"STOP OK before=RUNNING after=STOPPED changed"}},
		{verb: "stop", setup: npInit, line: "stop", code: exitRefused, want: []string{"code=MACHINESTATE", "already stopped"}},
		{verb: "stop", setup: []string{"init", "start", "stop --op op-stop-1"}, line: "stop --op op-stop-1", code: 0, want: []string{"STOP OK", "op=op-stop-1 replay=yes", "already applied at epoch 0; nothing was written"}},
		{verb: "clear", setup: npInit, line: "clear --confirm sprint", code: 0, want: []string{"CLEAR OK", "epoch=0->1", "clear: the sprint is at epoch 1, STOPPED"}},
		{verb: "clear", setup: npRunning, line: "clear --confirm sprint", code: exitRefused, want: []string{"code=MACHINESTATE", "run stop first"}},
		{verb: "clear", setup: npInit, line: "clear --confirm other", code: exitRefused, want: []string{"wants --confirm sprint"}},
		// The write path does not carry goals yet: the store refuses the step
		// REQUEST, a bug code (1.3.5), and the verb exits 3.
		{verb: "goal set", setup: []string{"init"}, line: "goal set p1 --file {dir}/goal.txt", code: exitBug, want: []string{"GOAL-SET FAIL code=REQUEST", "the write path does not carry goals yet"}},
		{verb: "goal set", setup: npInit, line: "goal set p1 --file {dir}/goal.txt --to file:/x", code: exitRefused, want: []string{"--to is not on the new path"}},
		{verb: "goal show", setup: npInit, line: "goal show p1", code: exitRefused, want: []string{"GOAL-SHOW FAIL code=REQUEST", "no read of a person's goal yet"}},
		{verb: "goal show", line: "goal show", code: exitRefused, want: []string{"takes one name"}},
		{verb: "goal drop", setup: npInit, line: "goal drop p1", code: exitBug, want: []string{"GOAL-DROP FAIL code=REQUEST"}},

		// IT17.
		stub("run", "run", "IT17"),
		stub("tick", "tick", "IT17"),
		// IT19.
		stub("add", "add --stream s1 --count 3", "IT19"),
		{verb: "add", line: "add --stream s1", code: exitRefused, want: []string{"wants --stream and either ids, --count <n> or --sentinel <id>"}},
		stub("release", "release s1-gate-1 --reason looked", "IT19"),
		stub("rank", "rank s1-2 --first", "IT19"),
		stub("rank", "rank s1-2 --after s1-3", "IT19"),
		// IT20.
		stub("take", "take --as m1 s1-1@1", "IT20"),
		{verb: "take", line: "take --as m1 s1-1", code: exitRefused, want: []string{"<card>@<gen>"}},
		stub("finish", "finish --as m1 s1-1@1 --failed --report red", "IT20"),
		stub("read", "read --as r1 --ok s1-1", "IT20"),
		stub("queue", "queue --as m1", "IT20"),
		stub("fleet beat", "fleet beat m1 m2", "IT20"),
		stub("fleet up", "fleet up m1 m2", "IT20"),
		stub("fleet down", "fleet down m1", "IT20"),
		stub("reader add", "reader add r1 r2", "IT20"),
		// IT21.
		stub("ask", "ask s1-1 --another", "IT21"),
		stub("accept", "accept s1-1", "IT21"),
		stub("accept", "accept --stream s1,s2", "IT21"),
		{verb: "rework", line: "rework s1-1 --fix 'handle the empty case'", code: exitRefused, want: []string{"REWORK FAIL code=REQUEST", "no such card on the table"}},
		stub("return", "return s1-1 --reason suspect", "IT21"),
		{verb: "drop", line: "drop s1-1 --reason obsolete", code: exitRefused, want: []string{"DROP FAIL code=REQUEST", "not open on the table"}},
		{verb: "drop", line: "drop --stream s1,s2 --col ready --reason obsolete", code: exitRefused, want: []string{"DROP FAIL code=NOROW"}},
		{verb: "drop", line: "drop --abort --op op-drop-1", code: exitRefused, want: []string{"DROP FAIL code=REQUEST", "the sprint has no stream: op op-drop-1 freezes nothing"}},
		{verb: "drop", line: "drop --abort", code: exitRefused, want: []string{"drop --abort takes --op <op> alone"}},
		stub("ci", "ci s1-1 --red --run 812", "IT21"),
		stub("merge", "merge --stream s1 --batch 100 --red --suspect s1-4 s1-5", "IT21"),
		stub("resume", "resume --stream s1,s2 --did rebased", "IT21"),
		// IT22.
		stub("ack", "ack n-1 --reason flaky", "IT22"),
		stub("wait", "wait n-1 n-2 --for 30m --reason later", "IT22"),
		stub("inbox", "inbox --read", "IT22"),
		stub("card", "card s1-1", "IT22"),
		stub("log", "log --card s1-1", "IT22"),
		stub("where", "where --json", "IT22"),
		stub("where", "where --watch --every 2s", "IT22"),
		// After: the check, remove; no item: teardown, repair; not in
		// section 3: resolve, fleet level.
		stub("check", "check", "IT26"),
		stub("remove", "remove --stream s1 --confirm sprint", "IT27"),
		stub("remove", "remove --abort --op op-rm-1", "IT27"),
		stub("teardown", "teardown --confirm sprint", "no item"),
		stub("repair", "repair", "AL7"),
		stub("resolve", "resolve s1-1", "no item"),
		stub("fleet level", "fleet level", "no item"),
		// play: the driver over the entry point reads where --json first,
		// which is IT22's; until it lands the driver cannot read the view.
		{verb: "play", setup: npRunning, line: "play --ticks 1 --simulation", code: exitRefused, want: []string{"the view could not be read"}},
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
		{ref("REVISION", false), exitRefused}, // a race past its retries
		{ref("EXISTS", false), exitRefused},   // a race on a derived id (1.0)
		{ref("DRIFT", false), exitRefused},    // a card the lower layers refuse
		{&spverbs.Unknown{Verb: "v", Err: tset.ErrOutcomeUnknown}, exitRefused},
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

	refused := []string{"github.com/redis/go-redis", "internal/sprint/store", "internal/redisconn", "internal/nsprint/fn", "internal/ntable", "internal/tset"}
	fset := gotoken.NewFileSet()
	for _, name := range []string{"newpath.go", "newpath_verbs.go"} {
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
// and inbox --json as before and plays a tick. It needs the read verbs of
// IT22 (Where, Inbox), IT20 (Queue) and IT17's run: until they land, play is
// refused at its first read (TestEveryVerbOnNewPath's play case pins that).
func TestPlaySimulationOnNewPath(t *testing.T) {
	t.Parallel()
	t.Skip("IT22 (verbs.Where, verbs.Inbox), IT20 (verbs.Queue) and IT17 (machine.Run): the driver's reads are stubbed on the new path until they land")
	na := newNPApp(t)
	for _, l := range []string{"init", "reader add reader-a reader-b", "fleet up m1 m2", "add --stream s1 --count 6", "start"} {
		na.ok(l)
	}
	out := na.ok("play --simulation --ticks 3")
	if !strings.Contains(out, "PLAY OK stopped=") {
		t.Fatalf("play:\n%s", out)
	}
	var w struct {
		Epoch   uint64 `json:"epoch"`
		All     int64  `json:"all"`
		Machine string `json:"machine"`
	}
	if err := json.Unmarshal([]byte(na.ok("where --json")), &w); err != nil || w.All != 6 {
		t.Fatalf("where --json after play: %+v, %v", w, err)
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

// TestReworkAndDropOnNewPath (IT21 on IT23): rework and drop driven through
// the command entry point on the new path over the twin.
func TestReworkAndDropOnNewPath(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	na.ok("init")
	na.ok("start")

	// Initialize score counter once:
	res, err := sprintfn.Step(context.Background(), na.tw, &sprintfn.Request{
		Epoch:  "0",
		Meta:   sprintfn.Meta{Verb: "fixture", Actor: "coord"},
		Sprint: &sprintfn.SprintPart{Counter: &sprintfn.CounterChange{Read: map[string]string{"score": ""}, Set: map[string]string{"score": "1000000"}}},
	})
	if err != nil || res.Refusal != nil || res.Err != nil {
		t.Fatalf("init counter: %v %v %v", err, res.Refusal, res.Err)
	}

	raw := func(entries ...tset.Entry) {
		t.Helper()
		res, err := sprintfn.Step(context.Background(), na.tw, &sprintfn.Request{
			Epoch: "0",
			Meta:  sprintfn.Meta{Verb: "fixture", Actor: "coord"},
			Body:  sprintfn.Body{Entries: entries},
		})
		if err != nil || res.Refusal != nil || res.Err != nil {
			t.Fatalf("fixture: err %v, refusal %v, result err %v", err, res.Refusal, res.Err)
		}
	}

	raw(
		tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{"s1"}},
		tset.Entry{Kind: "rows", Table: sprint.Fleet, Add: []string{"m1", "m2"}},
		tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:review", IDs: []string{"p1"}, Scores: []string{"1"},
			Each: []map[string]string{{"kind": "work", "attempt": "1", "head": "h1", "result": "failed", "work": sprint.WorkCardID("p1", 1)}}, About: []string{"p1"}},
		tset.Entry{Kind: "create", Table: sprint.Fleet, To: "m1:working", IDs: []string{sprint.WorkCardID("p1", 1)}, Scores: []string{"1"},
			Each: []map[string]string{{"kind": "work", sprint.PrimaryField: "p1", "stream": "s1", "attempt": "1", "member": "m1"}}, About: []string{sprint.WorkCardID("p1", 1)}},
	)

	// rework p1:
	out := na.ok("rework p1 --fix 'fix error handling'")
	if !strings.Contains(out, "REWORK OK") {
		t.Fatalf("rework: %s", out)
	}

	// Now drop p1:
	out = na.ok("drop p1 --reason obsolete")
	if !strings.Contains(out, "DROP OK") {
		t.Fatalf("drop: %s", out)
	}

	// Drop a stream:
	raw(
		tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{"s2"}},
		tset.Entry{Kind: "create", Table: sprint.Work, To: "s2:ready", IDs: []string{"p2"}, Scores: []string{"1"},
			Each: []map[string]string{{"kind": "work", "attempt": "0"}}, About: []string{"p2"}},
	)
	out = na.ok("drop --stream s2 --reason 'out of scope'")
	if !strings.Contains(out, "DROP OK") {
		t.Fatalf("drop --stream: %s", out)
	}

	// Freeze s1 with op-abort-1 and abort it:
	res, err = sprintfn.Step(context.Background(), na.tw, &sprintfn.Request{
		Epoch:  "0",
		Meta:   sprintfn.Meta{Verb: "fixture", Actor: "coord"},
		Sprint: &sprintfn.SprintPart{Dropping: map[string]string{"s1": "op-abort-1"}},
	})
	if err != nil || res.Refusal != nil || res.Err != nil {
		t.Fatalf("freeze fixture: %v %v %v", err, res.Refusal, res.Err)
	}
	out = na.ok("drop --abort --op op-abort-1")
	if !strings.Contains(out, "DROP OK") || !strings.Contains(out, "op-abort-1 aborted") {
		t.Fatalf("drop --abort: %s", out)
	}
}
