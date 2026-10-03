// nova-sprint: the sprint table (docs/SPEC-SPRINT.md). Four tables on
// nova-table (work, readers, merge, fleet), the moves between them, and the
// notifications that bring the coordinator its decisions. Every verb takes a
// set and is one step; the command is a face over internal/sprint (the pure
// core) and internal/sprint/store (the binding to the table layer).
//
// Exit 0 done, 1 refused (a card or the store said no), 2 usage or a store
// that did not answer (fleet sync --check: there is drift), 3 (fleet sync: the
// config cannot be read).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/redis/go-redis/v9"
)

const prog = "nova-sprint"

// version is empty in every ordinary build; a release stamps it with
// -ldflags "-X main.version=<tag>".
var version string

func main() {
	a := newApp(os.Getenv)
	defer a.close()
	os.Exit(a.run(os.Args[1:], os.Stdout, os.Stderr))
}

// app is one process's view of the store: its connection, opened once and
// shared by every verb it runs (the driver runs many), its clock, and how it
// sleeps. Tests give it a backend of their own.
type app struct {
	getenv  func(string) string
	now     func() time.Time
	sleep   func(time.Duration)
	backend func(ctx context.Context, addr string, names sprint.Names) (store.Backend, error)
	conns   map[string]*redisconn.Conn
	cached  map[string]store.Backend
	twins   map[string]*twin // the open `--redis mem:<file>` twins (twin.go)
	meter   hostload.Source  // how fleet beat measures this machine
	etaMu   sync.Mutex
	etas    []etaSample // the view's estimates of the last etaHold (heldETA)

	// inventory reads the machines of nova-config and their widths (fleet
	// sync): tests give it the config's in-memory store.
	inventory inventoryFn
	// friends reads the names of nova-config's friend rows (friend sync):
	// tests give it the config's in-memory store.
	friends friendsFn
	loc     *time.Location // the zone times print in: nil is the machine's local zone
	// notify is how an interrupt reaches a command that runs until it is
	// interrupted (where --watch): the context it returns is done at one.
	notify func(ctx context.Context) (context.Context, context.CancelFunc)
	// screen is the rows and columns of the screen a writer draws on, each 0
	// when not known (the writer is not a terminal).
	screen func(w io.Writer) (rows, cols int)
	// ticked, when set, is told of each tick run begins: its count, when it
	// began, and why (the loop's start, a line on the log, the clock of a
	// quiet log, a retry).
	ticked func(n int, began time.Time, why string)
	// executable is the path of the binary this process runs, os.Executable
	// unless a test sets it: run stops when the file there is replaced.
	executable func() (string, error)
	// profiled, when set, is told of each tick run finished, by its count:
	// run --cpuprofile ends its profile at the last tick it covers.
	profiled func(n int)
	// checkTwin, when set (a test), is every store's CheckTwin: each part a
	// tick plans on its twin is checked against a fresh read (store/twin.go).
	checkTwin func(held, fresh *sprint.Snapshot) error
	// readTwins is the process's read twin of each store, by address: what
	// its verbs last read, so a verb after the first reads only what changed
	// (store/twin.go). It is not the mem twin above, which is a store.
	readTwins map[string]*store.Twin
	// landRoot is the directory land keeps its clones under when it is given
	// no --repo-dir (land.go): os.UserCacheDir's nova-sprint/land.
	landRoot func() (string, error)
	// gitEnv is the environment land's git and check run in: nil is the
	// caller's, untouched (a test gives git an identity and no global config).
	gitEnv []string
	// beforePush, when set (a test), runs before each push land makes, with
	// the attempt (1, then 2 after the base moved).
	beforePush func(attempt int)
	// serial is the server's one line of control (serve.go): a worker's batch
	// and a tick of the run loop each hold it, so neither runs during the other.
	// serveAddr is the store the server runs the workers' verbs on.
	serial    sync.Mutex
	serveAddr string
	// serving says the verb running is one a worker sent to the server (set and
	// cleared under serial): its step names the epoch its worker holds, or is
	// refused (runStep).
	serving bool
	// forward sends verbs to the sprint's server named by NOVA_SPRINT_SERVER (the
	// coordinator's verbs, forward.go): nil is sprintwire.Client's Do, a test gives the
	// server's own step.
	forward func(ctx context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error)
	// landFailed is what the land loop's last round printed when it failed, "" after a
	// round that did not (landloop.go): the same failure again prints nothing.
	landFailed string
	// prune is the landed cards' branches waiting for the cleanup (landprune.go), and
	// landLazy says the land running is the land loop's, which cleans up between its
	// rounds: land itself then leaves the queue as it is.
	prune    pruneQueue
	landLazy bool
	// tickDeadline is how long the run loop waits for one tick (run
	// --tick-deadline; 0, a test's loop, waits for ever); after is the clock
	// it waits on (time.After unless a test sets it), and exit how the loop
	// ends the process when a tick runs past it (os.Exit unless a test sets it).
	tickDeadline time.Duration
	after        func(time.Duration) <-chan time.Time
	exit         func(code int)
	// home is the directory a seat's inbox is under (inbox --wait --push seat:
	// ~/<holder>-working/inbox): os.UserHomeDir unless a test sets it.
	home func() (string, error)
}

func newApp(getenv func(string) string) *app {
	a := &app{getenv: getenv, now: time.Now, sleep: time.Sleep, after: time.After, exit: os.Exit, conns: map[string]*redisconn.Conn{}, cached: map[string]store.Backend{}, meter: hostload.Local(), notify: interruptContext, screen: screenSize}
	a.backend = a.redisBackend
	a.inventory = a.readInventory
	a.friends = a.readFriends
	a.landRoot = defaultLandRoot
	a.home = os.UserHomeDir
	return a
}

func (a *app) close() {
	for _, c := range a.conns {
		// ignored: a close at the end of the run, after every answer is printed
		_ = c.Close()
	}
}

// redisBackend opens the store once per address, as nova-table dials it: the
// address, then NOVA_SPRINT_REDIS_USER and the variable
// NOVA_SPRINT_REDIS_PASSWORD_ENV names.
func (a *app) redisBackend(ctx context.Context, addr string, names sprint.Names) (store.Backend, error) {
	if isTwin(addr) {
		return a.twinBackend(addr)
	}
	key := addr
	if b, ok := a.cached[key]; ok {
		return b, nil
	}
	conn, ok := a.conns[addr]
	if !ok {
		var err error
		conn, err = a.openConn(ctx, addr)
		if err != nil {
			return nil, err
		}
		if err := libraryMatches(ctx, conn.Client(), addr); err != nil {
			// ignored: a close on the failure path; the library mismatch error is the one returned
			_ = conn.Close()
			return nil, err
		}
		a.conns[addr] = conn
	}
	b := &store.Redis{C: conn.Client(), Names: names, Now: a.now}
	b.CountTrips() // a tick's cost says its round trips (store/stats.go)
	a.cached[key] = b
	return b, nil
}

// openConn dials the address as nova-table does: the address, then
// NOVA_SPRINT_REDIS_USER and the variable NOVA_SPRINT_REDIS_PASSWORD_ENV
// names, bounded to 10 s.
func (a *app) openConn(ctx context.Context, addr string) (*redisconn.Conn, error) {
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if a.getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if a.getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return redisconn.Open(ctx, o, a.getenv)
}

// libraryMatches refuses a store whose loaded table function library is not
// this build's (one FUNCTION LIST, once per process and address): every write
// through a library of another build would be refused or unreadable.
func libraryMatches(ctx context.Context, c *redis.Client, addr string) error {
	source, err := fn.Source()
	if err != nil {
		return err
	}
	code, found, err := fn.Loaded(ctx, c)
	if err != nil {
		return err
	}
	switch {
	case !found:
		return fmt.Errorf("the store at %s holds no %s function library; run: nova-redis fn load --addr %s", addr, fn.Library, addr)
	case fn.Sum(code) != fn.Sum(source):
		return fmt.Errorf("the store at %s holds %s library %s, and this build is %s; run: nova-redis fn load --addr %s", addr, fn.Library, fn.Sum(code), fn.Sum(source), addr)
	}
	return nil
}

// common is the flags every store verb takes.
type common struct {
	verb             string // the verb's name: its class (coordinator.go)
	coordinator      string // init --coordinator: the coordinator it names
	redis, actor, op string
	json             bool
	max              int
	epoch            int64       // the epoch the caller holds; -1 is none
	group            groupReport // set by --group, for the verb's report
	// packets, when set, is what the step hands its actor (take: each
	// card's packet), read after the step and printed with its report.
	packets func(ctx context.Context, st *store.Store, res store.Result) []sprint.Packet
	handed  []sprint.Packet
	// says is what the verb tells its reader about what it did that the moves
	// do not say (a card's id from its file, a card with no brief, a take cut
	// short and why), printed as NOTE lines under its summary line; after, when
	// set, adds to it from the step's result.
	says  []string
	after func(ctx context.Context, st *store.Store, res store.Result) []string
	// addStream and addBefore are add's own: its result line names them
	// (stream=<s> cards=<n> before=<sentinel>).
	addStream string
	addBefore string
}

func (c *common) register(fs flagSet, getenv func(string) string) {
	fs.StringVar(&c.redis, "redis", firstEnv(getenv, "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR"), "the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)")
	fs.StringVar(&c.actor, "actor", getenv("NOVA_SPRINT_ACTOR"), "who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)")
	fs.StringVar(&c.op, "op", "", "the caller's operation id: the same id again returns the recorded result and changes nothing")
	fs.BoolVar(&c.json, "json", false, "print one JSON object for a program instead of the lines")
	fs.IntVar(&c.max, "max", 20, "listed items of each kind; 0 is all")
	fs.Int64Var(&c.epoch, "epoch", -1, "the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none")
}

func firstEnv(getenv func(string) string, names ...string) string {
	for _, n := range names {
		if v := getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// store is the one way a command reaches the store: pinned to the sprint's
// current epoch (a restore a cut clear still owes performed first), so every
// key it reads or writes is of that epoch. A command reads an earlier epoch
// only through storeAt.
func (a *app) store(c common) (*store.Store, error) { return a.storeCtx(context.Background(), c) }

// storeCtx is store, its reads made in ctx: a command that can be interrupted
// (where --watch) hands the context it ends with, so the interrupt cuts a
// read short.
func (a *app) storeCtx(ctx context.Context, c common) (*store.Store, error) {
	if a.getenv("NOVA_SPRINT_PREFIX") != "" {
		return nil, errors.New("NOVA_SPRINT_PREFIX is set: " + noPrefix + "; unset it")
	}
	if strings.TrimSpace(c.redis) == "" {
		// the twin is named here too, so a first run with no Redis is one turn away (tool ledger P9)
		return nil, fmt.Errorf("--redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR); with no Redis, --redis mem:<file> runs it on the in-memory twin kept in that file (nova-sprint help, trying it without a Redis)")
	}
	if why := needsActor(c); why != "" {
		return nil, errors.New(why)
	}
	names := sprint.Names{}
	b, err := a.backend(ctx, c.redis, names)
	if err != nil {
		return nil, err
	}
	// a twin is ticked by hand: no machine runs between its commands (twin.go)
	st := &store.Store{B: b, Names: names, Actor: c.actor, Now: a.now, NewID: store.NewID, Sleep: a.sleep, CheckTwin: a.checkTwin, ByHand: a.twinOpen(c.redis)}
	// every verb this process runs on the store reads through one twin: a
	// verb after the first reads only what changed (store/twin.go)
	if a.readTwins == nil {
		a.readTwins = map[string]*store.Twin{}
	}
	if a.readTwins[c.redis] == nil {
		a.readTwins[c.redis] = store.NewTwin()
	}
	st.ShareTwin(a.readTwins[c.redis])
	st.LockAfterLoss = true // a part that lost a try locks (store/lock.go)
	if t := a.twins[c.redis]; t != nil {
		st.NewID = t.newID
	}
	if st, err = st.Pinned(ctx); err != nil {
		return nil, err
	}
	if a.twinOpen(c.redis) {
		if err := a.beatTwin(ctx, st); err != nil {
			return nil, err
		}
	}
	if why, err := coordinatorOnly(ctx, st, c); err != nil || why != "" {
		if err == nil {
			err = errors.New(why)
		}
		return nil, err
	}
	return st, nil
}

// run is the one entry point: the command line, and the driver, which runs
// every verb it plays through it with an argument list.
func (a *app) run(args []string, stdout, stderr io.Writer) (code int) {
	defer recoverHelp(stdout, &code)
	defer func() {
		// a twin is saved after every verb (twin.go); a verb that could not
		// save it has not finished, whatever it printed
		if err := a.saveTwins(); err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", prog, oneline.Escape(err.Error()))
			code = 2
		}
	}()
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; available: "+strings.Join(verbNames(), ", ")+"; run: nova-sprint help")
	}
	if args[0] == "help" || verbflag.IsHelp(args[0]) {
		return helpCommand(args[1:], stdout, stderr)
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintln(stdout, versionLine())
		return 0
	}
	if code, sent := a.forwarded(args, stdout, stderr); sent {
		return code
	}
	for _, v := range verbs {
		words := strings.Fields(v.name)
		if len(args) >= len(words) && strings.Join(args[:len(words)], " ") == v.name {
			return v.run(a, args[len(words):], stdout, stderr)
		}
	}
	if members := groupVerbs(args[0]); len(members) > 0 {
		// a verb group: its -h is its help at exit 0 (help is never a refusal); a bare
		// group, or a word that is none of its verbs, is refused naming its verbs
		// (tool ledger P7, X11)
		if len(args) > 1 && verbflag.IsHelp(args[1]) {
			return helpCommand(args[:1], stdout, stderr)
		}
		for _, w := range args[1:] {
			if w == "--prefix" || w == "-prefix" || strings.HasPrefix(w, "--prefix=") || strings.HasPrefix(w, "-prefix=") {
				return refuse(stderr, args[0], noPrefix)
			}
		}
		why := args[0] + " wants one of its verbs"
		if len(args) > 1 {
			why = "unknown verb " + oneline.Escape(args[0]+" "+args[1])
		}
		return refuse(stderr, args[0], why+"; its verbs are "+strings.Join(members, ", ")+"; run: nova-sprint help "+args[0])
	}
	return refuse(stderr, "", "unknown verb "+oneline.Escape(args[0])+"; available: "+strings.Join(verbNames(), ", ")+"; run: nova-sprint help")
}

// refuse is a usage refusal: exit 2.
// noPrefix is what a --prefix flag or a NOVA_SPRINT_PREFIX variable is refused
// with.
const noPrefix = "there is no prefix: the tables are always work, merge, readers and fleet and the view is sprint"

// errNoPrefix is the error of a --prefix flag: a verb reports it as it is,
// never wrapped in what the verb was parsing.
var errNoPrefix = errors.New(noPrefix)

// argErr is what a verb refuses its arguments with: the words, then the error;
// a --prefix flag is the one line errNoPrefix, alone, and a flag refusal (flagError)
// is its own line.
func argErr(words string, err error, found ...string) string {
	var fe *flagError
	switch {
	case err == nil && len(found) == 0:
		// a verb that refuses its words with no parse error refuses them by
		// what it found, never `<nil>` (tool ledger P4)
		return strings.TrimSpace(words) + ", found none"
	case err == nil:
		return strings.TrimSpace(words) + ", found " + strings.Join(found, " ")
	case errors.Is(err, errNoPrefix), errors.As(err, &fe):
		return err.Error()
	}
	return fmt.Sprint(words, err)
}

// refuse prints the one refusal line, `nova-sprint[ <verb>] REFUSED: <what>;
// run: <remedy>` (docs/STANDARD.md section 3, point 1), and is a usage refusal:
// exit 2.
func refuse(stderr io.Writer, verb, what string) int {
	where := prog
	if verb != "" {
		where += " " + verb
	}
	if !strings.Contains(what, "; run: ") {
		what += "; run: nova-sprint " + strings.TrimSpace(verb+" -h")
	}
	fmt.Fprintf(stderr, "%s REFUSED: %s\n", where, oneline.Escape(what))
	return 2
}

// storeAt is the store, reading an earlier epoch as it was when at is 0 or
// more: a store for reads only.
func (a *app) storeAt(c common, at int64) (*store.Store, error) {
	return a.storeAtCtx(context.Background(), c, at)
}

// storeAtCtx is storeAt, its reads made in ctx (see storeCtx).
func (a *app) storeAtCtx(ctx context.Context, c common, at int64) (*store.Store, error) {
	st, err := a.storeCtx(ctx, c)
	if err != nil || at < 0 {
		return st, err
	}
	return st.At(uint64(at)), nil
}
