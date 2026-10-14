// nova-hook hooks an identity in: the one record, in the bus Redis, of the
// pushes its role needs (the bus, the sprint, its work), each live while its
// holder renews a lease and proven while a proof stands (docs/SPEC-HOOK.md;
// the module is hook/, the machine tla/Hook.tla). Tools gate the acting
// identity; some bus mailbox proofs are advisory unless --require-push is set
// or NOVA_BUS_REQUIRE_PUSH=1.
// The verbs are hook, prove, unhook and status;
// the dispatch, the banner, the help, the version verb, the refusals and the
// output envelope are pkg/tool's, and the rules are the hook module's.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/hookgate"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

var version string

// Tick is how long one blocking read of the hook's loop waits: the bus
// stream is read, and the record looked at, once a tick.
const Tick = time.Second

// world is what the tool reaches outside itself: the environment, the store
// it opens, the clock, the signals the loop stops on and the nonce source.
// main passes the real one; a test passes its own over hooktest's store.
type world struct {
	getenv   func(string) string
	open     func(ctx context.Context, addr string) (st bus.Store, login string, closeStore func(), err error)
	fleetBus func(ctx context.Context, addr string) (string, error)
	lookup   bus.Lookup
	now      func() time.Time
	signals  func(ctx context.Context) (context.Context, context.CancelFunc)
	nonce    func() string
	holder   string        // who holds the pushes this process holds, as the lease shows it
	tick     time.Duration // one blocking read of the loop
}

func realWorld() world {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown-host" // ignored: the holder's name is a label on the lease, never a key
	}
	w := world{getenv: os.Getenv, now: time.Now, tick: Tick,
		holder: fmt.Sprintf("nova-hook %s pid %d", host, os.Getpid()),
		lookup: func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		},
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		},
		nonce: func() string {
			var raw [8]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return fmt.Sprint(time.Now().UnixNano()) // ignored: no random bytes; the clock serves a nonce the stream alone carries
			}
			return hex.EncodeToString(raw[:])
		},
	}
	w.open = func(ctx context.Context, addr string) (bus.Store, string, func(), error) {
		st, login, closeStore, err := bus.OpenRedis(ctx, addr, w.getenv)
		if err != nil {
			return nil, "", nil, err
		}
		return st, login, closeStore, nil
	}
	w.fleetBus = func(ctx context.Context, addr string) (string, error) { return bus.ReadFleetBus(ctx, addr, w.getenv) }
	return w
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, realWorld())) }

// run is the entry point apart from the process.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, w world) int {
	return hookTool(w).Run(args, stdin, stdout, stderr)
}

func roleNames() string {
	var out []string
	for _, r := range hook.Roles {
		out = append(out, string(r)+" ("+pushList(hook.PushesOf(r))+")")
	}
	return strings.Join(out, ", ")
}

func pushList(ps []hook.Push) string {
	var out []string
	for _, p := range ps {
		out = append(out, string(p))
	}
	return strings.Join(out, ",")
}

func hookTool(w world) *tool.Tool {
	store := func(f *tool.Flags) {
		f.String("redis", w.getenv(bus.RedisEnv), "the bus Redis address, host:port, where the hook record lives (default: "+bus.RedisEnv+", else "+bus.SprintRedisEnv+"'s fleet:bus)")
	}
	as := func(f *tool.Flags) {
		f.String("as", "", "your name: the login user when there is one (then it may be left out)")
	}
	return &tool.Tool{
		Name:  "nova-hook",
		What:  "hooks an acting identity to its role's live, proven pushes; some bus mailbox checks are advisory",
		Stamp: version,
		How: `roles: coordinator bus+sprint; friend bus+work; comms bus; member/reader work.
strict gates require the acting name's role push to be live and proven.
nova-bus send/recv note unproven mailboxes unless --require-push or NOVA_BUS_REQUIRE_PUSH=1.
a hook proof is a protocol response only, not foreground presence or native idle-wake evidence.
record in bus Redis; address: --redis, else NOVA_BUS_REDIS, else fleet:bus at NOVA_SPRINT_REDIS.`,
		ExitTable: "0 done (status: proven), 1 the verb ran and said no (not hooked in, a challenge not answered, unhooked under the loop), 2 could not run (a flag, an input, a store that did not answer).",
		Words:     []string{"ARMED", "CHALLENGE", "PUSH", "STATE", "STOP", "DRY-RUN"},
		Verbs: []tool.Verb{
			{
				Name:    "status",
				Usage:   "status [--as <name>] [--max <n>] [--redis <addr>]",
				Example: "status --as m1",
				Effect:  tool.Inspection,
				Detail: `With --as, one name's record: STATUS OK when it is proven (exit 0), else STATUS FAILED with what is not
live and the command that hooks it in (exit 1). Without it, every name that ever hooked in: STATUS OK count=<n>
proven=<n>, then one STATUS NAME name= state= role= epoch= <push>=<down|armed|proven> line each, at exit 0.
The states: none, unhooked, expired (no push live), partial, live (every push live, not every one proven), proven.
example: nova-hook status --as ada`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "the name whose record to read (default: every name that ever hooked in)")
					f.Max()
					store(f)
				},
				Run: w.status,
			},
			{
				Name:    "hook",
				Usage:   "hook --as <me> --role <role> [--once] [--redis <addr>] [--dry-run]",
				Example: "hook --as m1 --role member --once",
				Effect:  tool.Delivery + ": writes the hook record and the lease of every push it holds, and streams the bus push until a signal",
				DryRun:  true,
				Detail: `Hooks you in as --role (` + roleNames() + `): a record already hooked as that role is joined at
its epoch, otherwise a new epoch starts and every lease and proof of the old one is cleared. This process holds
the bus push when the role carries it: it renews the lease every ` + hook.RenewEvery.String() + ` and prints each message on your bus
stream as it lands, HOOK PUSH push=bus id= from= subject=. A push it does not hold (the sprint push, held by
nova-sprint hook; the work push, held by the friend daemon or the member loop) is named with its holder on the
HOOK ARMED line's notes. It prints HOOK CHALLENGE nonce=<n> at start and every ` + hook.ChallengeEvery.String() + `: run the line it carries,
nova-hook prove --as <me> --nonce <n>, within ` + hook.AnswerBound.String() + `, and the pushes it holds are proven. HOOK STATE state= line
each time the record changes. Run it under the session's Monitor: each line is a notification. It stops on SIGINT or
SIGTERM (HOOK STOP, exit 0; the leases lapse after ` + hook.LeaseTTL.String() + `), or when the record is unhooked under it (exit 1).
This proof records a protocol response to the challenge; it does not establish foreground session presence or native idle-wake behavior.
--once arms, renews once, sets the challenge and prints the record's state, at exit 0 when proven, else 1 with the
remedy: what a daemon's start does, and what a role whose holders are daemons runs to hook in again after an unhook.
--dry-run reads the record and prints the plan the real run would take (the role, the epoch, the pushes held and
each other push's holder) and writes nothing.
example: nova-hook hook --as ada --role coordinator`,
				Flags: func(f *tool.Flags) {
					as(f)
					f.Required("role", "the role you hook in as, one of "+roleNames())
					f.Bool("once", false, "arm, renew once, print the state and exit (0 proven, 1 not); no loop")
					store(f)
					f.Check(func(c *tool.Call) {
						if r := c.Str("role"); r != "" {
							if _, err := hook.ParseRole(r); err != nil {
								c.Problem(err.Error())
							}
						}
					})
				},
				Run: w.hook,
			},
			{
				Name:    "prove",
				Usage:   "prove --as <me> --nonce <n> [--redis <addr>] [--dry-run]",
				Example: "", // the nonce is the one a hook's stream printed: no example line can carry it
				Effect:  tool.Delivery + ": writes the proof of each push the challenge named",
				DryRun:  true,
				Detail: `Answers the challenge the hook printed (HOOK CHALLENGE nonce=<n>): with that nonce, under the record's
epoch, every push the challenge named whose lease is live is proven for ` + hook.ProofTTL.String() + `, and the challenge is spent.
Prints PROVE OK proven=<n> and the record's state. A nonce that is not the one asked, a challenge that lapsed or
was answered, or a record unhooked since is FAILED at exit 1, with what to do.
--dry-run reads the record and prints what it would prove (the state, the role and how many live pushes a
proof would cover) and writes nothing.
example: nova-hook prove --as ada --nonce 9f2c4e1a0b3d5c7e`,
				Flags: func(f *tool.Flags) {
					as(f)
					f.Required("nonce", "the nonce the newest HOOK CHALLENGE line printed")
					store(f)
				},
				Run: w.prove,
			},
			{
				Name:    "unhook",
				Usage:   "unhook --as <me> [--redis <addr>] [--dry-run]",
				Example: "unhook --as m1",
				Effect:  tool.Delivery + ": unhooks the record: the epoch moves on, every lease, proof and challenge is cleared",
				DryRun:  true,
				Detail: `Unhooks you: from now every bus, friend and sprint tool refuses your name until you hook in again, and a
running holder (a hook loop, a friend daemon, a member loop) holds nothing more until someone hooks you in as
its role. Prints UNHOOK OK was=<hooked|unhooked|none> epoch=<n>. --dry-run prints what the record is and writes nothing.
example: nova-hook unhook --as ada`,
				Flags: func(f *tool.Flags) {
					as(f)
					store(f)
				},
				Run: w.unhook,
			},
		},
	}
}

// session is one verb's open store: the bus store, its hook record and the
// identity the verb acts as.
type session struct {
	bus   bus.Store
	hooks hook.Store
	as    string
	close func()
}

// open opens the store a verb names and says who acts (bus.Address,
// bus.Identity), or the result that stops it. needAs false lets a verb run
// with no identity (status without --as).
func (w world) openSession(c *tool.Call, needAs bool) (*session, *tool.Out) {
	ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
	defer cancel()
	addr, refusal := bus.Address(ctx, c.Str("redis"), w.getenv, w.fleetBus)
	if refusal != "" {
		return nil, tool.Refuse(refusal)
	}
	if why := bus.CheckAddr(ctx, addr, w.lookup); why != "" {
		return nil, tool.Refuse(why)
	}
	st, login, closeStore, err := w.open(ctx, addr)
	if err != nil {
		return nil, tool.Refuse(err.Error())
	}
	hs, err := hookgate.Store(st)
	if err != nil {
		closeStore()
		return nil, tool.Refuse(err.Error())
	}
	s := &session{bus: st, hooks: hs, close: closeStore}
	if needAs || c.Str("as") != "" || login != "" {
		as, refusal := bus.Identity(c.Str("as"), login)
		if refusal != "" {
			closeStore()
			return nil, tool.Refuse(refusal)
		}
		s.as = as
	}
	return s, nil
}

// call is a context bounded by one call's deadline.
func call() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), bus.CallTimeout)
}

// line prints one line of a verb that prints as it goes.
func line(c *tool.Call, o *tool.Out) {
	o.Verb = "hook"
	o.Render(c.Stdout, false)
}

// stateLine is the record's state as one HOOK STATE line, with the breadcrumb
// as its notes when it is not proven.
func stateLine(st hook.Status, role hook.Role) *tool.Out {
	o := tool.Done().As("STATE").Fact("state", st.State).Fact("role", string(st.Role)).Fact("epoch", st.Epoch)
	for _, p := range st.Pushes {
		o.Fact(string(p.Push), p.State)
	}
	if st.State != hook.StateProven {
		if r := st.Gate(hook.Want{Name: st.Name, Role: role}); r != nil {
			for _, l := range r.Lines {
				o.Note(l)
			}
		}
	}
	return o
}

// holds is the pushes this process holds for a role: the bus push, when the
// role carries it; every other push has its own holder (hook.HeldBy).
func holds(r hook.Role) []hook.Push {
	if slices.Contains(hook.PushesOf(r), hook.Bus) {
		return []hook.Push{hook.Bus}
	}
	return nil
}

// hook is the verb: arm, hold, challenge, then stream until a signal or an unhook
// (docs/SPEC-HOOK.md, the hook verb; tla/Hook.tla, Arm, Renew and Challenge).
func (w world) hook(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, true)
	if refused != nil {
		return refused
	}
	defer s.close()
	role, err := hook.ParseRole(c.Str("role"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if c.DryRun() {
		ctx, cancel := call()
		defer cancel()
		return w.hookPlan(ctx, s, role)
	}
	held := holds(role)
	// the identity's own word (hook.Store.HookAs): it joins a record hooked as role,
	// and otherwise arms a new epoch: after an unhook, or over a record hooked as
	// another role, which it takes over (a holder's start never does either)
	ctx, cancel := call()
	took := ""
	if st, _, rerr := s.hooks.Read(ctx, s.as); rerr == nil && st[0].Role != role && st[0].State != hook.StateNone && st[0].State != hook.StateUnhooked {
		took = string(st[0].Role)
	}
	epoch, joined, err := s.hooks.HookAs(ctx, s.as, role, w.holder)
	cancel()
	if err != nil {
		return tool.Refuse("the hook record could not be written: " + err.Error())
	}
	if err := w.renew(s, role, held, epoch); err != nil {
		return tool.Refuse("the lease could not be written: " + err.Error())
	}
	// the cursor is read before anything is printed, so a message that lands while the
	// session reads the first lines is a push of this hook (as nova-bus wait arms)
	cursor, refused := w.cursor(s, held)
	if refused != nil {
		return refused
	}
	armed := tool.Done().As("ARMED").Fact("as", s.as).Fact("role", string(role)).Fact("epoch", epoch).Fact("joined", joined).Fact("hold", pushList(held))
	if took != "" {
		armed.Note("the record was hooked as " + took + ": it is " + string(role) + " from now, and the holders of " + took + " hold nothing until it is hooked as " + took + " again")
	}
	for _, p := range hook.PushesOf(role) {
		if !slices.Contains(held, p) {
			armed.Note(fmt.Sprintf("the %s push is held by: %s", p, hook.HeldBy(s.as, role, p)))
		}
	}
	once := c.Bool("once")
	if !once {
		line(c, armed)
	}
	challenge := func(epoch int64) *tool.Out {
		if len(held) == 0 {
			return nil
		}
		nonce := w.nonce()
		ctx, cancel := call()
		defer cancel()
		if err := s.hooks.Challenge(ctx, s.as, role, epoch, held, nonce); err != nil {
			return tool.Refuse("the challenge could not be written: " + err.Error())
		}
		o := tool.Done().As("CHALLENGE").Fact("nonce", nonce).Fact("push", pushList(held)).Fact("within", hook.AnswerBound.String())
		o.Note("run: nova-hook prove --as " + s.as + " --nonce " + nonce)
		return o
	}
	ch := challenge(epoch)
	if ch != nil && ch.Status == tool.Refused {
		return ch
	}
	if once {
		return w.onceResult(s, role, held, armed, ch)
	}
	if ch != nil {
		line(c, ch)
	}
	return w.loop(c, s, role, held, epoch, cursor, challenge)
}

// cursor is the bus stream's last entry id when this process holds the bus
// push ("0-0" for an empty stream): the hook's pushes are the entries after it.
func (w world) cursor(s *session, held []hook.Push) (string, *tool.Out) {
	waiter, ok := s.bus.(bus.Waiter)
	if !ok || !slices.Contains(held, hook.Bus) {
		return "0-0", nil
	}
	ctx, cancel := call()
	defer cancel()
	last, exists, err := waiter.Tail(ctx, bus.StreamOf(s.as))
	if err != nil {
		return "", tool.Refuse("the bus stream could not be read: " + err.Error())
	}
	if !exists || last == "" {
		return "0-0", nil
	}
	return last, nil
}

// renew renews the lease of every push held.
func (w world) renew(s *session, role hook.Role, held []hook.Push, epoch int64) error {
	for _, p := range held {
		ctx, cancel := call()
		err := s.hooks.Renew(ctx, s.as, role, p, epoch, w.holder)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// onceResult is --once's one result: the armed facts, the challenge and the
// state, OK when proven and FAILED with the remedy when not.
func (w world) onceResult(s *session, role hook.Role, held []hook.Push, armed, ch *tool.Out) *tool.Out {
	ctx, cancel := call()
	st, _, err := s.hooks.Read(ctx, s.as)
	cancel()
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	o := tool.Done()
	if st[0].State != hook.StateProven {
		o = tool.Fail(fmt.Sprintf("%s is armed as %s and not proven: %s", s.as, role, st[0].Words()))
		o.Reasons = []string{hookgate.Reason}
		o.Remedy = "nova-hook status --as " + s.as // a role whose holders are daemons: they hook in at their start (the notes name them)
		if len(held) > 0 {
			o.Remedy = hook.HookCommand(s.as, role) + " under the session's Monitor" // this process held a push: hold it for good
		}
	}
	o.Facts = append(o.Facts, armed.Facts...)
	o.Fact("state", st[0].State)
	if ch != nil {
		o.Fact("nonce", ch.Facts[0].V)
		o.Note("prove: nova-hook prove --as " + s.as + " --nonce " + fmt.Sprint(ch.Facts[0].V) + " within " + hook.AnswerBound.String())
	}
	o.Notes = append(o.Notes, armed.Notes...)
	return o
}

// loop holds the bus push and streams it until a signal or an unhook: one
// blocking read of the stream a tick, the lease renewed every RenewEvery, a
// new challenge every ChallengeEvery, and a HOOK STATE line on each change.
// A stream that does not answer is said once while it stays the same fault,
// and the loop waits a tick before it reads again: a store that is down never
// floods the Monitor. A record lost under the loop (none: deleted, or the
// store flushed) is armed again, the session's hook being the identity's word;
// an unhook ends the loop.
func (w world) loop(c *tool.Call, s *session, role hook.Role, held []hook.Push, epoch int64, cursor string, challenge func(int64) *tool.Out) *tool.Out {
	ctx, stop := w.signals(context.Background())
	defer stop()
	stream := bus.StreamOf(s.as)
	waiter, _ := s.bus.(bus.Waiter) // ignored: a store with no wait reads holds no bus push here; its loop only renews
	nextRenew, nextChallenge := w.now().Add(hook.RenewEvery), w.now().Add(hook.ChallengeEvery)
	lastState, unread := "", ""
	pause := func() {
		select {
		case <-ctx.Done():
		case <-time.After(w.tick):
		}
	}
	for {
		if ctx.Err() != nil {
			line(c, tool.Done().As("STOP").Fact("as", s.as).Fact("lapse", hook.LeaseTTL.String()))
			return tool.Exit(0)
		}
		if waiter != nil && slices.Contains(held, hook.Bus) {
			got, err := waiter.BlockRead(ctx, stream, cursor, w.tick, bus.WaitRead)
			switch {
			case err != nil && ctx.Err() == nil:
				if why := err.Error(); why != unread {
					unread = why
					line(c, tool.Done().As("STATE").Fact("state", "unread").Note("the bus stream did not answer: "+why+"; the loop reads it again every "+w.tick.String()+", and says so again only when the fault changes"))
				}
				pause()
			case err == nil && unread != "":
				unread = ""
				line(c, tool.Done().As("STATE").Fact("state", "read").Note("the bus stream answers again"))
			}
			for _, e := range got {
				cursor = e.Entry
				if m := e.Message(); m.From != s.as {
					line(c, tool.Done().As("PUSH").Fact("push", "bus").Fact("id", m.ID).Fact("from", m.From).Fact("subject", tool.Text(m.Subject)))
				}
			}
		} else {
			pause()
		}
		if ctx.Err() != nil {
			continue
		}
		if !w.now().Before(nextRenew) {
			nextRenew = w.now().Add(hook.RenewEvery)
			err := w.renew(s, role, held, epoch)
			if errors.Is(err, hook.ErrNotHooked) {
				if e, ok := w.rearm(s, role, held); ok {
					// the record was lost (none), not unhooked: armed again, a new challenge
					epoch, err = e, nil
					line(c, tool.Done().As("ARMED").Fact("as", s.as).Fact("role", string(role)).Fact("epoch", epoch).Fact("joined", false).Fact("hold", pushList(held)).Note("the hook record was lost under this hook (no record: deleted, or the store flushed); armed again"))
					nextChallenge = w.now()
				}
			}
			if errors.Is(err, hook.ErrNotHooked) {
				o := tool.Fail(s.as + " was unhooked under this hook (nova-hook unhook, or a hook as another role); nothing is held from now")
				o.Reasons = []string{hookgate.Reason}
				o.Remedy = hook.HookCommand(s.as, role)
				return o
			} else if err != nil {
				line(c, tool.Done().As("STATE").Fact("state", "unrenewed").Note("the lease could not be renewed: "+err.Error()+"; it is tried again at the next renewal, inside the lease"))
			}
		}
		if !w.now().Before(nextChallenge) {
			nextChallenge = w.now().Add(hook.ChallengeEvery)
			if ch := challenge(epoch); ch != nil {
				line(c, ch)
			}
		}
		cctx, cancel := call()
		st, _, err := s.hooks.Read(cctx, s.as)
		cancel()
		if err == nil && st[0].Words() != lastState {
			lastState = st[0].Words()
			line(c, stateLine(st[0], role))
		}
	}
}

// rearm arms the record again when it is gone (none) under the loop, and
// renews what the loop holds: ok false when the record is there (unhooked, or
// hooked again since), which ends the loop.
func (w world) rearm(s *session, role hook.Role, held []hook.Push) (int64, bool) {
	ctx, cancel := call()
	st, _, err := s.hooks.Read(ctx, s.as)
	cancel()
	if err != nil || st[0].State != hook.StateNone {
		return 0, false
	}
	ctx, cancel = call()
	epoch, _, err := s.hooks.HookAs(ctx, s.as, role, w.holder)
	cancel()
	if err != nil || w.renew(s, role, held, epoch) != nil {
		return 0, false
	}
	return epoch, true
}

// prove is the verb: the answer to the hook's challenge (tla/Hook.tla, Answer).
func (w world) prove(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, true)
	if refused != nil {
		return refused
	}
	defer s.close()
	ctx, cancel := call()
	defer cancel()
	if c.DryRun() {
		return w.provePlan(ctx, s)
	}
	n, err := s.hooks.Answer(ctx, s.as, c.Str("nonce"), "session")
	switch {
	case errors.Is(err, hook.ErrNoChallenge), errors.Is(err, hook.ErrWrongNonce):
		o := tool.Fail(err.Error())
		o.Remedy = "nova-hook status --as " + s.as
		return o
	case errors.Is(err, hook.ErrNotHooked):
		o := tool.Fail(s.as + " was unhooked or hooked again since the challenge; its hook prints a new one")
		o.Remedy = "nova-hook status --as " + s.as
		return o
	case err != nil:
		return tool.Refuse("the proof could not be written: " + err.Error())
	}
	st, _, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	return tool.Done().Fact("proven", n).Fact("state", st[0].State).Fact("role", string(st[0].Role))
}

// provePlan is prove --dry-run's result: it reads the record and prints what
// the answer would prove -- each push of the record's role whose lease is live
// (the pushes a challenge can name) -- and writes nothing.
func (w world) provePlan(ctx context.Context, s *session) *tool.Out {
	st, _, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	live := 0
	for _, p := range st[0].Pushes {
		if p.State != hook.PushDown {
			live++
		}
	}
	return tool.Done().As("DRY-RUN").Fact("as", s.as).Fact("state", st[0].State).Fact("role", string(st[0].Role)).Fact("would-prove", live)
}

// hookPlan is hook --dry-run's result: it reads the record and prints the plan
// the real run would take -- the role, the epoch (joined at the record's, else
// the next), the pushes this process would hold, and each other push's holder
// -- and writes nothing.
func (w world) hookPlan(ctx context.Context, s *session, role hook.Role) *tool.Out {
	st, _, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	cur := st[0]
	joined := cur.Role == role && cur.State != hook.StateNone && cur.State != hook.StateUnhooked
	epoch := cur.Epoch + 1
	if joined {
		epoch = cur.Epoch
	}
	if epoch < 1 {
		epoch = 1
	}
	held := holds(role)
	o := tool.Done().As("DRY-RUN").Fact("as", s.as).Fact("role", string(role)).Fact("epoch", epoch).Fact("joined", joined).Fact("hold", pushList(held))
	for _, p := range hook.PushesOf(role) {
		if !slices.Contains(held, p) {
			o.Note(fmt.Sprintf("the %s push is held by: %s", p, hook.HeldBy(s.as, role, p)))
		}
	}
	return o
}

// unhook is the verb (tla/Hook.tla, Unhook).
func (w world) unhook(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, true)
	if refused != nil {
		return refused
	}
	defer s.close()
	ctx, cancel := call()
	defer cancel()
	if c.DryRun() {
		st, _, err := s.hooks.Read(ctx, s.as)
		if err != nil {
			return tool.Refuse("the hook record could not be read: " + err.Error())
		}
		return tool.Done().As("DRY-RUN").Fact("as", s.as).Fact("state", st[0].State).Fact("epoch", st[0].Epoch)
	}
	was, err := s.hooks.Unhook(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be written: " + err.Error())
	}
	st, _, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	o := tool.Done().Fact("as", s.as).Fact("was", was).Fact("epoch", st[0].Epoch)
	if was != hook.StateNone {
		o.Note("hook in again: nova-hook hook --as " + s.as + " --role " + string(st[0].Role))
	}
	return o
}

// status is the verb: one name's record as a check, or every name's.
func (w world) status(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, false)
	if refused != nil {
		return refused
	}
	defer s.close()
	ctx, cancel := call()
	defer cancel()
	if s.as != "" {
		st, _, err := s.hooks.Read(ctx, s.as)
		if err != nil {
			return tool.Refuse("the hook record could not be read: " + err.Error())
		}
		if r := st[0].Gate(hook.Want{Name: s.as, Role: st[0].Role}); r != nil {
			o := hookgate.Out(r)
			o.Facts = statusFacts(st[0])
			return o
		}
		o := tool.Done()
		o.Facts = statusFacts(st[0])
		return o
	}
	names, err := s.hooks.Names(ctx)
	if err != nil {
		return tool.Refuse("the hook names could not be read: " + err.Error())
	}
	sts, _, err := s.hooks.Read(ctx, names...)
	if err != nil {
		return tool.Refuse("the hook record could not be read: " + err.Error())
	}
	proven := 0
	o := tool.Done()
	for _, st := range sts {
		if st.State == hook.StateProven {
			proven++
		}
		o.Item("name", fieldsOf(statusFacts(st))...)
	}
	return o.Fact("count", len(sts)).Fact("proven", proven)
}

// statusFacts is a record's facts: name, state, role, epoch and each push.
func statusFacts(st hook.Status) tool.Fields {
	o := tool.Done().Fact("name", st.Name).Fact("state", st.State).Fact("role", string(st.Role)).Fact("epoch", st.Epoch)
	for _, p := range st.Pushes {
		o.Fact(string(p.Push), p.State)
	}
	return o.Facts
}

func fieldsOf(fs tool.Fields) []any {
	var kv []any
	for _, f := range fs {
		kv = append(kv, f.K, f.V)
	}
	return kv
}
