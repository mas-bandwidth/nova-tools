package main

import (
	"context"
	"io"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2/server"
	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/hook/hooktest"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rig is the tool over one hooktest store whose roster names ada and bob, on
// a clock that moves only when a test moves it, with signals a test cancels.
type rig struct {
	h      *hooktest.Rig
	mu     sync.Mutex
	clock  time.Time
	cancel context.CancelFunc
	nonces int
	home   string // HOME as the tool reads it: a test's own directory
	// failTells makes every judgment and told message fail to send, as a bus
	// that does not answer would; challenges still go out.
	failTells atomic.Bool
	// loseAnswers sends every judgment and told message and then answers an
	// error, as a send whose reply was lost after the store took it.
	loseAnswers atomic.Bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	h := hooktest.New(t)
	require.NoError(t, h.Client.SAdd(context.Background(), "friends", "ada", "bob").Err())
	return &rig{h: h, home: t.TempDir(), clock: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
}

// TestHookHelpClarifiesEnforcementScope pins the distinction between a strict
// acting-identity gate and nova-bus's advisory mailbox proof, plus the limits
// of what answering a hook challenge proves.
func TestHookHelpClarifiesEnforcementScope(t *testing.T) {
	t.Parallel()
	cli := newRig(t).cli()

	mainHelp := cli.Run("--help")
	require.Zero(t, mainHelp.Code, mainHelp.Stdout+mainHelp.Stderr)
	for _, want := range []string{
		"strict gates require the acting name's role push to be live and proven.",
		"nova-bus send/recv note unproven mailboxes unless --require-push or NOVA_BUS_REQUIRE_PUSH=1.",
		"a hook proof is a protocol response only, not foreground presence or native idle-wake evidence.",
	} {
		assert.Contains(t, mainHelp.Stdout, want)
	}

	hookHelp := cli.Run("hook", "--help")
	require.Zero(t, hookHelp.Code, hookHelp.Stdout+hookHelp.Stderr)
	assert.Contains(t, hookHelp.Stdout, "nova-hook hook --as <me> --role <role> [--once]")
	assert.Contains(t, hookHelp.Stdout, "This proof records a protocol response to the challenge")
	assert.Contains(t, hookHelp.Stdout, "it does not establish foreground session presence or native idle-wake behavior.")
}

func (r *rig) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clock
}

func (r *rig) advance(d time.Duration) {
	r.mu.Lock()
	r.clock = r.clock.Add(d)
	r.mu.Unlock()
}

func (r *rig) world() world {
	return world{
		getenv: func(k string) string {
			if k == bus.RedisEnv {
				return "127.0.0.1:6379"
			}
			if k == "HOME" {
				return r.home
			}
			return ""
		},
		open: func(context.Context, string) (bus.Store, string, func(), error) {
			return bus.Redis{C: r.h.Client}, "", func() {}, nil
		},
		fleetBus: func(context.Context, string) (string, error) { return "", nil },
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.AddrFrom4([4]byte{127, 0, 0, 1})}, nil
		},
		now: r.now,
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			// a bound only so a broken loop fails the test and never hangs it; every
			// test that loops ends it on its own event (a push, an unhook)
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			r.cancel = cancel
			return ctx, cancel
		},
		nonce: func() string {
			r.nonces++
			return "nonce" + strings.Repeat("x", r.nonces)
		},
		holder: "test",
		tick:   time.Millisecond,
		after:  func(time.Duration) <-chan time.Time { return nil }, // serve's tests run --once
		deliver: func(b *bus.Bus, m bus.Message) (bus.Message, error) {
			if r.failTells.Load() && !strings.HasPrefix(m.Subject, "HOOK CHALLENGE") {
				return bus.Message{}, context.DeadlineExceeded
			}
			ctx, cancel := context.WithTimeout(context.Background(), bus.CallTimeout)
			defer cancel()
			sent, err := b.Send(ctx, m)
			if err == nil && r.loseAnswers.Load() && !strings.HasPrefix(m.Subject, "HOOK CHALLENGE") {
				return bus.Message{}, context.DeadlineExceeded
			}
			return sent, err
		},
	}
}

func (r *rig) cli() testkit.Main {
	w := r.world()
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}

var nonceRe = regexp.MustCompile(`nonce=(\S+)`)

// TestHookProveStatusUnhook pins the verbs on the record (tla/Hook.tla): a
// name never hooked is refused by status with the breadcrumb; hook --once arms
// it live and unproven and prints the challenge; prove with that nonce makes it
// proven; status lists it; unhook ends it, and status refuses it again.
func TestHookProveStatusUnhook(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cli := r.cli()
	res := cli.Run("status", "--as", "ada")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "STATUS FAILED name=ada state=none")
	assert.Contains(t, res.Stdout+res.Stderr, "reason=unhooked: ada is not hooked in: no hook record")
	assert.Contains(t, res.Stdout+res.Stderr, "; run: nova-hook status --as ada", "no role known: the remedy looks, it never guesses the coordinator")
	assert.Contains(t, res.Stdout+res.Stderr, "NOTE hook in as comms (on the bus, taking no sprint work): nova-hook hook --as ada --role comms")

	res = cli.Run("hook", "--as", "ada", "--role", "comms", "--once")
	require.Equal(t, 1, res.Code, res.Stdout+res.Stderr)
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "HOOK FAILED as=ada role=comms epoch=1 joined=false hold=bus state=live nonce=")
	assert.Contains(t, out, "reason=unhooked: ada is armed as comms and not proven")
	m := nonceRe.FindStringSubmatch(out)
	require.NotNil(t, m, out)

	res = cli.Run("prove", "--as", "ada", "--nonce", "wrong")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "PROVE FAILED: the nonce is not the one the challenge asked")
	cli.Do(t, "prove", "--as", "ada", "--nonce", m[1]).Exit(0).Out("PROVE OK proven=1 state=proven role=comms")
	res = cli.Run("prove", "--as", "ada", "--nonce", m[1])
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "no challenge stands")
	r.h.Evidence(t, "ada") // a heartbeat server passing and its answer (hook.Served.Down)
	cli.Do(t, "status", "--as", "ada").Exit(0).Out("STATUS OK name=ada state=proven role=comms epoch=1 bus=proven")
	cli.Do(t, "status").Exit(0).Out("STATUS OK count=1 proven=1", "STATUS NAME name=ada state=proven")

	cli.Do(t, "unhook", "--as", "ada", "--dry-run").Exit(0).Out("UNHOOK DRY-RUN as=ada state=proven epoch=1")
	assert.Equal(t, hook.StateProven, r.h.State(t, "ada").State, "a dry run writes nothing")
	cli.Do(t, "unhook", "--as", "ada").Exit(0).Out("UNHOOK OK as=ada was=hooked epoch=2", "hook in again: nova-hook hook --as ada --role comms")
	res = cli.Run("status", "--as", "ada")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "ada is not hooked in: unhooked at")
	cli.Do(t, "unhook", "--as", "bob").Exit(0).Out("UNHOOK OK as=bob was=none")
}

// TestProveDryRunPlansAndWritesNothing pins prove --dry-run: it reads the
// record, prints what the answer would prove, and leaves the record's state as
// it found it (no proof is written).
func TestProveDryRunPlansAndWritesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cli := r.cli()
	res := cli.Run("hook", "--as", "ada", "--role", "comms", "--once")
	require.Equal(t, 1, res.Code, res.Stdout+res.Stderr)
	m := nonceRe.FindStringSubmatch(res.Stdout + res.Stderr)
	require.NotNil(t, m, res.Stdout+res.Stderr)
	before := r.h.State(t, "ada")
	require.Equal(t, hook.StateLive, before.State)

	res = cli.Run("prove", "--as", "ada", "--nonce", m[1], "--dry-run")
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	assert.Contains(t, res.Stdout, "PROVE DRY-RUN as=ada state=live role=comms would-prove=1")
	assert.Equal(t, before.State, r.h.State(t, "ada").State, "a dry run writes no proof")

	cli.Do(t, "prove", "--as", "ada", "--nonce", m[1]).Exit(0).Out("PROVE OK proven=1")
	assert.Equal(t, hook.StateProven, r.h.State(t, "ada").State, "the nonce still stands after a dry run")
}

// TestHookDryRunPlansAndWritesNothing pins hook --dry-run: it prints the plan
// the real run would take and writes no record or lease.
func TestHookDryRunPlansAndWritesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cli := r.cli()
	cli.Do(t, "hook", "--as", "bob", "--role", "member", "--dry-run").
		Exit(0).Out("HOOK DRY-RUN as=bob role=member epoch=1 joined=false hold=-")
	names, err := r.h.Store.Names(context.Background())
	require.NoError(t, err)
	assert.Empty(t, names, "a dry run armed nothing")

	cli.Do(t, "hook", "--as", "ada", "--role", "comms", "--once")
	before := r.h.State(t, "ada")
	cli.Do(t, "hook", "--as", "ada", "--role", "comms", "--dry-run").
		Exit(0).Out("HOOK DRY-RUN as=ada role=comms epoch=1 joined=true hold=bus")
	assert.Equal(t, before.State, r.h.State(t, "ada").State, "a dry run renewed no lease and wrote no record")
}

// TestARoleOfDaemonsIsArmedAndNamesItsHolders pins hook for a role whose
// pushes this process does not hold: it arms the record, holds nothing, and
// names each push's holder.
func TestARoleOfDaemonsIsArmedAndNamesItsHolders(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	res := r.cli().Run("hook", "--as", "bob", "--role", "member", "--once")
	assert.Equal(t, 1, res.Code)
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "HOOK FAILED as=bob role=member epoch=1 joined=false hold=- state=expired reason=unhooked")
	assert.Contains(t, out, "the work push is held by: the member loop, nova-swarm member --as bob")
	assert.Equal(t, hook.Member, r.h.State(t, "bob").Role)
}

// streamOut is a writer that hands every line to a callback as it is written:
// the test's view of a hook's stream, as a Monitor reads it.
type streamOut struct {
	mu   sync.Mutex
	all  strings.Builder
	each func(line string)
}

func (s *streamOut) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.all.Write(p)
	s.mu.Unlock()
	for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		s.each(l)
	}
	return len(p), nil
}

// TestTheHookStreamsPushesUntilASignal pins the loop: it prints the challenge,
// the session answers it from the stream, a message to the name is printed as
// a push, and a signal stops it at exit 0 with the record proven.
func TestTheHookStreamsPushesUntilASignal(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	sent := false
	out := &streamOut{}
	out.each = func(l string) {
		switch {
		case strings.HasPrefix(l, "HOOK CHALLENGE") && !sent:
			sent = true
			m := nonceRe.FindStringSubmatch(l)
			_, err := r.h.Store.Answer(ctx, "ada", m[1], "test")
			assert.NoError(t, err)
			_, err = b.Send(ctx, bus.Message{From: "bob", To: []string{"ada"}, Subject: "hello", Body: "x"})
			assert.NoError(t, err)
		case strings.HasPrefix(l, "HOOK PUSH"):
			r.cancel()
		}
	}
	code := run([]string{"hook", "--as", "ada", "--role", "comms"}, strings.NewReader(""), out, io.Discard, r.world())
	assert.Equal(t, 0, code, out.all.String())
	assert.Contains(t, out.all.String(), "HOOK ARMED as=ada role=comms epoch=1 joined=false hold=bus")
	assert.Contains(t, out.all.String(), `HOOK PUSH push=bus id=`)
	assert.Contains(t, out.all.String(), `from=bob subject="hello"`)
	assert.Contains(t, out.all.String(), "HOOK STOP as=ada lapse=1m30s")
	assert.Equal(t, hook.StateProven, r.h.State(t, "ada").State)
}

// TestTheHookEndsWhenUnhookedUnderIt pins that an unhook stands against a
// running hook: at its next renewal it holds nothing more and exits 1 naming
// the command that hooks in again.
func TestTheHookEndsWhenUnhookedUnderIt(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	out := &streamOut{}
	done := false
	out.each = func(l string) {
		switch {
		case strings.HasPrefix(l, "HOOK CHALLENGE") && !done:
			done = true
			r.h.Unhook(t, "ada")
		case strings.HasPrefix(l, "HOOK STATE state=unhooked"):
			r.advance(hook.RenewEvery) // the loop's next renewal is due: it meets the unhook
		}
	}
	var errb strings.Builder
	code := run([]string{"hook", "--as", "ada", "--role", "coordinator"}, strings.NewReader(""), out, &errb, r.world())
	assert.Equal(t, 1, code)
	assert.Contains(t, out.all.String()+errb.String(), "ada was unhooked under this hook")
	assert.Contains(t, out.all.String()+errb.String(), "; run: nova-hook hook --as ada --role coordinator")
	assert.Contains(t, out.all.String(), "the sprint push is held by: the sprint's server (nova-sprint run), which holds it for the coordinator while it runs and reaches the bus")
	assert.Equal(t, hook.StateUnhooked, r.h.State(t, "ada").State, "the hook wrote nothing after the unhook")
}

// TestARoleThatIsNoneIsRefused pins the role's refusal before anything is written.
func TestARoleThatIsNoneIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.cli().Do(t, "hook", "--as", "ada", "--role", "seat").Exit(2).Err(`--role "seat" is no role; it wants one of coordinator, friend, comms, member, reader`)
	names, err := r.h.Store.Names(context.Background())
	require.NoError(t, err)
	assert.Empty(t, names, "nothing was armed")
}

// TestHookToolMeetsTheStandard holds the tool to pkg/tool's standard: every
// verb's help, flags and effect (Problems).
func TestHookToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, hookTool(newRig(t).world()).Problems())
}

// TestHookAsAnotherRoleTakesTheRecordOver pins the identity's own hook as
// another role: the record is taken over, a new epoch, and the line says so.
func TestHookAsAnotherRoleTakesTheRecordOver(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, _, err := r.h.Store.Arm(context.Background(), "bob", hook.Friend, "daemon", false)
	require.NoError(t, err)
	res := r.cli().Run("hook", "--as", "bob", "--role", "member", "--once")
	assert.Contains(t, res.Stdout+res.Stderr, "role=member epoch=2 joined=false")
	assert.Contains(t, res.Stdout+res.Stderr, "the record was hooked as friend: it is member from now")
	assert.Equal(t, hook.Member, r.h.State(t, "bob").Role)
}

// TestAStoreThatIsDownNeverFloodsTheHook pins the loop against a store that
// answers every read with an error: the fault is said once while it stays the
// same, the loop waits a tick between reads, and it says when the stream
// answers again. The store fails the first 50 stream reads after the
// challenge, and the run is stopped at the 60th: no wall clock in the test.
func TestAStoreThatIsDownNeverFloodsTheHook(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	var mu sync.Mutex
	var stop context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		mu.Lock()
		stop = cancel
		mu.Unlock()
		return ctx, cancel
	}
	var armed atomic.Bool
	var reads atomic.Int64
	r.h.MR.Server().SetPreHook(func(c *server.Peer, cmd string, _ ...string) bool {
		if !armed.Load() || !strings.EqualFold(cmd, "XREAD") {
			return false
		}
		switch n := reads.Add(1); {
		case n <= 50:
			c.WriteError("ERR the store is down")
			return true
		case n == 60:
			mu.Lock()
			stop()
			mu.Unlock()
		}
		return false
	})
	out := &streamOut{}
	out.each = func(l string) {
		if strings.HasPrefix(l, "HOOK CHALLENGE") {
			armed.Store(true)
		}
	}
	code := run([]string{"hook", "--as", "ada", "--role", "comms"}, strings.NewReader(""), out, io.Discard, w)
	assert.Equal(t, 0, code, out.all.String())
	all := out.all.String()
	assert.GreaterOrEqual(t, reads.Load(), int64(60), "the loop read on after the fault")
	assert.Equal(t, 1, strings.Count(all, "HOOK STATE state=unread"), all)
	assert.Contains(t, all, "the bus stream did not answer: ERR the store is down")
	assert.Equal(t, 1, strings.Count(all, "HOOK STATE state=read"), all)
}

// TestALostRecordIsArmedAgainUnderTheHook pins that the session's hook arms a
// record lost under it (none: the store flushed) at its next renewal, and is
// not ended by it as by an unhook.
func TestALostRecordIsArmedAgainUnderTheHook(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	out := &streamOut{}
	flushed := false
	out.each = func(l string) {
		switch {
		case strings.HasPrefix(l, "HOOK CHALLENGE") && !flushed:
			flushed = true
			r.h.MR.FlushAll()
		case strings.HasPrefix(l, "HOOK STATE state=none"):
			r.advance(hook.RenewEvery) // the loop saw the record gone: its next renewal is due
		case strings.Contains(l, "was lost under this hook"):
			r.cancel()
		}
	}
	code := run([]string{"hook", "--as", "ada", "--role", "comms"}, strings.NewReader(""), out, io.Discard, r.world())
	assert.Equal(t, 0, code, out.all.String())
	st := r.h.State(t, "ada")
	assert.Equal(t, hook.Comms, st.Role)
	assert.Equal(t, hook.StateLive, st.State, "armed again and its lease renewed; the new challenge waits for its answer")
}
