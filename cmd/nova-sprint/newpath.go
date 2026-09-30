package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	spverbs "github.com/mas-bandwidth/nova-tools/internal/sprint/verbs"
)

// The new path (the upper design, EVENT-DRIVEN-TICK version 2.1, item IT23):
// every verb is one entry of newVerbs, and each reaches the store only
// through internal/sprint/verbs and IT17's tick loop over one
// sprintfn.Client, whose one write is FCALL ns_sprint_step (E7). The grammar
// is the present command's (section 3 adds to it), the flags are parsed as
// the present verbs parse them (verbflag, flags anywhere among the words),
// and the exit codes are section 8.1's: 0 done, 2 refused, 3 a bug refusal
// (1.3.5).
//
// The switch: app.newPath selects this path for every verb run through the
// command's entry point (app.run), the driver's included. It is false in an
// ordinary build until the verb items IT17 and IT19 to IT22 are in the table;
// the tests set it.

// Exit codes of the new path (8.1, IT23).
const (
	exitDone    = 0
	exitRefused = 2
	exitBug     = 3
)

// codeNotOnNewPath is the refusal of a verb the table names and whose item
// has not landed: local, nothing sent. It is the command's own code and never
// reaches the store.
const codeNotOnNewPath = "NOTONNEWPATH"

// The machine's state words, as the present start and stop print them.
const (
	stateRunning = "RUNNING"
	stateStopped = "STOPPED"
)

// envPG is where init --pg defaults from: nova-config's store, as nova-config
// reads it.
const envPG = "NOVA_PG_DSN"

// bugCodes are the codes that are a bug when the store refuses a verb's step
// with them (1.3.5, "A bug"; 1.4.2: BUDGET on a read). A verb that receives
// one exits 3. EXISTS and NOROW are races on a derived id and on the stream
// set (1.0), which spverbs.IsRace names, and a race is never a bug here.
var bugCodes = map[string]bool{
	"LIMIT": true, "BUDGET": true, "REQUEST": true, "TWICE": true, "EXISTS": true, "WRONGTYPE": true,
	"NOPERM": true, "OVERFLOW": true, "LOGID": true, "CONFIG": true, "ENGINE": true, "NOROW": true,
	"NOCOL": true, "FIELDNAME": true, "FIELDOVERLAP": true, "ROWCONFLICT": true, "OCCUPIED": true,
	"OPCONFLICT": true,
}

// usageError is a verb's words or flags refused before anything is read:
// exit 2, as the present refuse.
type usageError struct{ why string }

func (u *usageError) Error() string { return u.why }

func usage(format string, args ...any) error { return &usageError{why: fmt.Sprintf(format, args...)} }

// exitOf is a verb's exit code from its error (8.1, IT23; 1.3.5): 0 done; 3
// when the store refused the step with a bug code; 2 for every other refusal
// (the verb's own from its read, a race past its retries, a usage refusal),
// for an outcome the store did not confirm (spverbs.Unknown), and for a store
// that did not answer.
func exitOf(err error) int {
	if err == nil {
		return exitDone
	}
	var rf *spverbs.Refused
	if errors.As(err, &rf) && !rf.Local && !spverbs.IsRace(rf.Code()) && bugCodes[rf.Code()] {
		return exitBug
	}
	return exitRefused
}

// newPathClient is the production client of the new path: sprintfn.NewRedis
// at the address, with the login the present command dials with
// (NOVA_SPRINT_REDIS_USER, and the variable NOVA_SPRINT_REDIS_PASSWORD_ENV
// names, else NOVA_REDIS_BENCH_PASSWORD).
func (a *app) newPathClient(_ context.Context, addr string, names sprint.Names) (sprintfn.Client, func() error, error) {
	user, passwordEnv := a.getenv(redisauth.UserEnv), ""
	if user != "" {
		passwordEnv = a.getenv(redisauth.PasswordEnvEnv)
		if passwordEnv == "" {
			passwordEnv = redisauth.DefaultPasswordEnv
		}
	}
	r, err := sprintfn.NewRedis(addr, user, passwordEnv, names)
	if err != nil {
		return nil, nil, err
	}
	return r, r.Close, nil
}

// newPathConfig opens nova-config's store at the DSN, as nova-config does.
func newPathConfig(ctx context.Context, dsn string) (spverbs.ConfigRows, func() error, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, nil, fmt.Errorf("init reads the coordinator from nova-config: --pg <dsn> is required (or %s)", envPG)
	}
	pg, err := config.OpenPG(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	return pg, pg.Close, nil
}

// newPathState is what one process keeps of the new path between the verbs
// it runs (the driver runs many): the clients, opened once per address and
// prefix, and the epoch each last saw, so a verb after a clear plans at the
// new epoch without a first read at the old one.
type newPathState struct {
	clients map[string]sprintfn.Client
	epochs  map[string]uint64
	closers []func() error
}

func (a *app) pathState() *newPathState {
	if a.np == nil {
		a.np = &newPathState{clients: map[string]sprintfn.Client{}, epochs: map[string]uint64{}}
	}
	return a.np
}

// env is the verbs' Env for a verb run with the common flags c: the client
// at --redis for --prefix, the actor, and the epoch the caller holds
// (--epoch), else the one this process last saw, else 0 (the verb's first
// read names the active epoch, AL2).
func (a *app) env(ctx context.Context, c common, class string) (*spverbs.Env, string, error) {
	if strings.TrimSpace(c.redis) == "" {
		return nil, "", usage("--redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR)")
	}
	if why := actorRule(class, c.actor); why != "" {
		return nil, "", usage("%s", why)
	}
	names := sprint.Names{Prefix: c.prefix}
	ps := a.pathState()
	key := c.redis + "\x00" + c.prefix
	cl, ok := ps.clients[key]
	if !ok {
		var closer func() error
		var err error
		cl, closer, err = a.sprintClient(ctx, c.redis, names)
		if err != nil {
			return nil, "", err
		}
		ps.clients[key] = cl
		if closer != nil {
			ps.closers = append(ps.closers, closer)
		}
	}
	e := &spverbs.Env{C: cl, Names: names, Actor: c.actor, Epoch: ps.epochs[key]}
	if c.epoch >= 0 {
		e.Epoch = uint64(c.epoch)
	}
	return e, key, nil
}

// closePath closes what the new path opened.
func (a *app) closePath() {
	if a.np == nil {
		return
	}
	for _, c := range a.np.closers {
		_ = c()
	}
	a.np = nil
}

// reach is whether the store the common flags name can be reached, on the
// path the app is on: play's check before it plays.
func (a *app) reach(c common) error {
	if !a.newPath {
		_, err := a.store(c)
		return err
	}
	_, _, err := a.env(context.Background(), c, verbClasses[c.verb])
	return err
}

// runNew is the command's entry point on the new path.
func (a *app) runNew(args []string, stdout, stderr io.Writer) (code int) {
	defer verbflag.Recover(stdout, prog, newBanner(), &code)
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; available: "+strings.Join(newVerbNames(), ", ")+"; run: nova-sprint help")
	}
	if args[0] == "help" || verbflag.IsHelp(args[0]) {
		return newHelp(args[1:], stdout, stderr)
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintln(stdout, versionLine())
		return 0
	}
	v, rest, ok := lookupNew(args)
	if !ok {
		if newGroups[args[0]] {
			return refuse(stderr, args[0], "unknown or missing subverb; run: nova-sprint help "+args[0])
		}
		return refuse(stderr, "", "unknown verb "+oneline.Escape(args[0])+"; available: "+strings.Join(newVerbNames(), ", ")+"; run: nova-sprint help")
	}
	return a.dispatch(v, rest, stdout, stderr)
}

// lookupNew is the entry the words name: the longest name that is a prefix of
// them ("fleet up" before "fleet").
func lookupNew(args []string) (newVerb, []string, bool) {
	best, n := -1, 0
	for i, v := range newVerbs {
		words := strings.Fields(v.name)
		if len(args) >= len(words) && strings.Join(args[:len(words)], " ") == v.name && len(words) > n {
			best, n = i, len(words)
		}
	}
	if best < 0 {
		return newVerb{}, nil, false
	}
	return newVerbs[best], args[n:], true
}

// dispatch parses the verb's flags and words, runs it, and renders its result.
func (a *app) dispatch(v newVerb, args []string, stdout, stderr io.Writer) int {
	if v.local != nil { // a verb of the command itself (play): it parses and reports itself
		return v.local(a, args, stdout, stderr)
	}
	p, err := a.parseNew(v, args)
	if err != nil {
		return refuse(stderr, v.name, err.Error())
	}
	ctx := context.Background()
	e, key, err := a.env(ctx, p.c, classOf(v))
	if err != nil {
		var u *usageError
		if errors.As(err, &u) {
			return refuse(stderr, v.name, err.Error())
		}
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, v.name, oneline.Escape(err.Error()))
		return exitRefused
	}
	res, err := v.call(ctx, e, p)
	if err == nil || res.EpochAfter > 0 || res.Epoch > 0 {
		a.pathState().epochs[key] = e.Epoch
	}
	var u *usageError
	if errors.As(err, &u) {
		return refuse(stderr, v.name, err.Error())
	}
	return render(v, p, res, err, stdout, stderr)
}

// newOut is a verb's report for a program: the present report's fields
// (verb, op, moved, refused, notes, attempts, replay, error, unknown; and
// start's and stop's before, after and changed), then the new path's own.
// moved and refused are always lists, as the present --json prints them; the
// new path's Result carries no per-card lists yet, so they are empty.
type newOut struct {
	Verb     string           `json:"verb"`
	Op       string           `json:"op,omitempty"`
	Moved    []string         `json:"moved"`
	Refused  []sprint.Refusal `json:"refused"`
	Notes    int              `json:"notes"`
	Attempts int              `json:"attempts"`
	Replay   bool             `json:"replay,omitempty"`
	Error    string           `json:"error,omitempty"`
	Unknown  bool             `json:"unknown,omitempty"`
	Before   string           `json:"before,omitempty"`
	After    string           `json:"after,omitempty"`
	Changed  *bool            `json:"changed,omitempty"`

	Code       string `json:"code,omitempty"`
	Epoch      uint64 `json:"epoch"`
	EpochAfter uint64 `json:"epoch_after,omitempty"`
	Parts      int    `json:"parts,omitempty"`
	Resumed    int    `json:"resumed,omitempty"`
	Retries    int    `json:"retries,omitempty"`
	Trips      int    `json:"trips"`
	Recorded   string `json:"recorded,omitempty"`
	Said       string `json:"said,omitempty"`
	Exit       int    `json:"exit"`
}

// machineStates is start's and stop's before and after, as the present
// report prints them: from the verb's result, or from its MACHINESTATE
// refusal (the state it found and left).
func machineStates(verb string, err error) (before, after string, changed bool, ok bool) {
	running := verb == "start"
	var rf *spverbs.Refused
	switch {
	case verb != "start" && verb != "stop":
		return "", "", false, false
	case err == nil:
		if running {
			return stateStopped, stateRunning, true, true
		}
		return stateRunning, stateStopped, true, true
	case errors.As(err, &rf) && rf.Code() == sprintfn.CodeMachineState:
		if running {
			return stateRunning, stateRunning, false, true
		}
		return stateStopped, stateStopped, false, true
	}
	return "", "", false, false
}

// render prints a verb's result, as the present verbs print theirs: with
// --json one object on stdout; else its token line (OK on stdout, FAIL on
// stderr), its one line, and on an error the error on stderr. The exit code
// is exitOf's.
func render(v newVerb, p *parsed, res spverbs.Result, err error, stdout, stderr io.Writer) int {
	code := exitOf(err)
	var rf *spverbs.Refused
	var un *spverbs.Unknown
	isRefused, isUnknown := errors.As(err, &rf), errors.As(err, &un)
	if p.c.json {
		o := newOut{Verb: v.name, Op: res.Op, Moved: []string{}, Refused: []sprint.Refusal{}, Attempts: 1 + res.Retries,
			Replay: res.Replay, Unknown: isUnknown, Epoch: res.Epoch, EpochAfter: res.EpochAfter, Parts: res.Parts,
			Resumed: res.Resumed, Retries: res.Retries, Trips: res.Trips, Recorded: res.Recorded, Said: res.Said, Exit: code}
		if before, after, changed, ok := machineStates(v.name, err); ok {
			o.Before, o.After, o.Changed = before, after, &changed
		}
		if err != nil {
			o.Error = err.Error()
		}
		if isRefused {
			o.Code = rf.Code()
			if o.Op == "" {
				o.Op = rf.Op
			}
		}
		b, _ := json.Marshal(o)
		fmt.Fprintln(stdout, string(b))
		return code
	}
	var fields []string
	if before, after, changed, ok := machineStates(v.name, err); ok {
		fields = append(fields, "before="+before, "after="+after)
		if changed {
			fields = append(fields, "changed")
		} else {
			fields = append(fields, "unchanged: the machine is "+after+" already")
		}
	}
	status := "OK"
	if err != nil {
		status = "FAIL"
		switch {
		case isRefused:
			fields = append(fields, "code="+rf.Code(), "changed=no")
		case isUnknown:
			fields = append(fields, "changed=unknown")
		default:
			fields = append(fields, "changed=no")
		}
	}
	op := res.Op
	if op == "" && isRefused {
		op = rf.Op
	}
	if op != "" {
		fields = append(fields, "op="+oneline.Escape(op))
	}
	if res.Replay {
		fields = append(fields, "replay=yes")
	}
	if res.EpochAfter > 0 && res.EpochAfter != res.Epoch {
		fields = append(fields, fmt.Sprintf("epoch=%d->%d", res.Epoch, res.EpochAfter))
	} else {
		fields = append(fields, fmt.Sprintf("epoch=%d", res.Epoch))
	}
	if res.Parts > 0 || res.Resumed > 0 {
		fields = append(fields, fmt.Sprintf("parts=%d resumed=%d", res.Parts, res.Resumed))
	}
	if res.Retries > 0 {
		fields = append(fields, fmt.Sprintf("retries=%d", res.Retries))
	}
	fields = append(fields, fmt.Sprintf("trips=%d", res.Trips))
	out := stdout
	if err != nil {
		out = stderr
	}
	fmt.Fprintf(out, "%s %s %s\n", token(v.name), status, strings.Join(fields, " "))
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, v.name, oneline.Escape(err.Error()))
		return code
	}
	if res.Said != "" {
		fmt.Fprintln(stdout, oneline.Escape(res.Said))
	}
	return code
}

// stubbed is the call of a verb whose item has not landed: refused locally,
// naming the item, before anything is sent. Swapping in the real verb is one
// entry of newVerbs: its call.
func stubbed(item string) verbCall {
	return func(_ context.Context, _ *spverbs.Env, p *parsed) (spverbs.Result, error) {
		return spverbs.Result{Verb: p.verb}, &spverbs.Refused{Verb: p.verb, Local: true, Refusal: &sprintfn.Refusal{Code: codeNotOnNewPath,
			Message: fmt.Sprintf("%s is not on the new path yet (%s builds it); nothing was sent", p.verb, item)}}
	}
}
