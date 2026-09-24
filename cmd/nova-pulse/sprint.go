package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/deal"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// `nova-pulse sprint` is the bounded set of work and the four questions asked of it: how far
// along (x/y z%), how long is left (the WALL, not the sum), where does each task go (the
// router, over the consumers table), and how good were the estimates (calibration).
//
// It lives under nova-pulse and not in a `nova-sprint` of its own because the noun is the
// same one nova-pulse already owns: pulse is the coordinator's bounded work -- it cuts the
// cards, fills the benches, harvests what comes back and draws the swarm table. A sprint is
// that work with a boundary drawn round it and friends counted in the same x/y. AGENTS.md's
// layout is one tool per noun, and a second binary here would put `fill` (which deals cards
// to consumers) and `sprint refill` (which deals tasks to the same consumers by the same
// rule) in two binaries that must agree forever.

// sprintDeps are the seams a test replaces: the store, and the two primary-record readers.
// cards is a factory rather than a value, like open, because the ev:cards half now dials
// the fleet Redis too (#2587's stream) and does not know --store, --store-user or the
// password until the flags are parsed.
type sprintDeps struct {
	open    func(addr, user, password string) (sprint.Store, error)
	records func(friends []string) sprint.Records
	cards   func(addr, user, password string) (sprint.Cards, error)
	getenv  func(string) string
	// setCheck runs nova-work set check and returns its stdout and exit code;
	// sleep waits between reconciliation rounds. Both are seams so a test runs
	// the evaluate verb without a gh, a network or a fixed wait.
	setCheck func(ctx context.Context, bin string, args []string) (string, int, error)
	sleep    func(time.Duration)
}

func defaultSprintDeps() sprintDeps {
	return sprintDeps{
		open: func(addr, user, password string) (sprint.Store, error) {
			return sprint.Dial(addr, user, password)
		},
		records: func(friends []string) sprint.Records {
			return sprint.NewGH("", os.Getenv("GH_CONFIG_DIR"), friends, 30*time.Second)
		},
		cards: func(addr, user, password string) (sprint.Cards, error) {
			return sprint.DialCards(context.Background(), addr, user, password)
		},
		getenv:   os.Getenv,
		setCheck: runSetCheck,
		sleep:    time.Sleep,
	}
}

// cmdSprint is the single "nova-pulse sprint" dispatcher. It owns the funnel sub-verb
// (implemented in funnel.go, as cmdSprintFunnel: recording/reporting card events) and the
// bounded-task-set verbs below (via runSprint), because both live under the same "sprint"
// noun and a second dispatcher for the same verb would drift from this one forever.
func cmdSprint(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		return refuse(stderr, " sprint", "a sub-verb is required; it is one of funnel, open, add, status, route, split, refill, wall, calibration, evaluate, close")
	}
	if args[0] == "funnel" {
		return cmdSprintFunnel(args[1:], stdout, stderr, now)
	}
	return runSprint(args, stdout, stderr, now, defaultSprintDeps())
}

func runSprint(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	if len(args) == 0 {
		return refuse(stderr, " sprint", "no verb given; it is one of open, add, status, route, split, refill, wall, calibration, evaluate, close")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "open":
		return sprintOpen(rest, stdout, stderr, now, deps)
	case "add":
		return sprintAdd(rest, stdout, stderr, now, deps)
	case "status":
		return sprintStatus(rest, stdout, stderr, now, deps)
	case "route":
		return sprintRoute(rest, stdout, stderr, now, deps)
	case "split":
		return sprintSplit(rest, stdout, stderr, now, deps)
	case "refill":
		return sprintRefill(rest, stdout, stderr, now, deps)
	case "wall":
		return sprintWall(rest, stdout, stderr, now, deps)
	case "calibration":
		return sprintCalibration(rest, stdout, stderr, now, deps)
	case "evaluate":
		return sprintEvaluate(rest, stdout, stderr, now, deps)
	case "close":
		return sprintClose(rest, stdout, stderr, now, deps)
	}
	return refuse(stderr, " sprint", fmt.Sprintf("unknown verb %q; it is one of open, add, status, route, split, refill, wall, calibration, evaluate, close", verb))
}

// storeFlags are the three flags every sprint verb takes to reach the store. The password is
// read from the NAMED ENVIRONMENT VARIABLE and never from a flag: a flag is in the process
// table, and nova-secrets exec is what puts it in the environment of this process alone.
type storeFlags struct {
	addr        *string
	user        *string
	passwordEnv *string
}

func addStoreFlags(f *flags) storeFlags {
	return storeFlags{
		addr:        f.fs.String("store", "", ""),
		user:        f.fs.String("store-user", "bench", ""),
		passwordEnv: f.fs.String("store-password-env", "NOVA_REDIS_BENCH_PASSWORD", ""),
	}
}

func (s storeFlags) open(f *flags, deps sprintDeps) (sprint.Store, error) {
	f.want(*s.addr, "store", "host:port of the fleet Redis, e.g. 100.115.99.19:6380")
	if strings.TrimSpace(*s.addr) == "" {
		return nil, fmt.Errorf("no store")
	}
	return deps.open(*s.addr, *s.user, s.password(deps))
}

// password reads the store password from the NAMED ENVIRONMENT VARIABLE, never from a flag
// (a flag is in the process table). Both the store and the ev:cards half read it the same
// way, because they are the same fleet Redis.
func (s storeFlags) password(deps sprintDeps) string {
	if deps.getenv == nil {
		return ""
	}
	return deps.getenv(*s.passwordEnv)
}

// openCards dials the ev:cards half over the same addr/user/password as the store. It
// returns a nil Cards, no error when deps.cards is nil (a test that never flips), so
// flipFromRecords can pass it straight to sprint.Flip.
func (s storeFlags) openCards(deps sprintDeps) (sprint.Cards, error) {
	if deps.cards == nil {
		return nil, nil
	}
	return deps.cards(*s.addr, *s.user, s.password(deps))
}

func sprintOpen(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint open")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	goal := f.fs.String("goal", "", "")
	plannedClose := f.fs.String("planned-close", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*name, "name", "the sprint's id, e.g. fixes-2026-09-22")
	f.want(*goal, "goal", "one sentence naming what this bounded set is toward")
	planned := time.Time{}
	if strings.TrimSpace(*plannedClose) != "" {
		t, err := time.Parse(time.RFC3339, *plannedClose)
		if err != nil {
			f.add(fmt.Sprintf("--planned-close wants an RFC3339 time like 2026-09-22T22:00:00Z, got %q", *plannedClose))
		} else {
			planned = t.UTC()
		}
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint open", err.Error())
	}
	defer st.Close()
	s := sprint.Sprint{Name: *name, Goal: *goal, OpenedAt: now, PlannedCloseAt: planned}
	if err := st.PutSprint(context.Background(), s); err != nil {
		return refuse(stderr, " sprint open", err.Error())
	}
	if code := writeProgress(stderr, "sprint open", context.Background(), st, s.Name); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "SPRINT OPEN %s %s\n", s.Name, s.Goal)
	return 0
}

func sprintAdd(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint add")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	id := f.fs.String("id", "", "")
	ref := f.fs.String("ref", "", "")
	kind := f.fs.String("kind", "", "")
	owner := f.fs.String("owner", "", "")
	est := f.fs.Int("est", 0, "")
	paths := f.fs.String("paths", "", "")
	dependsOn := f.fs.String("depends-on", "", "")
	leg := f.fs.String("leg", "", "")
	locality := f.fs.String("locality", "", "")
	isolation := f.fs.String("isolation", "", "")
	routes := f.fs.String("routes", "", "")
	repo := f.fs.String("repo", "", "")
	base := f.fs.String("base", "", "")
	reader := f.fs.String("reader", "", "")
	priority := f.fs.Bool("priority", false, "")
	// --leased-at is for a task that was already somebody's before it was written down:
	// the actual at close is measured from it, and a task added with none simply has no
	// actual, which is a fact the calibration prints rather than a zero it averages in.
	leasedAt := f.fs.String("leased-at", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*name, "name", "the sprint this task belongs to")
	f.want(*ref, "ref", "the primary record this task is done BY: owner/name#123, a card label, or a memory slug")
	f.want(*kind, "kind", "one of "+strings.Join(sprint.Kinds, "|"))
	if *kind != "" {
		if err := sprint.ValidateKind(*kind); err != nil {
			f.add(err.Error())
		}
	}
	taskID := strings.TrimSpace(*id)
	if taskID == "" {
		taskID = taskIDFrom(*ref)
	}
	if err := sprint.ValidateName("task", taskID); err != nil {
		f.add(err.Error())
	}
	minutes := *est
	if minutes == 0 {
		minutes = sprint.DefaultEstimate[*kind]
	}
	leased := time.Time{}
	if strings.TrimSpace(*leasedAt) != "" {
		t, err := time.Parse(time.RFC3339, *leasedAt)
		if err != nil {
			f.add(fmt.Sprintf("--leased-at wants an RFC3339 time, got %q", *leasedAt))
		} else {
			leased = t.UTC()
		}
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint add", err.Error())
	}
	defer st.Close()
	t := sprint.Task{
		ID: taskID, Kind: *kind, Ref: *ref, Owner: strings.ToLower(*owner), State: sprint.StateOpen,
		EstMinutes: minutes, DependsOn: splitList(*dependsOn), Paths: splitList(*paths),
		Leg: *leg, Locality: *locality, Isolation: *isolation, Routes: splitList(*routes),
		Repo: *repo, Base: *base, Reader: strings.ToLower(*reader), Priority: *priority, CreatedAt: now,
		LeasedAt: leased,
	}
	ctx := context.Background()
	if err := st.PutTask(ctx, t); err != nil {
		return refuse(stderr, " sprint add", err.Error())
	}
	if err := st.AddTask(ctx, *name, t.ID); err != nil {
		return refuse(stderr, " sprint add", err.Error())
	}
	if code := writeProgress(stderr, "sprint add", ctx, st, *name); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "TASK %s %s kind=%s owner=%s est=%s\n", t.ID, t.Ref, t.Kind, dash(t.Owner), sprint.Minutes(t.EstMinutes))
	return 0
}

func sprintStatus(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint status")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	verbose := f.fs.Bool("verbose", false, "")
	flip := f.fs.Bool("flip", false, "")
	friends := f.fs.String("friends", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint status", err.Error())
	}
	defer st.Close()
	ctx := context.Background()
	active, err := activeSprints(ctx, st, *name)
	if err != nil {
		return refuse(stderr, " sprint status", err.Error())
	}
	if len(active) == 0 {
		return refuse(stderr, " sprint status", "no sprint is open; run: nova-pulse sprint open --name <id> --goal <one sentence>")
	}
	if *flip {
		cards, err := sf.openCards(deps)
		if err != nil {
			return refuse(stderr, " sprint status", err.Error())
		}
		if closer, ok := cards.(interface{ Close() error }); ok {
			defer closer.Close()
		}
		for _, s := range active {
			if code := flipFromRecords(ctx, st, s.Name, splitList(*friends), now, deps, cards, stdout, stderr); code != 0 {
				return code
			}
		}
	}
	if len(active) == 1 {
		tasks, err := st.Tasks(ctx, active[0].Name)
		if err != nil {
			return refuse(stderr, " sprint status", err.Error())
		}
		line, err := sprint.LineWithLeft(active[0], tasks, now)
		if err != nil {
			return refuse(stderr, " sprint status", err.Error())
		}
		fmt.Fprintln(stdout, line)
		if *verbose {
			v, err := sprint.Verbose(tasks)
			if err != nil {
				return refuse(stderr, " sprint status", err.Error())
			}
			fmt.Fprint(stdout, v)
		}
		return 0
	}
	var rows []sprint.TableRow
	for _, s := range active {
		tasks, err := st.Tasks(ctx, s.Name)
		if err != nil {
			return refuse(stderr, " sprint status", err.Error())
		}
		line, err := sprint.LineWithLeft(s, tasks, now)
		if err != nil {
			return refuse(stderr, " sprint status", err.Error())
		}
		rows = append(rows, sprint.TableRow{Name: s.Name, Line: line})
	}
	fmt.Fprint(stdout, sprint.Table(rows))
	return 0
}

func flipFromRecords(ctx context.Context, st sprint.Store, name string, friends []string, now time.Time, deps sprintDeps, cards sprint.Cards, stdout, stderr io.Writer) int {
	tasks, err := st.Tasks(ctx, name)
	if err != nil {
		return 2
	}
	var recs sprint.Records
	if deps.records != nil {
		recs = deps.records(friends)
	}
	changed, problems := sprint.Flip(ctx, tasks, recs, cards, now)
	for _, t := range changed {
		if err := st.PutTask(ctx, t); err != nil {
			return 2
		}
		fmt.Fprintf(stdout, "CLOSED %s %s %s\n", t.ID, t.Ref, t.Evidence)
	}
	for _, p := range problems {
		fmt.Fprintf(stdout, "UNREADABLE %s\n", p)
	}
	if len(changed) > 0 {
		if code := writeProgress(stderr, "sprint status", ctx, st, name); code != 0 {
			return code
		}
	}
	return 0
}

func sprintRoute(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint route")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	task := f.fs.String("task", "", "")
	apply := f.fs.Bool("apply", false, "")
	handOver := f.fs.Bool("hand-over", false, "")
	cf := addConsumerFlags(f)
	if !f.parse(args, stderr) {
		return 2
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint route", err.Error())
	}
	defer st.Close()
	ctx := context.Background()
	table, err := cf.table(ctx, st)
	if err != nil {
		return refuse(stderr, " sprint route", err.Error())
	}
	tasks, err := tasksFor(ctx, st, *name, *task)
	if err != nil {
		return refuse(stderr, " sprint route", err.Error())
	}
	sprintName, err := oneSprint(ctx, st, *name)
	if err != nil {
		return refuse(stderr, " sprint route", err.Error())
	}
	all, err := st.Tasks(ctx, sprintName)
	if err != nil {
		return refuse(stderr, " sprint route", err.Error())
	}
	load := laneLoad(all)
	bad := 0
	for _, t := range tasks {
		if !t.Open() {
			continue
		}
		// READY (#2636): an open dependency, or a path a task already working holds, and
		// this task waits -- it is not routed, and it is not an error, so it does not set
		// the exit code. A serial head is never blocked by this: only what depends on it,
		// or what shares its paths, waits behind it.
		if reason := sprint.Blocked(t, all); reason != "" {
			fmt.Fprintf(stdout, "NOT READY %s %s\n", t.ID, reason)
			continue
		}
		if *handOver {
			// The hand-over: the owner stays the required READER and the typing moves.
			// It is the whole of "the router offers the tail of the chain to another
			// consumer with the original owner as the required reader", and it is done
			// only after the owner has said yes -- the coordinator asks, this records.
			if t.Owner != "" {
				load[sprint.LaneOf(t)] -= t.EstMinutes
				t.Reader = t.Owner
				t.Owner = ""
			}
		}
		d, err := sprint.Route(t, table, load, now)
		if err != nil {
			fmt.Fprintf(stdout, "NO ROUTE %s %s\n", t.ID, err)
			bad++
			continue
		}
		fmt.Fprintln(stdout, d.Line())
		if *apply && !d.Split {
			t.Route = d.Route
			t.RouteReason = d.Reason
			t.Reader = d.Reader
			if *handOver {
				t.Owner = d.Consumer
			}
			load[strings.ToLower(d.Consumer)] += t.EstMinutes
			if err := st.PutTask(ctx, t); err != nil {
				return refuse(stderr, " sprint route", err.Error())
			}
		}
	}
	if *apply {
		if code := writeProgress(stderr, "sprint route", ctx, st, sprintName); code != 0 {
			return code
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}

func sprintSplit(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint split")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	task := f.fs.String("task", "", "")
	apply := f.fs.Bool("apply", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*task, "task", "the task id to propose halves for")
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint split", err.Error())
	}
	defer st.Close()
	ctx := context.Background()
	t, err := st.GetTask(ctx, *task)
	if err != nil {
		return refuse(stderr, " sprint split", err.Error())
	}
	split, seams := sprint.SplitFirst(t)
	if !split {
		fmt.Fprintf(stdout, "NO SPLIT %s its paths sit in one package; it stays one task and runs serial\n", t.ID)
		return 0
	}
	children, err := sprint.Split(t, seams)
	if err != nil {
		return refuse(stderr, " sprint split", err.Error())
	}
	for _, c := range children {
		fmt.Fprintf(stdout, "SPLIT %s -> %s paths=%s reader=%s est=%s\n", t.ID, c.ID, strings.Join(c.Paths, ","), dash(c.Reader), sprint.Minutes(c.EstMinutes))
		if *apply {
			c.CreatedAt = now
			if err := st.PutTask(ctx, c); err != nil {
				return refuse(stderr, " sprint split", err.Error())
			}
			if *name != "" {
				if err := st.AddTask(ctx, *name, c.ID); err != nil {
					return refuse(stderr, " sprint split", err.Error())
				}
			}
		}
	}
	if *apply {
		t.State = sprint.StateClosed
		t.Evidence = "split into " + strings.Join(ids(children), ", ")
		t.DoneAt = now
		if err := st.PutTask(ctx, t); err != nil {
			return refuse(stderr, " sprint split", err.Error())
		}
		if code := writeProgress(stderr, "sprint split", ctx, st, *name); code != 0 {
			return code
		}
		if err := sprint.WriteProgressContaining(ctx, st, t.ID); err != nil {
			return refuse(stderr, " sprint split", err.Error())
		}
	}
	return 0
}

func sprintRefill(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint refill")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	lowWater := f.fs.Int("low-water", deal.LowWater, "")
	dry := f.fs.Bool("dry-run", false, "")
	cf := addConsumerFlags(f)
	if !f.parse(args, stderr) {
		return 2
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint refill", err.Error())
	}
	defer st.Close()
	ctx := context.Background()
	table, err := cf.table(ctx, st)
	if err != nil {
		return refuse(stderr, " sprint refill", err.Error())
	}
	sprintName, err := oneSprint(ctx, st, *name)
	if err != nil {
		return refuse(stderr, " sprint refill", err.Error())
	}
	res, err := sprint.Refill(ctx, st, sprintName, table, *lowWater, now, *dry)
	if err != nil {
		return refuse(stderr, " sprint refill", err.Error())
	}
	for _, line := range res.Lines() {
		fmt.Fprintln(stdout, line)
	}
	if !*dry {
		if code := writeProgress(stderr, "sprint refill", ctx, st, sprintName); code != 0 {
			return code
		}
	}
	return 0
}

func sprintWall(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint wall")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	move := f.fs.String("move", "", "")
	to := f.fs.String("to", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint wall", err.Error())
	}
	defer st.Close()
	ctx := context.Background()
	sprintName, err := oneSprint(ctx, st, *name)
	if err != nil {
		return refuse(stderr, " sprint wall", err.Error())
	}
	tasks, err := st.Tasks(ctx, sprintName)
	if err != nil {
		return refuse(stderr, " sprint wall", err.Error())
	}
	w, err := sprint.ComputeWall(tasks)
	if err != nil {
		return refuse(stderr, " sprint wall", err.Error())
	}
	fmt.Fprintf(stdout, "WALL %s work %s critical %s\n", sprint.Minutes(w.Minutes), sprint.Minutes(w.WorkMinutes), dash(w.Critical))
	for _, l := range w.Lanes {
		fmt.Fprintf(stdout, "LANE %s tasks=%d serial=%s finish=%s\n", l.Consumer, len(l.Tasks), sprint.Minutes(l.Minutes), sprint.Minutes(l.Finish))
	}
	for _, t := range w.Splittable {
		fmt.Fprintf(stdout, "SPLITTABLE %s %s paths=%s\n", t.ID, dash(t.Ref), strings.Join(t.Paths, ","))
	}
	if strings.TrimSpace(*move) != "" {
		after, err := sprint.AfterMoving(tasks, splitList(*move), strings.ToLower(*to))
		if err != nil {
			return refuse(stderr, " sprint wall", err.Error())
		}
		fmt.Fprintf(stdout, "MOVING %s -> wall %s (from %s)\n", *move, sprint.Minutes(after.Minutes), sprint.Minutes(w.Minutes))
	}
	return 0
}

func sprintCalibration(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint calibration")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint calibration", err.Error())
	}
	defer st.Close()
	ctx := context.Background()
	names, err := sprintNames(ctx, st, *name)
	if err != nil {
		return refuse(stderr, " sprint calibration", err.Error())
	}
	var all []sprint.Task
	for _, n := range names {
		tasks, err := st.Tasks(ctx, n)
		if err != nil {
			return refuse(stderr, " sprint calibration", err.Error())
		}
		all = append(all, tasks...)
	}
	fmt.Fprint(stdout, sprint.Calibrate(all).String())
	return 0
}

func sprintClose(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint close")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	task := f.fs.String("task", "", "")
	evidence := f.fs.String("evidence", "", "")
	// --at is when the PRIMARY RECORD says it closed (the merge, the close, the typed
	// line), not when this ran: the actual is measured against that, so a task recorded
	// an hour late does not read as an hour of work.
	at := f.fs.String("at", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*name, "name", "the sprint to close, or the sprint the --task belongs to")
	closedAt := now
	if strings.TrimSpace(*at) != "" {
		t, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			f.add(fmt.Sprintf("--at wants an RFC3339 time, got %q", *at))
		} else {
			closedAt = t.UTC()
		}
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint close", err.Error())
	}
	defer st.Close()
	ctx := context.Background()
	if strings.TrimSpace(*task) != "" {
		f.want(*evidence, "evidence", "the primary record that closed it: a merge sha, a landed sha, a typed line id, an issue close")
		if f.refused(stderr) {
			return 2
		}
		t, err := st.GetTask(ctx, *task)
		if err != nil {
			return refuse(stderr, " sprint close", err.Error())
		}
		t.State = sprint.StateClosed
		t.Evidence = *evidence
		t.DoneAt = closedAt
		if !t.LeasedAt.IsZero() && closedAt.After(t.LeasedAt) {
			t.Actual = int(closedAt.Sub(t.LeasedAt).Minutes())
		}
		if err := st.PutTask(ctx, t); err != nil {
			return refuse(stderr, " sprint close", err.Error())
		}
		if code := writeProgress(stderr, "sprint close", ctx, st, *name); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "CLOSED %s %s %s\n", t.ID, t.Ref, t.Evidence)
		return 0
	}
	s, err := st.GetSprint(ctx, *name)
	if err != nil {
		return refuse(stderr, " sprint close", err.Error())
	}
	tasks, err := st.Tasks(ctx, *name)
	if err != nil {
		return refuse(stderr, " sprint close", err.Error())
	}
	c := sprint.Count(tasks)
	s.ClosedAt = closedAt
	if err := st.PutSprint(ctx, s); err != nil {
		return refuse(stderr, " sprint close", err.Error())
	}
	if code := writeProgress(stderr, "sprint close", ctx, st, s.Name); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "SPRINT CLOSED %s %d/%d %d%%\n", s.Name, c.Closed, c.Total, sprint.Percent(c.Closed, c.Total))
	return 0
}

// writeProgress rewrites sprint:<name> {done, units, percent, eta_minutes} after
// a task state change. done, units and percent stay the last SET OK / SET DONE
// evaluation; eta is the wall. An older read does not land after a newer one.
func writeProgress(stderr io.Writer, verb string, ctx context.Context, st sprint.Store, name string) int {
	if strings.TrimSpace(name) == "" {
		return 0
	}
	if err := sprint.WriteProgress(ctx, st, name); err != nil {
		return refuse(stderr, " "+verb, err.Error())
	}
	return 0
}

// consumerFlags are how a verb is handed the consumers table: the machines registry for the
// benches (capabilities in the row's notes) and the bus roster or a name list for the
// friends. It is NOT a new file, and there is no flag for a queue name anywhere.
type consumerFlags struct {
	machines *string
	bus      *string
	friends  *string
}

func addConsumerFlags(f *flags) consumerFlags {
	return consumerFlags{
		machines: f.fs.String("machines", "", ""),
		bus:      f.fs.String("bus", "", ""),
		friends:  f.fs.String("friends", "", ""),
	}
}

func (c consumerFlags) table(ctx context.Context, st sprint.Store) ([]deal.Capabilities, error) {
	table, err := deal.LoadTable(*c.machines, *c.bus, splitList(*c.friends))
	if err != nil {
		return nil, err
	}
	present, err := st.Presence(ctx)
	if err != nil {
		return nil, err
	}
	return deal.ApplyPresence(table, present), nil
}

func activeSprints(ctx context.Context, st sprint.Store, name string) ([]sprint.Sprint, error) {
	if strings.TrimSpace(name) != "" {
		s, err := st.GetSprint(ctx, name)
		if err != nil {
			return nil, err
		}
		return []sprint.Sprint{s}, nil
	}
	all, err := st.Sprints(ctx)
	if err != nil {
		return nil, err
	}
	var out []sprint.Sprint
	for _, s := range all {
		if s.Active() {
			out = append(out, s)
		}
	}
	return out, nil
}

func oneSprint(ctx context.Context, st sprint.Store, name string) (string, error) {
	active, err := activeSprints(ctx, st, name)
	if err != nil {
		return "", err
	}
	switch len(active) {
	case 0:
		return "", fmt.Errorf("no sprint is open; run: nova-pulse sprint open --name <id> --goal <one sentence>")
	case 1:
		return active[0].Name, nil
	}
	var names []string
	for _, s := range active {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return "", fmt.Errorf("%d sprints are open (%s); name one with --name", len(active), strings.Join(names, ", "))
}

func sprintNames(ctx context.Context, st sprint.Store, name string) ([]string, error) {
	if strings.TrimSpace(name) != "" {
		return []string{name}, nil
	}
	all, err := st.Sprints(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range all {
		out = append(out, s.Name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the store holds no sprint")
	}
	return out, nil
}

func tasksFor(ctx context.Context, st sprint.Store, name, task string) ([]sprint.Task, error) {
	if strings.TrimSpace(task) != "" {
		t, err := st.GetTask(ctx, task)
		if err != nil {
			return nil, err
		}
		return []sprint.Task{t}, nil
	}
	sprintName, err := oneSprint(ctx, st, name)
	if err != nil {
		return nil, err
	}
	return st.Tasks(ctx, sprintName)
}

// taskIDFrom makes a task id out of a ref when the caller gives none: `owner/name#123`
// becomes `name-123`, a card label stays itself. The id is what the store keys on, so it
// must not carry the one-line grammar's separator.
func taskIDFrom(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.ReplaceAll(ref, ": ", "-")
	if i := strings.LastIndex(ref, "#"); i > 0 {
		repo := ref[:i]
		if j := strings.LastIndex(repo, "/"); j >= 0 {
			repo = repo[j+1:]
		}
		return strings.ToLower(repo + "-" + strings.TrimPrefix(ref[i+1:], "#"))
	}
	return strings.ToLower(strings.ReplaceAll(ref, " ", "-"))
}

// laneLoad is how many open minutes each consumer already holds. It is what the router
// balances against, so a hand-over lands on the emptiest lane that can take it and the wall
// actually gets shorter.
func laneLoad(tasks []sprint.Task) map[string]int {
	load := map[string]int{}
	for _, t := range tasks {
		if !t.Open() {
			continue
		}
		load[sprint.LaneOf(t)] += t.EstMinutes
	}
	return load
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ids(tasks []sprint.Task) []string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
