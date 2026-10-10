package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
)

// watch --wake: the coordinator's wake as a verb (card coordinator-wake-verb;
// the model is tla/CoordinatorWake.tla). It blocks until the first of what the
// coordinator would otherwise have to look at by hand, prints one line
//
//	WAKE <kind> <time> <evidence>
//
// and exits 0; the session runs it again. The kinds, in the order a look
// decides between them when more than one is due:
//
//	bus       a message to the coordinator that asks or reports (a request, a
//	          blocker or a report; a message with no kind is told by its
//	          subject: no ping, pong, dealt, finished, landed, width, RESULT or
//	          HOLD notice), never the coordinator's own
//	judgment  a judgment new since the last judgment wake, at most one wake in
//	          --judgment-every
//	stop      the machine STOPPED by anyone but the coordinator's stop verb,
//	          once per stop
//	friend    a friend down by herself (not held on purpose) at two looks in a
//	          row, once per time she goes down
//	merge     merging over --merge-over, or merging with no land pass for
//	          --land-after; at most one wake in --merge-every; never after a
//	          stop the coordinator asked
//	backlog   the fleet working under half its up width, review over
//	          --review-over, merging over --merging-over, or no card ready with
//	          cards waiting; at most one wake in --backlog-every; never after a
//	          stop the coordinator asked
//	check     --check since the last wake
//
// The state file keeps what makes a run after another lose and repeat nothing:
// the bus entry id the last run consumed, the judgments seen, the stop woken,
// how many looks each friend has been down, and the time of the last wake of
// each kind. It is written, whole and in place, before the line is printed, so
// a wake is never printed for a cursor not kept (CoordinatorWake.tla,
// AtMostOnce), and an event that arrives between two runs is found by the
// second (NoLapse). The first run, with no file, starts from now: what is
// already there is not new.

func init() { notServed = append(notServed, "watch") }

// wakeKinds are the kinds, so the help and the evidence name the same words.
const (
	wakeBus      = "bus"
	wakeJudgment = "judgment"
	wakeStop     = "stop"
	wakeFriend   = "friend"
	wakeMerge    = "merge"
	wakeBacklog  = "backlog"
	wakeCheck    = "check"
)

// wakeFailLimit is how many looks in a row may fail before the verb says so and ends.
const wakeFailLimit = 5

// wakeCfg is what a wake is told: how often it looks and the thresholds, the
// defaults the hand script had.
type wakeCfg struct {
	every         time.Duration // between two looks
	check         time.Duration // since the last wake of any kind
	judgmentEvery time.Duration
	mergeEvery    time.Duration
	backlogEvery  time.Duration
	landAfter     time.Duration
	mergeOver     int // merging over this wakes
	mergingOver   int // merging over this is a backlog
	reviewOver    int
	sleep         func(time.Duration)
}

func defaultWakeCfg() wakeCfg {
	return wakeCfg{every: 20 * time.Second, check: 10 * time.Minute, judgmentEvery: 20 * time.Minute, mergeEvery: 10 * time.Minute,
		backlogEvery: 30 * time.Minute, landAfter: 15 * time.Minute, mergeOver: 30, mergingOver: 60, reviewOver: 40, sleep: time.Sleep}
}

// wakeMsg is one entry of the coordinator's bus stream.
type wakeMsg struct{ ID, From, Subject, Kind string }

// wakeLook is what one look at the world reads.
type wakeLook struct {
	Coordinator string
	Msgs        []wakeMsg // the stream's entries after the cursor, oldest first
	Judgments   []string  // the open judgments' notes
	Stopped     bool      // the machine reads STOPPED (not DONE)
	StopAsked   bool      // and the coordinator's stop verb stopped it
	StopKey     string    // tells one stop from another
	StopLine    string
	// the work and fleet tables' totals; Working and Width are the fleet members that are up
	Merging, Review, Waiting, Ready int
	Working, Width                  int
	Down                            []string  // friends down by themselves, not held on purpose
	LastLand                        time.Time // the last land pass, zero for none
}

// wakeSource is where a wake looks: the store and the bus, or a fake. after is
// the bus entry id the cursor holds; the look carries the entries past it.
type wakeSource interface {
	look(ctx context.Context, after string) (wakeLook, error)
}

// wakeState is the state file.
type wakeState struct {
	Seeded    bool                 `json:"seeded"`
	Bus       string               `json:"bus"`
	Judgments []string             `json:"judgments"`
	Stop      string               `json:"stop"`
	Down      map[string]int       `json:"down"`
	Told      []string             `json:"told"` // friends woken for the time they have been down
	Last      map[string]time.Time `json:"last"`
}

// wakeLine is one wake: its kind, when, and what it saw.
type wakeLine struct {
	Kind     string
	At       time.Time
	Evidence string
}

func (w wakeLine) String() string {
	return "WAKE " + w.Kind + " " + w.At.Format(time.RFC3339) + " " + oneline.Escape(w.Evidence)
}

// readWakeState reads the state file; none is a first run.
func readWakeState(path string) (wakeState, error) {
	var s wakeState
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("the state file cannot be read: %w", err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("the state file %s is not a wake state (%v); remove it and the next run starts from now", path, err)
	}
	return s, nil
}

// writeWakeState writes the state whole, in place.
func writeWakeState(path string, s wakeState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("the state file cannot be written: %w", err)
	}
	return nil
}

// notAnAsk is the subjects of a message with no kind that need no answer: what
// the hand script filtered.
var notAnAsk = regexp.MustCompile(`(?i)pong|ping|^card \S+ dealt|card \S+ finished|\bland\b|landed|^done|^width \d|e2e-ok|^awake|^result:| hold$|rule applied|^ack|acknowledged`)

// wakes says the message is one the coordinator is woken by: by its kind when
// it has one, by its subject when it has none; never the coordinator's own.
func (m wakeMsg) wakes(coordinator string) bool {
	switch {
	case m.From == coordinator:
		return false
	case m.Kind != "":
		return m.Kind == bus.KindRequest || m.Kind == bus.KindBlocker || m.Kind == bus.KindReport
	}
	return !notAnAsk.MatchString(m.Subject)
}

// step is one look decided against the state: the wake it is due, if any, with
// the state moved as that wake moves it; a look that wakes nothing moves only
// what a look moves (the bus cursor past what is filtered, the friends' counts).
// The kinds are tried in the order the header lists them, and a kind not chosen
// keeps its cursor, so it wakes a later run (tla/CoordinatorWake.tla, NoLapse).
func (s *wakeState) step(cfg wakeCfg, l wakeLook, now time.Time) (wakeLine, bool) {
	if s.Last == nil {
		s.Last = map[string]time.Time{}
	}
	if s.Down == nil {
		s.Down = map[string]int{}
	}
	woke := func(kind, evidence string) (wakeLine, bool) {
		s.Last[kind], s.Last[wakeCheck] = now, now
		return wakeLine{Kind: kind, At: now, Evidence: evidence}, true
	}
	due := func(kind string, every time.Duration) bool { return now.Sub(s.Last[kind]) >= every }

	// the bus: the cursor passes what never wakes; a message that wakes is consumed with its wake
	var asks []wakeMsg
	for _, m := range l.Msgs {
		if m.wakes(l.Coordinator) {
			asks = append(asks, m)
		}
	}
	busEnd := s.Bus
	if len(l.Msgs) > 0 {
		busEnd = l.Msgs[len(l.Msgs)-1].ID
	}
	if len(asks) == 0 {
		s.Bus = busEnd
	}

	// the friends' looks: a count of looks down in a row, kept while she is down
	down := map[string]bool{}
	for _, n := range l.Down {
		down[n] = true
		s.Down[n]++
	}
	for n := range s.Down {
		if !down[n] {
			delete(s.Down, n)
		}
	}
	s.Told = slices.DeleteFunc(s.Told, func(n string) bool { return !down[n] })

	// the judgments: the ones seen are the ones still open
	seen := map[string]bool{}
	for _, j := range s.Judgments {
		seen[j] = true
	}
	var fresh []string
	for _, j := range l.Judgments {
		if !seen[j] {
			fresh = append(fresh, j)
		}
	}
	s.Judgments = slices.DeleteFunc(s.Judgments, func(j string) bool { return !slices.Contains(l.Judgments, j) })

	if !l.Stopped {
		s.Stop = ""
	}

	if len(asks) > 0 {
		s.Bus = asks[len(asks)-1].ID
		var ev []string
		for _, m := range asks[:min(len(asks), 3)] {
			ev = append(ev, m.From+": "+m.Subject)
		}
		if len(asks) > 3 {
			ev = append(ev, fmt.Sprintf("and %d more", len(asks)-3))
		}
		return woke(wakeBus, strings.Join(ev, "; "))
	}
	if len(fresh) > 0 && due(wakeJudgment, cfg.judgmentEvery) {
		s.Judgments = slices.Clone(l.Judgments)
		return woke(wakeJudgment, fmt.Sprintf("%d new: %s", len(fresh), strings.Join(fresh, ", ")))
	}
	if l.Stopped && !l.StopAsked && s.Stop != l.StopKey {
		s.Stop = l.StopKey
		return woke(wakeStop, fmt.Sprintf("the machine reads %q and the coordinator did not ask for a stop", l.StopLine))
	}
	var gone []string
	for _, n := range slices.Sorted(mapKeys(s.Down)) {
		if s.Down[n] >= 2 && !slices.Contains(s.Told, n) {
			gone = append(gone, n)
		}
	}
	if len(gone) > 0 {
		s.Told = append(s.Told, gone...)
		return woke(wakeFriend, "down: "+strings.Join(gone, ","))
	}
	if !l.StopAsked {
		if ago := now.Sub(l.LastLand); l.Merging > cfg.mergeOver || (l.Merging > 0 && !l.LastLand.IsZero() && ago > cfg.landAfter) {
			if due(wakeMerge, cfg.mergeEvery) {
				ev := fmt.Sprintf("merging %d", l.Merging)
				if !l.LastLand.IsZero() {
					ev += fmt.Sprintf(", last land pass %ds ago", int(ago.Seconds()))
				}
				return woke(wakeMerge, ev)
			}
		}
		var why []string
		if l.Width > 0 && l.Working*2 < l.Width {
			why = append(why, fmt.Sprintf("fleet %d/%d", l.Working, l.Width))
		}
		if l.Review > cfg.reviewOver {
			why = append(why, fmt.Sprintf("review %d", l.Review))
		}
		if l.Merging > cfg.mergingOver {
			why = append(why, fmt.Sprintf("merging %d", l.Merging))
		}
		if l.Ready == 0 && l.Waiting > 0 {
			why = append(why, fmt.Sprintf("ready 0 with %d waiting", l.Waiting))
		}
		if len(why) > 0 && due(wakeBacklog, cfg.backlogEvery) {
			return woke(wakeBacklog, strings.Join(why, ", "))
		}
	}
	if due(wakeCheck, cfg.check) {
		return woke(wakeCheck, "the check, "+cfg.check.String()+" since the last wake")
	}
	return wakeLine{}, false
}

// mapKeys is the keys of m as a sequence.
func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// errWakeInterrupted is a wake ended by an interrupt before anything woke it.
var errWakeInterrupted = errors.New("interrupted before anything woke it; the state file keeps its cursors")

// runWake is the verb: it looks every cfg.every until a look is due a wake, keeps
// the state file as that wake moves it, and gives the wake (the caller prints
// it, after the file is written: tla/CoordinatorWake.tla, Wake).
func runWake(ctx context.Context, cfg wakeCfg, src wakeSource, path string, now func() time.Time) (wakeLine, error) {
	st, err := readWakeState(path)
	if err != nil {
		return wakeLine{}, err
	}
	if !st.Seeded {
		st.Bus = bus.IDAt(now()) // a first run starts from now
	}
	kept, _ := json.Marshal(st)
	failed := 0
	for {
		if ctx.Err() != nil {
			return wakeLine{}, errWakeInterrupted
		}
		l, err := src.look(ctx, st.Bus)
		switch {
		case err != nil && ctx.Err() != nil:
			return wakeLine{}, errWakeInterrupted
		case err != nil:
			if failed++; failed >= wakeFailLimit {
				return wakeLine{}, fmt.Errorf("the sprint could not be read at %d looks in a row, the last: %w", failed, err)
			}
			cfg.sleep(cfg.every)
			continue
		}
		failed = 0
		at := now()
		if !st.Seeded {
			// what is open now is not new, and the check counts from now
			st.Seeded, st.Judgments = true, slices.Clone(l.Judgments)
			st.Last = map[string]time.Time{wakeCheck: at}
		}
		w, ok := st.step(cfg, l, at)
		if now, _ := json.Marshal(st); ok || string(now) != string(kept) {
			if err := writeWakeState(path, st); err != nil {
				return wakeLine{}, err
			}
			kept = now
		}
		if ok {
			return w, nil
		}
		cfg.sleep(cfg.every)
	}
}

// storeWake is the wake's source on the store and the bus.
type storeWake struct {
	st     *store.Store
	a      *app
	stream string // the coordinator's stream; "" with no bus
	bus    *bus.Bus
}

// busBatch bounds one read of the stream.
const busBatch = 100

func (w *storeWake) look(ctx context.Context, after string) (wakeLook, error) {
	var l wakeLook
	v, _, err := w.a.where(ctx, w.st, defaultStale, false)
	if err != nil {
		return l, err
	}
	l.Coordinator = v.Coordinator
	l.Ready = int(v.Ready)
	for _, r := range v.Tables[sprint.Work] {
		l.Review += cell(r, string(sprint.Review))
		l.Merging += cell(r, string(sprint.Merging))
		l.Waiting += cell(r, string(sprint.Waiting))
	}
	for _, r := range v.Tables[sprint.Fleet] {
		if cellText(r[sprint.Status]) != sprint.Up {
			continue
		}
		l.Working += cell(r, string(sprint.Working))
		if n, err := sprint.ParseWidth(cellText(r[sprint.FieldWidth])); err == nil {
			l.Width += n
		} else {
			l.Width += sprint.DefaultWidth
		}
	}
	for _, f := range v.Friends {
		if f.Status == sprint.Down { // held on purpose is sprint.Held, a status of its own
			l.Down = append(l.Down, f.Name)
		}
	}
	m, _, err := w.st.Machine(ctx)
	if err != nil {
		return l, err
	}
	l.StopLine = v.Machine
	l.Stopped = strings.HasPrefix(v.Machine, "machine: STOPPED")
	l.StopAsked = !m.Running() && m.Cause == "" && m.Who != "" && m.Who == v.Coordinator
	l.StopKey = m.Since.UTC().Format(time.RFC3339Nano) + " " + m.State
	in, err := w.st.Inbox(ctx, defaultDeadline, defaultStale, 10000)
	if err != nil {
		return l, err
	}
	for _, g := range in.Groups {
		if g.Kind == sprint.Judgment {
			l.Judgments = append(l.Judgments, noteKeys(g)...)
		}
	}
	facts, err := w.st.WhereFacts(ctx, 0)
	if err != nil {
		return l, err
	}
	for _, t := range facts.Landed {
		if t.After(l.LastLand) {
			l.LastLand = t
		}
	}
	if w.bus != nil && w.stream != "" {
		es, err := w.bus.Store.Range(ctx, w.stream, "("+after, "+", busBatch)
		if err != nil {
			return l, err
		}
		for _, e := range es {
			f := e.Fields
			l.Msgs = append(l.Msgs, wakeMsg{ID: e.Entry, From: f["from"], Subject: f["subject"], Kind: f["kind"]})
		}
	}
	return l, nil
}

// cell is a table cell as a count; one that is no number counts none.
func cell(row map[string]any, col string) int {
	n, err := strconv.Atoi(strings.TrimSpace(cellText(row[col])))
	if err != nil {
		return 0
	}
	return n
}

// wakeStatePath is the state file --state names, or the one kept for the
// store under the user's cache directory (one file a store).
func (a *app) wakeStatePath(given, redis string) (string, error) {
	if given != "" {
		return given, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no --state given and no cache directory to keep one in (%v)", err)
	}
	sum := sha256.Sum256([]byte(redis))
	if err := os.MkdirAll(filepath.Join(dir, prog), 0o700); err != nil {
		return "", fmt.Errorf("the state directory cannot be made: %w", err)
	}
	return filepath.Join(dir, prog, fmt.Sprintf("wake-%x.json", sum[:6])), nil
}

// openWakeBus dials the bus as friend sync does (NOVA_BUS_REDIS); nil with no address.
func (a *app) openWakeBus(ctx context.Context) (*bus.Bus, io.Closer, error) {
	addr := a.getenv(busRedisEnv)
	if addr == "" {
		return nil, nil, nil
	}
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if a.getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if a.getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	conn, err := redisconn.Open(ctx, o, a.getenv)
	if err != nil {
		return nil, nil, err
	}
	return &bus.Bus{Store: bus.Redis{C: conn.Client()}}, conn, nil
}

// cmdWatch is watch --wake (the header above).
func (a *app) cmdWatch(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("watch")
	d := defaultWakeCfg()
	wake := fs.Bool("wake", false, "block until the first thing that wakes the coordinator, print WAKE <kind> <time> <evidence> and exit 0 (kinds: bus, judgment, stop, friend, merge, backlog, check)")
	state := fs.String("state", "", "the file that keeps the cursors between runs, so no event is missed or woken twice (default: one a store under the user cache directory); the first run starts from now")
	every := fs.Duration("every", d.every, "how often the sprint and the bus are looked at, above 0")
	check := fs.Duration("check", d.check, "wake with a check this long after the last wake")
	judgmentEvery := fs.Duration("judgment-every", d.judgmentEvery, "at most one judgment wake in this long")
	mergeEvery := fs.Duration("merge-every", d.mergeEvery, "at most one merge wake in this long")
	backlogEvery := fs.Duration("backlog-every", d.backlogEvery, "at most one backlog wake in this long")
	landAfter := fs.Duration("land-after", d.landAfter, "merging with no land pass for this long is a merge wake")
	mergeOver := fs.Int("merge-over", d.mergeOver, "merging over this many is a merge wake")
	mergingOver := fs.Int("merging-over", d.mergingOver, "merging over this many is a backlog wake")
	reviewOver := fs.Int("review-over", d.reviewOver, "review over this many is a backlog wake")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "watch", argErr("takes no words ", err, pos...))
	}
	if !*wake {
		return refuse(stderr, "watch", "--wake is the one thing watch does: it waits for what wakes the coordinator; run: nova-sprint watch --wake")
	}
	for name, v := range map[string]time.Duration{"every": *every, "check": *check, "judgment-every": *judgmentEvery, "merge-every": *mergeEvery, "backlog-every": *backlogEvery, "land-after": *landAfter} {
		if v <= 0 {
			return refuse(stderr, "watch", "--"+name+" wants a duration above 0, got "+v.String())
		}
	}
	ctx, stop := a.notify(context.Background())
	defer stop()
	st, err := a.storeCtx(ctx, *c)
	if err != nil {
		return refuse(stderr, "watch", err.Error())
	}
	path, err := a.wakeStatePath(*state, c.redis)
	if err != nil {
		return refuse(stderr, "watch", err.Error())
	}
	b, closer, err := a.openWakeBus(ctx)
	if err != nil {
		return refuse(stderr, "watch", "the bus cannot be dialed: "+err.Error()+"; unset "+busRedisEnv+" to wake without it")
	}
	if closer != nil {
		defer closer.Close() // ignored: the connection ends with the verb; a failed close has no one to tell
	} else {
		fmt.Fprintf(stderr, "%s watch: NOTE %s is not set: bus messages do not wake\n", prog, busRedisEnv)
	}
	coord, err := st.B.Coordinator(ctx)
	if err != nil {
		return a.readFailed("watch", err, stderr)
	}
	src := &storeWake{st: st, a: a, bus: b}
	if coord != "" {
		src.stream = bus.StreamOf(coord)
	}
	cfg := wakeCfg{every: *every, check: *check, judgmentEvery: *judgmentEvery, mergeEvery: *mergeEvery, backlogEvery: *backlogEvery,
		landAfter: *landAfter, mergeOver: *mergeOver, mergingOver: *mergingOver, reviewOver: *reviewOver, sleep: func(d time.Duration) { a.pause(ctx, d) }}
	w, err := runWake(ctx, cfg, src, path, a.now)
	if err != nil {
		fmt.Fprintf(stderr, "%s watch: %s\n", prog, oneline.WithRemedy(err.Error(), prog+" watch --wake --state <file>: it starts again from its cursors"))
		if errors.Is(err, errWakeInterrupted) {
			return 1
		}
		return 2
	}
	fmt.Fprintln(stdout, w.String())
	return 0
}
