package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// The two functions of internal/nsprint/fn/lua/friend_here.lua.
const (
	fnHere     = "ns_friend_here"
	fnHereBeat = "ns_friend_here_beat"
)

// staleAfter is how old another session's beat may be before here takes it
// over instead of refusing BUSY: the consumer table's own minute.
const staleAfter = 60 * time.Second

// hereFailLimit is how many beats in a row may fail before the loop says
// bye and exits 3.
const hereFailLimit = 5

// byeGrace bounds the bye a stopping loop writes.
const byeGrace = 2 * time.Second

var (
	// errFenced: another session holds the beat; this loop is no longer the
	// presence and says no bye.
	errFenced = errors.New("FENCED")
	// errUnregistered: the roster no longer holds the name.
	errUnregistered = errors.New("UNREGISTERED")
)

// presence is one here session.
type presence struct {
	Name, Host, Harness, Session, Sprint string
	// PID is the harness process the friend's copies are bound to (0: none;
	// the leases wait for a binding).
	PID int
	// Logins are the friend's login aliases (--login), written to
	// friends:login at registration.
	Logins []string
}

// loginFlags is the repeatable `here --login <alias>`.
type loginFlags []string

func (l *loginFlags) String() string { return strings.Join(*l, ",") }

func (l *loginFlags) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// hereSteps are the calls of one here tick, in order: the beat, the leases
// (the binding, then the observed owners), the wake poll, the take, and the
// bye at the end. Production binds them to the store (liveHereSteps); a
// test injects them and observes the order.
type hereSteps struct {
	beat   func(context.Context) error
	leases func(context.Context) ([]string, error)
	poll   func(context.Context) (string, error)
	take   func(context.Context) ([]task.Claim, error)
	bye    func(context.Context) error
}

// hereEnd says whether a tick ended the loop, and whether the loop still
// owns the beat to say bye.
type hereEnd int

const (
	keepGoing hereEnd = iota
	endWithBye
	endWithoutBye
)

func runHere(ctx context.Context, e env, args []string, out, errOut io.Writer) int {
	const verb = "here"
	fs := newFlags(verb)
	redisAddr := fs.String("redis", "", redisHelp)
	as := fs.String("as", "", asHelp)
	harness := fs.String("harness", "", "the harness this session runs in, on your beat (default nova-friend)")
	host := fs.String("host", "", "the host this session is on, on your beat (default the hostname)")
	session := fs.String("session", "", "this session's identity (default a fresh one)")
	sprint := fs.String("sprint", "", "the sprint whose queued work the take claims (default every open sprint)")
	pid := fs.Int("pid", 0, "your harness's live pid: every copy you hold is bound to it, so its lease renews while it lives (default none)")
	var logins loginFlags
	fs.Var(&logins, "login", "a login alias of yours (a forge account), repeatable; written to friends:login")
	once := fs.Bool("once", false, "register, tick once and return, leaving you up")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	name, err := e.actor(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *host == "" {
		if *host, err = os.Hostname(); err != nil {
			return refuse(errOut, verb, "--host wants the host this session is on (no hostname: "+err.Error()+")")
		}
	}
	if strings.ContainsAny(*host, " \t\r\n:") {
		return refuse(errOut, verb, "--host wants one word without a colon")
	}
	if *session == "" {
		if *session, err = newSession(); err != nil {
			return refuse(errOut, verb, err.Error())
		}
	}
	var owner taskcard.ProcessOwner
	if *pid != 0 {
		if *pid < 0 || *pid == os.Getpid() {
			return refuse(errOut, verb, "--pid wants your harness's live pid, not this process")
		}
		observed := life.ProbeProcess(*pid)
		if observed.Err != nil || observed.Absent || observed.Start == "" {
			return refuse(errOut, verb, fmt.Sprintf("--pid %d is not a live process this session can see (%v)", *pid, observed.Err))
		}
		observer, err := os.Hostname()
		if err != nil {
			return refuse(errOut, verb, "no hostname to bind the owner on: "+err.Error())
		}
		owner = taskcard.ProcessOwner{Host: observer, PID: *pid, Start: observed.Start}
	}
	if *harness == "" {
		*harness = defaultHarness
	}
	p := presence{Name: name, Host: *host, Harness: *harness, Session: *session, Sprint: *sprint, PID: *pid, Logins: logins}
	st, err := openLoopStore(ctx, e.redis(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()

	up, err := hereRegister(ctx, st, p)
	if err != nil {
		var r refusal
		if errors.As(err, &r) {
			return refused(errOut, verb, r.why)
		}
		return refuse(errOut, verb, err.Error())
	}
	if up.TookOver {
		fmt.Fprintf(errOut, "friend %s here: took over a stale session's beat\n", name)
	}
	claims, err := task.TakeAvailable(ctx, st, name, *sprint, "", 0, name, "")
	if err != nil {
		fmt.Fprintf(errOut, "friend %s take: %v\n", name, err)
	}
	fmt.Fprintf(out, "FRIEND HERE as=%s host=%s session=%s slots=%d taken=%d\n", name, *host, *session, up.Slots, len(claims))
	printClaims(out, claims)

	steps := liveHereSteps(st, p, owner)
	fails := 0
	if *once {
		code, end := hereCycle(ctx, p, steps, &fails, out, errOut)
		if end == endWithBye {
			return hereDown(ctx, p, steps, code, out, errOut)
		}
		return code
	}
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(life.BeatInterval)
	defer ticker.Stop()
	return hereLoop(signalCtx, ticker.C, p, steps, out, errOut)
}

// refusal is the store saying no to a well-formed here (UNREGISTERED, BUSY,
// NAME-IS-LOGIN): exit 1, its why on the refusal line.
type refusal struct{ why string }

func (r refusal) Error() string { return r.why }

// hereUp is what ns_friend_here answered.
type hereUp struct {
	Slots    int
	WasUp    bool
	TookOver bool
}

// hereRegister is the one call that registers a session's presence.
func hereRegister(ctx context.Context, st *store.Store, p presence) (hereUp, error) {
	fargs := []any{p.Name, p.Host, p.Harness, p.Session, strconv.FormatInt(staleAfter.Milliseconds(), 10), p.Name, ""}
	for _, alias := range p.Logins {
		fargs = append(fargs, alias)
	}
	reply, err := st.Client().FCall(ctx, fnHere, nil, fargs...).Result()
	if err != nil {
		return hereUp{}, fmt.Errorf("register %s: %w", p.Name, err)
	}
	return parseHereReply(p.Name, words(reply))
}

// parseHereReply reads ns_friend_here's answer: UP <slots> <was_up>
// <took_over>, or a refusal with its remedy.
func parseHereReply(name string, w []string) (hereUp, error) {
	switch word(w, 0) {
	case "UP":
		slots, err := strconv.Atoi(word(w, 1))
		if err != nil {
			return hereUp{}, fmt.Errorf("register %s: slots %q: %w", name, word(w, 1), err)
		}
		return hereUp{Slots: slots, WasUp: word(w, 2) == "1", TookOver: word(w, 3) == "1"}, nil
	case "UNREGISTERED":
		return hereUp{}, refusal{unregistered(name)}
	case "NAME-IS-LOGIN":
		return hereUp{}, refusal{fmt.Sprintf("NAME-IS-LOGIN %s: that name is a login alias of a friend, not a friend; run: nova-friend here --as <the friend>", name)}
	case "BUSY":
		return hereUp{}, refusal{fmt.Sprintf("BUSY %s: a live session is here already on %s (session %s, beat %s ago); stop it, or wait a minute and it is taken over", name, dash(word(w, 1)), dash(word(w, 2)), ageText(word(w, 3)))}
	case "INVALID":
		if alias := word(w, 1); alias != "" {
			return hereUp{}, refusal{fmt.Sprintf("--login %s wants letters, digits and dashes", alias)}
		}
	case "LOGIN-IS-FRIEND":
		return hereUp{}, refusal{fmt.Sprintf("LOGIN-IS-FRIEND %s: --login %s is a friend's name, not a login alias", name, word(w, 1))}
	case "LOGIN-TAKEN":
		return hereUp{}, refusal{fmt.Sprintf("LOGIN-TAKEN %s: --login %s is %s's login already", name, word(w, 1), dash(word(w, 2)))}
	case "":
		return hereUp{}, fmt.Errorf("register %s: empty reply", name)
	}
	return hereUp{}, fmt.Errorf("register %s: %s", name, strings.Join(w, " "))
}

// ageText spells a millisecond count as a duration, or "?".
func ageText(ms string) string {
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return "?"
	}
	return (time.Duration(n) * time.Millisecond).Truncate(time.Second).String()
}

// defaultHarness is the beat's harness when the friend names none: the
// presence process itself.
const defaultHarness = "nova-friend"

// liveHereSteps binds the tick to the store.
func liveHereSteps(st *store.Store, p presence, owner taskcard.ProcessOwner) hereSteps {
	c := st.Client()
	if p.Harness == "" {
		p.Harness = defaultHarness
	}
	as := taskcard.Consumer{Kind: "friend", Name: p.Name}
	report := &ownerReport{states: map[string]string{}, refusals: map[string]string{}}
	return hereSteps{
		beat: func(ctx context.Context) error {
			reply, err := c.FCall(ctx, fnHereBeat, nil, p.Name, p.Session, p.Host, p.Harness,
				life.Load1Now(), strconv.Itoa(runtime.NumCPU()), life.CPUBusyNow()).Result()
			if err != nil {
				return err
			}
			w := words(reply)
			switch word(w, 0) {
			case "OK":
				return nil
			case "FENCED":
				return fmt.Errorf("%w: session %s on %s holds the beat", errFenced, dash(word(w, 1)), dash(word(w, 2)))
			case "UNREGISTERED":
				return fmt.Errorf("%w: %s left the roster", errUnregistered, p.Name)
			}
			return fmt.Errorf("beat: %s", strings.Join(w, " "))
		},
		leases: func(ctx context.Context) ([]string, error) {
			var lines []string
			if owner.PID > 0 {
				bound, err := bindOwners(ctx, c, as, owner)
				lines = append(lines, bound...)
				if err != nil {
					return lines, err
				}
			}
			res, err := life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: p.Name, Host: p.Host, Harness: p.Harness,
				Load1: life.Load1Now(), NCPU: runtime.NumCPU(), CPU: life.CPUBusyNow(), At: time.Now()})
			if err != nil {
				return lines, err
			}
			return append(lines, report.lines(p.Name, res)...), nil
		},
		poll: func(ctx context.Context) (string, error) {
			return life.PollWake(ctx, st, p.Name)
		},
		take: func(ctx context.Context) ([]task.Claim, error) {
			return task.TakeAvailable(ctx, st, p.Name, p.Sprint, "", 0, p.Name, "")
		},
		bye: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), byeGrace)
			defer cancel()
			return byeSession(ctx, c, p.Name, p.Session)
		},
	}
}

// bindOwners binds every working copy of as that is not yet bound to its
// current token to owner (taskcard.BindOwner, the library nova-sprint card
// owner calls): one line per binding, a refusal per copy on its own line.
func bindOwners(ctx context.Context, c redis.Cmdable, as taskcard.Consumer, owner taskcard.ProcessOwner) ([]string, error) {
	ids, err := ws.IDs(ws.CellRange(ctx, c, as.String(), "working"))
	if err != nil {
		return nil, fmt.Errorf("read working: %w", err)
	}
	var copies []string
	for _, id := range ids {
		if taskcard.IsCopy(id) {
			copies = append(copies, id)
		}
	}
	if len(copies) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	recs := make([]*redis.SliceCmd, len(copies))
	for i, id := range copies {
		recs[i] = pipe.HMGet(ctx, taskcard.Key(id), "token", "owner_token")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read copies: %w", err)
	}
	var lines []string
	for i, id := range copies {
		v := recs[i].Val()
		token, _ := v[0].(string)
		bound, _ := v[1].(string)
		if token == "" || bound == token {
			continue
		}
		if err := taskcard.BindOwner(ctx, c, as, id, token, owner); err != nil {
			if why, ok := taskcard.IsRefused(err); ok {
				lines = append(lines, fmt.Sprintf("FRIEND HERE OWNER id=%s state=refused as=%s pid=%d why=%s", id, as.Name, owner.PID, quoteField(why)))
				continue
			}
			return lines, fmt.Errorf("bind %s: %w", id, err)
		}
		lines = append(lines, fmt.Sprintf("FRIEND HERE OWNER id=%s state=bound as=%s pid=%d", id, as.Name, owner.PID))
	}
	return lines, nil
}

// ownerReport turns one lease pass into the lines worth printing: a copy's
// owner state when it changes (dead, unknown, a store refusal), once, never
// every tick.
type ownerReport struct {
	states, refusals map[string]string
}

func (r *ownerReport) lines(name string, res life.FriendBeatResult) []string {
	var lines []string
	current := map[string]string{}
	refused := map[string]bool{}
	for _, x := range res.Refused {
		refused[x.ID] = true
		if r.refusals[x.ID] != x.Why {
			lines = append(lines, fmt.Sprintf("FRIEND HERE OWNER id=%s state=refused as=%s why=%s", x.ID, name, quoteField(x.Why)))
		}
		current["refused:"+x.ID] = x.Why
	}
	for _, g := range []struct {
		ids        []string
		state, why string
	}{
		{res.Dead, "dead", "bound process exited or its pid was reused; its lease lapses"},
		{res.Unknown, "unknown", "no owner bound, or its liveness unproved; its lease lapses (run: nova-friend here --pid <harness-pid>)"},
	} {
		for _, id := range g.ids {
			if refused[id] {
				continue
			}
			if r.states[id] != g.state {
				lines = append(lines, fmt.Sprintf("FRIEND HERE OWNER id=%s state=%s as=%s why=%s", id, g.state, name, quoteField(g.why)))
			}
			current[id] = g.state
		}
	}
	r.states, r.refusals = map[string]string{}, map[string]string{}
	for k, v := range current {
		if why, ok := strings.CutPrefix(k, "refused:"); ok {
			r.refusals[why] = v
			continue
		}
		r.states[k] = v
	}
	return lines
}

// byeSession clears the beat this session holds: ns_friend_bye, fenced on
// the session so another session's beat is never deleted.
func byeSession(ctx context.Context, c redis.Cmdable, name, session string) error {
	reply, err := c.FCall(ctx, life.FunctionBye, nil, name, session, name, "").Result()
	if err != nil {
		return err
	}
	switch w := words(reply); word(w, 0) {
	case "DOWN":
		return nil
	case "FENCED":
		return fmt.Errorf("%w: another session holds the beat", errFenced)
	default:
		return fmt.Errorf("bye: %s", strings.Join(w, " "))
	}
}

// hereCycle is one tick: the beat, then the leases, then the wake poll,
// then the take. A failed beat counts; hereFailLimit in a row ends the
// loop (exit 3, with bye). FENCED ends it at once with no bye (the beat is
// another session's); UNREGISTERED ends it with bye. A failed lease pass,
// poll or take is said on stderr and never ends the loop.
func hereCycle(ctx context.Context, p presence, s hereSteps, fails *int, out, errOut io.Writer) (int, hereEnd) {
	if err := s.beat(ctx); err != nil {
		switch {
		case errors.Is(err, errFenced):
			fmt.Fprintf(errOut, "friend %s beat: %v; this session is no longer the presence\n", p.Name, err)
			return 3, endWithoutBye
		case errors.Is(err, errUnregistered):
			fmt.Fprintf(errOut, "friend %s beat: %v\n", p.Name, err)
			return 3, endWithBye
		}
		*fails++
		fmt.Fprintf(errOut, "friend %s beat: %v (%d of %d)\n", p.Name, err, *fails, hereFailLimit)
		if *fails >= hereFailLimit {
			return 3, endWithBye
		}
		return 0, keepGoing
	}
	*fails = 0
	lines, err := s.leases(ctx)
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	if err != nil {
		fmt.Fprintf(errOut, "friend %s leases: %v\n", p.Name, err)
	}
	wake, err := s.poll(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "friend %s wake: %v\n", p.Name, err)
	} else if wake != "" {
		fmt.Fprintf(out, "FRIEND HERE WOKEN as=%s wake=%s\n", p.Name, quoteField(wake))
	}
	claims, err := s.take(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "friend %s take: %v\n", p.Name, err)
	}
	printClaims(out, claims)
	return 0, keepGoing
}

// hereLoop ticks until ctx ends (a signal: bye, exit 0) or a tick ends it.
func hereLoop(ctx context.Context, ticks <-chan time.Time, p presence, s hereSteps, out, errOut io.Writer) int {
	fails := 0
	for {
		select {
		case <-ctx.Done():
			return hereDown(ctx, p, s, 0, out, errOut)
		case <-ticks:
			code, end := hereCycle(ctx, p, s, &fails, out, errOut)
			switch end {
			case endWithBye:
				return hereDown(ctx, p, s, code, out, errOut)
			case endWithoutBye:
				return code
			}
		}
	}
}

// hereDown says bye and prints the DOWN line; a bye that failed is said on
// stderr and on the line, and the code is the caller's.
func hereDown(ctx context.Context, p presence, s hereSteps, code int, out, errOut io.Writer) int {
	failed := ""
	if err := s.bye(ctx); err != nil {
		failed = " bye=failed"
		fmt.Fprintf(errOut, "friend %s bye: %v\n", p.Name, err)
	}
	fmt.Fprintf(out, "FRIEND HERE DOWN as=%s session=%s%s\n", p.Name, p.Session, failed)
	return code
}

// printClaims is one line per task the take claimed.
func printClaims(out io.Writer, claims []task.Claim) {
	for _, c := range claims {
		fmt.Fprintf(out, "FRIEND HERE TOOK sprint=%s id=%s attempt=%d token=%s\n", c.Sprint, c.ID, c.Attempt, c.Token)
	}
}
