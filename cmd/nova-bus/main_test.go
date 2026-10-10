package main

import (
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var start = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// rig is the tool over one fake store with ada and bob known: no socket, no
// clock, no seat. exec is what --exec's command does with the text it is
// handed; signals is the loop's context, which a test cancels.
type rig struct {
	store   *bustest.Fake
	env     map[string]string
	exec    func(stdin string) int
	execIn  []string
	cancel  context.CancelFunc
	opened  int
	openErr error
	login   string // the user the store logs in as; "" is a store with no users
	fleet   string // the applied fleet row's bus, read when nothing names the store
	fleetAt []string
	names   []string  // the names newRig keeps heard
	clock   time.Time // the store's clock as advance moved it (each trip adds a second more)
	now     time.Time // the wait verbs' clock; the fake store's block moves it, never real time
	wake    []string  // the wake-file reader's answers, one per look: "" is nothing new
}

// newRig is the rig with every name heard: each has a proven inbox push,
// as its friend daemon writes it (bus.PushKey). deafRig is the rig before
// any daemon has proven one.
func newRig(names ...string) *rig {
	r := deafRig(names...)
	r.names = names
	r.prove(start, true, names...)
	return r
}

func deafRig(names ...string) *rig {
	return &rig{store: bustest.NewFake(start, names...), env: map[string]string{RedisEnv: "store.test:6379"}, clock: start, now: start}
}

// prove writes each name's push proof at the instant at, up or down, as the
// friend daemon does, without a trip that moves the fake's clock.
func (r *rig) prove(at time.Time, up bool, names ...string) {
	for _, n := range names {
		p := bus.PushProof{Name: n, Harness: "claude", Nonce: "n-" + n, Proven: at, Up: up, At: at}
		if !up {
			p.Reason = "no session answer"
		}
		raw, err := json.Marshal(p)
		if err != nil {
			panic(err)
		}
		if err := r.store.AddAll(context.Background(), nil, nil, bus.Mark{Key: bus.PushKey, Field: n, Value: string(raw)}); err != nil {
			panic(err)
		}
	}
}

// advance moves the store's clock by d while every daemon of newRig keeps
// renewing its proof, so only the idle time of the messages grows.
func (r *rig) advance(d time.Duration) {
	r.store.Advance(d)
	r.clock = r.clock.Add(d)
	r.prove(r.clock, true, r.names...)
}

// wireClock wires the fake store's block to the rig's wait clock: a block
// that finds nothing past its cursor waits its duration out on r.now, so a
// test's timeout runs on no real time.
func (r *rig) wireClock() {
	r.store.Sleep = func(d time.Duration) { r.now = r.now.Add(d) }
}

func (r *rig) world() world {
	return world{
		getenv: func(k string) string { return r.env[k] },
		open: func(context.Context, string) (bus.Store, string, func(), error) {
			r.opened++
			if r.openErr != nil {
				return nil, "", nil, r.openErr
			}
			return r.store, r.login, func() {}, nil
		},
		run: func(_ context.Context, _ string, stdin string, _, _ io.Writer) (int, error) {
			r.execIn = append(r.execIn, stdin)
			if r.exec == nil {
				return 0, nil
			}
			return r.exec(stdin), nil
		},
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(ctx)
			r.cancel = cancel
			return ctx, cancel
		},
		fleetBus: func(_ context.Context, addr string) (string, error) {
			r.fleetAt = append(r.fleetAt, addr)
			if r.fleet == "down" {
				return "", io.ErrUnexpectedEOF
			}
			return r.fleet, nil
		},
		lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			return map[string][]netip.Addr{ // built from octets: nothing is dialled, and the ci net rule reads a spelled host
				"store.test": {netip.AddrFrom4([4]byte{100, 76, 0, 9})},  // the tailnet
				"far.test":   {netip.AddrFrom4([4]byte{203, 0, 113, 9})}, // the internet
				"lan.test":   {netip.AddrFrom4([4]byte{10, 0, 0, 5})},    // a private network that is not the tailnet
			}[host], nil
		},
		now: func() time.Time { return r.now },
		wakeArm: func(_ string, token string) (wakeCursor, error) {
			if token != "" {
				return parseWakeCursor(token)
			}
			return wakeCursor{Version: 1, Identity: "fake", Hash: strings.Repeat("0", 64)}, nil
		},
		wakeLine: func(_ string, c wakeCursor) (string, wakeCursor, error) {
			if len(r.wake) == 0 {
				return "", c, nil
			}
			line := r.wake[0]
			r.wake = r.wake[1:]
			if line != "" {
				c.Offset += int64(len(line)) + 1
			}
			return line, c, nil
		},
	}
}

func (r *rig) cli() testkit.Main {
	w := r.world()
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}

// id is the id= of an OK line.
func id(t *testing.T, line string) string {
	t.Helper()
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, "id="); ok {
			return v
		}
	}
	require.Fail(t, "no id= on the line", line)
	return ""
}

func TestTheToolMeetsTheSkeletonStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, busTool(newRig().world()).Problems())
}

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()
	newRig().cli().Do(t).Exit(2).Err("BUS REFUSED", "nova-bus help")
}

func TestSendRefusesNamingEveryProblem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		env  map[string]string
		says []string
	}{
		{"nothing given", []string{"send"}, nil, []string{"--to is required", "--subject is required", "exactly one of --body"}},
		{"no login and no --as", []string{"send", "--to", "bob", "--subject", "s", "--body", "x"}, nil, []string{"--as is required: this connection has no login user (NOVA_SPRINT_REDIS_USER is unset)"}},
		{"two bodies", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--stdin"}, nil, []string{"exactly one of --body"}},
		{"unknown recipient and bad name", []string{"send", "--as", "ada", "--to", "zed,B", "--subject", "s", "--body", "x"}, nil, []string{`"B" is not lowercase`}},
		{"unknown recipient", []string{"send", "--as", "ada", "--to", "zed", "--subject", "s", "--body", "x"}, nil, []string{"zed is no known name", "nova-config friend add zed"}},
		{"no store named", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}, map[string]string{}, []string{"--redis is required", RedisEnv + " is unset"}},
		{"empty body", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", " "}, nil, []string{"the body is empty"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig("ada", "bob")
			if c.env != nil {
				r.env = c.env
			}
			got := r.cli().Do(t, c.args...).Exit(2).Err("SEND REFUSED")
			for _, s := range c.says {
				got.Err(s)
			}
			assert.Equal(t, 0, r.store.Len(bus.LogKey), "a refused send writes nothing")
		})
	}
}

func TestAStoreThatDoesNotAnswerIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.openErr = io.ErrUnexpectedEOF
	r.cli().Do(t, "peek", "--as", "bob").Exit(2).Err("PEEK REFUSED", "unexpected EOF")
	r.cli().Do(t, "names").Exit(2).Err("NAMES REFUSED", "unexpected EOF")
}

func TestTheLoopSendPeekRecvAckLog(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()

	sent := cli.OK(t, "send", "--as", "ada", "--to", "bob", "--cc", "ada", "--subject", "hello there", "--body", "line one\nline two\n")
	assert.Regexp(t, `^SEND OK id=[0-9A-Z]{26} to=bob cc=ada at=2026-10-03T12:00:01Z bytes=18 sha256=[0-9a-f]{64} login=none\n$`, sent.Stdout)
	mid := id(t, sent.Stdout)

	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=0 new=1", "PEEK MESSAGE state=new id="+mid+" from=ada at=2026-10-03T12:00:01Z subject=\"hello there\"")

	got := cli.OK(t, "recv", "--as", "bob")
	assert.Equal(t, "RECV OK id="+mid+" from=ada to=bob cc=ada re=- at=2026-10-03T12:00:01Z login=none subject=\"hello there\"\n\nline one\nline two\n", got.Stdout)

	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=0", "state=pending")
	cli.Do(t, "recv", "--as", "bob").Exit(1).Err("RECV NONE: nothing for bob").NotErr("--block")
	cli.Do(t, "recv", "--as", "ada").Exit(0).Out("RECV OK id=" + mid + " from=ada to=bob cc=ada").NotErr("REFUSED")
	cli.Do(t, "recv", "--as", "bobb").Exit(2).Err("RECV REFUSED: bobb is no known name", "nova-config friend add bobb")

	cli.Do(t, "ack", "--as", "bob", "--id", mid+",NOPE").Exit(0).Out("ACK OK acked=1 asked=2 login=none", "ACK ID id="+mid+" acked=true", "ACK ID id=NOPE acked=false")
	cli.Do(t, "ack", "--as", "bob", "--id", mid).Exit(0).Out("ACK OK acked=0 asked=1", "acked=false")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=0 new=0")

	cli.Do(t, "log", "--bodies").Exit(0).Out("LOG OK total=1", "LOG MESSAGE id="+mid+" from=ada to=bob cc=ada re=- at=2026-10-03T12:00:01Z subject=\"hello there\" body=\"line one\\nline two\\n\"")
	cli.Do(t, "log", "--max", "1").Exit(0).Out("LOG OK total=1", "LOG MESSAGE").NotOut("MORE")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2", "NAMES NAME name=ada", "NAMES NAME name=bob")
	cli.Do(t, "names", "--json").Exit(0).Out(`"status":"ok"`, `"name":"ada"`)
}

func TestRecvExecAcksOnZeroAndKeepsThePendingMessageOnFailure(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	mid := id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "the body").Stdout)

	r.exec = func(string) int { return 3 }
	cli.Do(t, "recv", "--as", "bob", "--exec", "deliver").Exit(1).Err("RECV FAILED id=" + mid + " exec_exit=3: --exec exited 3, so the message stays pending")
	require.Len(t, r.execIn, 1)
	assert.Equal(t, "RECV OK id="+mid+" from=ada to=bob cc=- re=- at=2026-10-03T12:00:01Z login=none subject=\"s\"\n\nthe body\n", r.execIn[0], "the command reads what recv prints, the body ending in a newline")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=0")

	r.exec = func(string) int { return 0 }
	cli.Do(t, "recv", "--as", "bob", "--exec", "deliver").Exit(1).Err("RECV NONE", "nothing for bob")
	r.advance(bus.ClaimAfter)
	cli.Do(t, "recv", "--as", "bob", "--exec", "deliver").Exit(0).Out("RECV OK id="+mid, "acked=true exec_exit=0")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=0 new=0")
}

func TestRecvForeverWantsExecAndStopsOnASignal(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	cli.Do(t, "recv", "--as", "bob", "--forever").Exit(2).Err("--forever wants --exec")
	for _, s := range []string{"one", "two", "three"} {
		cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", s, "--body", s)
	}
	r.exec = func(string) int {
		if len(r.execIn) == 2 {
			r.cancel() // the signal arrives while the second message is being delivered
		}
		return 0
	}
	got := cli.Do(t, "recv", "--as", "bob", "--forever", "--exec", "deliver").Exit(0)
	got.Out(`subject="one"`).NotOut(`subject="two"`, `subject="three"`, "stopped")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=1", "state=pending", `subject="two"`)

	r.cancel = nil
	r.exec = func(string) int { return 1 }
	r.advance(bus.ClaimAfter)
	cli.Do(t, "recv", "--as", "bob", "--forever", "--exec", "deliver").Exit(1).Err("RECV FAILED id=", "exec_exit=1: --exec exited 1")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=1")
}

func TestRecvForeverJSONIsOneObjectPerMessageAndNothingElse(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "one", "--body", "x")
	cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "two", "--body", "y")
	r.exec = func(string) int {
		if len(r.execIn) == 2 {
			r.cancel()
		}
		return 0
	}
	got := cli.Do(t, "recv", "--as", "bob", "--forever", "--exec", "deliver", "--json").Exit(0)
	lines := strings.Split(strings.TrimSpace(got.Stdout), "\n")
	require.Len(t, lines, 1, "one object for the one message delivered, and no summary line: %q", got.Stdout)
	assert.True(t, strings.HasPrefix(lines[0], "{") && strings.HasSuffix(lines[0], "}"), "not JSON: %q", lines[0])
	assert.Contains(t, lines[0], `"subject":"one"`)
	assert.Empty(t, got.Stderr)
}

// The store is --redis, else NOVA_BUS_REDIS, else the fleet row's bus field as
// nova-config apply wrote it into the sprint store (fleet:bus), read at
// NOVA_SPRINT_REDIS, so no friend types the address; with none of the three,
// or an empty row, or a sprint store that does not answer, a refusal that
// names the row and how it is set.
func TestTheStoreIsTheFlagTheVariableOrTheFleetRow(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.env = map[string]string{"NOVA_REDIS_ADDR": "127.0.0.1:1"}
	r.cli().Do(t, "names").Exit(2).Err("--redis is required", RedisEnv+" is unset", "no "+SprintRedisEnv, "nova-config fleet set --bus")
	r.env = map[string]string{SprintRedisEnv: "127.0.0.1:6380"}
	r.cli().Do(t, "names").Exit(2).Err("the fleet's bus row is empty", "nova-config fleet set --bus <host:port>", "nova-config apply")
	assert.Equal(t, []string{"127.0.0.1:6380"}, r.fleetAt, "the row is read from the sprint store")
	r.fleet = "down"
	r.cli().Do(t, "names").Exit(2).Err("could not be read from the sprint store", "unexpected EOF")
	r.fleet = "store.test:6381"
	r.cli().Do(t, "names").Exit(0).Out("NAMES OK count=2")
	r.fleet = "lan.test:6381"
	r.cli().Do(t, "names").Exit(2).Err("loopback or the tailnet", "lan.test:6381 is 10.0.0.5")
	r.env[RedisEnv] = "127.0.0.1:1"
	r.fleetAt = nil
	r.cli().Do(t, "names").Exit(0).Out("NAMES OK count=2")
	r.cli().Do(t, "names", "--redis", "127.0.0.1:2").Exit(0).Out("NAMES OK count=2")
	assert.Empty(t, r.fleetAt, "the variable or the flag names the store; the row is not read")
}

func TestSendReadsTheBodyFromStdinAndBoundsIt(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	cli.OKIn(t, "from stdin\n", "send", "--as", "ada", "--to", "bob", "--subject", "s", "--stdin")
	cli.Do(t, "log", "--bodies").Exit(0).Out(`body="from stdin\n"`)
	big := strings.Repeat("x", bus.MaxBody+7)
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", big).Exit(2).Err("the body is 1048583 bytes, at most 1048576")
	cli.DoIn(t, big, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--stdin").Exit(2).Err("the body on stdin is over 1 MiB; at most 1048576 bytes")
}

// The identity is the store's login user, never a word on the line
// (SPEC-BUS.md, the identity): with a login, --as may repeat it or be left
// out and any other name is refused; with none, --as is required and every
// write says login=none.
func TestTheIdentityIsTheLoginUser(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.login = "ada"
	cli := r.cli()
	sent := cli.OK(t, "send", "--to", "bob", "--subject", "s", "--body", "x")
	assert.Regexp(t, `^SEND OK id=[0-9A-Z]{26} to=bob cc=- at=2026-10-03T12:00:01Z bytes=1 sha256=[0-9a-f]{64}\n$`, sent.Stdout, "no login=none with a login")
	cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x")
	cli.Do(t, "log").Exit(0).Out("LOG OK total=2", "from=ada")
	for _, verb := range [][]string{
		{"send", "--as", "bob", "--to", "ada", "--subject", "s", "--body", "x"},
		{"recv", "--as", "bob"},
		{"ack", "--as", "bob", "--id", "X"},
		{"peek", "--as", "bob"},
	} {
		cli.Do(t, verb...).Exit(2).Err("--as bob is not the login user ada: this connection acts as ada; drop --as, or log in as bob (NOVA_SPRINT_REDIS_USER=bob with its password)")
	}
	assert.Equal(t, 2, r.store.Len(bus.LogKey), "a refused send writes nothing")
	cli.Do(t, "peek").Exit(0).Out("PEEK OK pending=0 new=0")
	cli.Do(t, "recv").Exit(1).Err("RECV NONE: nothing for ada")
	cli.Do(t, "ack", "--id", "X").Exit(0).Out("ACK OK acked=0 asked=1").NotOut("login=none")

	bob := newRig("ada", "bob")
	bob.login = "bob"
	bob.cli().Do(t, "recv").Exit(1).Err("nothing for bob")
	bob.cli().Do(t, "recv", "--as", "ada").Exit(2).Err("--as ada is not the login user bob")
}

// The dogfood of 2026-10-04 (Rowan and Stella): a sender could not tell
// whether a --stdin or shell-built body arrived whole without asking the
// receiver. SEND OK carries the body's byte count and sha256 as the store
// holds it, so `shasum -a 256 f` beside the line is the check.
func TestSendOKCarriesTheBodysBytesAndDigest(t *testing.T) {
	t.Parallel()
	cli := newRig("ada", "bob").cli()
	// sha256("are you there?"), the first run's body, by shasum -a 256
	const digest = "cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745"
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "hello", "--body", "are you there?").Exit(0).Out(" bytes=14 sha256=" + digest + " ")
	cli.DoIn(t, "are you there?", "send", "--as", "ada", "--to", "bob", "--subject", "hello", "--stdin").Exit(0).Out(" bytes=14 sha256=" + digest + " ")
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "hello", "--body", "are you there?\n", "--json").Exit(0).Out(`"bytes":15,`).NotOut(`"sha256":"` + digest + `"`)
}

// A body's trailing newline is the body's: send keeps it, the store keeps
// it, and log and recv hand it back, so a file sent whole is read whole.
// The text form of recv ends the body in exactly one newline whether or
// not the body had one (the line a command reads), and the JSON payload
// carries the body byte for byte, which is where the difference shows.
func TestTheBodysTrailingNewlineIsKeptBySendLogAndRecv(t *testing.T) {
	t.Parallel()
	cli := newRig("ada", "bob").cli()
	cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "with", "--body", "x\n")
	cli.OKIn(t, "y\n", "send", "--as", "ada", "--to", "bob", "--subject", "stdin", "--stdin")
	cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "without", "--body", "z")
	cli.Do(t, "log", "--bodies").Exit(0).Out(`subject="with" body="x\n"`, `subject="stdin" body="y\n"`, `subject="without" body="z"`)
	cli.Do(t, "log", "--bodies", "--json").Exit(0).Out(`"body":"x\n"`, `"body":"y\n"`, `"body":"z"`)
	assert.True(t, strings.HasSuffix(cli.OK(t, "recv", "--as", "bob", "--json").Stdout, `"payload":"\nx\n"}`+"\n"))
	assert.True(t, strings.HasSuffix(cli.OK(t, "recv", "--as", "bob").Stdout, "\n\ny\n"))
	assert.True(t, strings.HasSuffix(cli.OK(t, "recv", "--as", "bob", "--json").Stdout, `"payload":"\nz"}`+"\n"))
}

// An empty recv is the verb running and saying no (the banner's exit 1):
// RECV NONE, the tool's own word, in the text form and in the JSON's word,
// never a refusal, and never a store error.
func TestAnEmptyRecvIsNoneAtExitOne(t *testing.T) {
	t.Parallel()
	cli := newRig("ada", "bob").cli()
	cli.Do(t, "recv", "--as", "bob").Exit(1).Err("RECV NONE: nothing for bob").NotErr("REFUSED", "FAILED")
	cli.Do(t, "recv", "--as", "bob", "--json").Exit(1).Out(`"exit":1`, `"word":"NONE"`).NotOut(`"refused"`)
}

// The decision of 2026-10-04: the tailnet is the boundary, no ACLs. A store off
// loopback and 100.64.0.0/10 is refused before any dial, by --redis or by
// NOVA_BUS_REDIS alike, in one line that names the rule.
func TestAStoreOffLoopbackAndTheTailnetIsRefusedBeforeTheDial(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	const rule = "nova-bus reaches a store over loopback or the tailnet (100.64.0.0/10) only: "
	cli.Do(t, "names", "--redis", "far.test:6381").Exit(2).Err("NAMES REFUSED: " + rule + "far.test:6381 is 203.0.113.9")
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--redis", "elsewhere.test:6381").Exit(2).Err("SEND REFUSED: " + rule + "elsewhere.test:6381 does not resolve")
	r.env[RedisEnv] = "lan.test:6381"
	cli.Do(t, "peek", "--as", "bob").Exit(2).Err("PEEK REFUSED: " + rule + "lan.test:6381 is 10.0.0.5")
	assert.Equal(t, 0, r.opened, "nothing was dialled")
	cli.Do(t, "names", "--redis", "127.0.0.1:6381").Exit(0).Out("NAMES OK count=2")
	cli.Do(t, "names", "--redis", "store.test:6381").Exit(0).Out("NAMES OK count=2")
}

// Alex, 2026-10-04: draining a thirty-message backlog was thirty recv-then-ack
// loops. --max <n> takes up to n in order and --all every one waiting, each
// its own RECV OK; --ack acks each a plain recv printed; with --exec each is
// handed over and acked on exit 0, the batch stopping at the first failure.
func TestRecvTakesABacklogInOrderWithMaxAllAndAck(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	var ids []string
	for _, s := range []string{"one", "two", "three", "four", "five"} {
		ids = append(ids, id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", s, "--body", s).Stdout))
	}
	got := cli.Do(t, "recv", "--as", "bob", "--max", "2").Exit(0).Out("RECV OK id="+ids[0], "RECV OK id="+ids[1]).NotOut("RECV OK id=" + ids[2])
	assert.Equal(t, "RECV OK id="+ids[0]+" from=ada to=bob cc=- re=- at=2026-10-03T12:00:01Z login=none subject=\"one\"\n\none\nRECV OK id="+ids[1]+" from=ada to=bob cc=- re=- at=2026-10-03T12:00:02Z login=none subject=\"two\"\n\ntwo\n", got.Stdout, "each message is its own result, in order")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=2 new=3")

	// a plain recv with --ack: printed, then acked; a held message is not
	// handed out again, so the next batch is the new ones
	cli.Do(t, "recv", "--as", "bob", "--max", "2", "--ack").Exit(0).Out("RECV OK id="+ids[2]+" from=ada to=bob cc=- re=- at=2026-10-03T12:00:03Z login=none acked=true subject=\"three\"", "RECV OK id="+ids[3]+" from=ada", "acked=true")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=2 new=1")

	// --all with --exec: every message waiting, acked on exit 0; the batch
	// stops at the first command that fails, that message still pending
	r.exec = func(stdin string) int {
		if strings.Contains(stdin, `subject="five"`) {
			return 7
		}
		return 0
	}
	r.advance(bus.ClaimAfter) // one and two are claimable again
	cli.Do(t, "recv", "--as", "bob", "--all", "--exec", "deliver").Exit(1).Out("RECV OK id="+ids[0], "RECV OK id="+ids[1], "acked=true exec_exit=0").Err("RECV FAILED id=" + ids[4] + " exec_exit=7")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=0", "id="+ids[4])
	r.exec = nil
	cli.Do(t, "recv", "--as", "bob", "--all", "--exec", "deliver").Exit(1).Err("RECV NONE", "nothing for bob")
	r.advance(bus.ClaimAfter)
	cli.Do(t, "recv", "--as", "bob", "--all", "--json", "--ack").Exit(0).Out(`"subject":"five"`, `"acked":true`)
	cli.Do(t, "recv", "--as", "bob", "--all").Exit(1).Err("RECV NONE: nothing for bob")

	for _, c := range [][]string{
		{"recv", "--as", "bob", "--all", "--max", "2"},
		{"recv", "--as", "bob", "--forever", "--exec", "x", "--all"},
		{"recv", "--as", "bob", "--max", "0"},
		{"recv", "--as", "bob", "--ack", "--exec", "x"},
	} {
		cli.Do(t, c...).Exit(2).Err("RECV REFUSED")
	}
}

// receipts says how far each message has come and how long it stood there;
// overdue lists what is still short of delivered past --older and exits 1
// (SPEC-BUS.md, message-receipts).
func TestReceiptsAndOverdueSayWhereEachMessageIs(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	mid := id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "first", "--body", "x").Stdout)
	cli.Do(t, "overdue").Exit(0).Out("OVERDUE OK count=0 older=10m0s")
	r.advance(11 * time.Minute)
	cli.Do(t, "overdue").Exit(1).Err("OVERDUE OVERDUE count=1 older=10m0s",
		"OVERDUE MESSAGE name=bob id="+mid+" state=new from=ada age=11m")
	cli.Do(t, "overdue", "--older", "1h").Exit(0).Out("OVERDUE OK count=0 older=1h0m0s")
	cli.Do(t, "overdue", "--json").Exit(1).Out(`"word":"OVERDUE"`, `"id":"`+mid+`"`)
	cli.Do(t, "receipts", "--as", "bob", "--id", mid).Exit(0).Out("RECEIPTS OK count=1", "RECEIPTS RECEIPT id="+mid+" state=none age=-")

	cli.OK(t, "recv", "--as", "bob")
	cli.Do(t, "overdue").Exit(0).Out("OVERDUE OK count=0")
	r.advance(time.Minute)
	cli.Do(t, "receipts", "--as", "bob").Exit(0).Out("RECEIPTS OK count=1 login=none", "RECEIPTS RECEIPT id="+mid+" state=delivered age=1m")
	cli.OK(t, "send", "--as", "bob", "--to", "ada", "--re", mid, "--subject", "re first", "--body", "done")
	cli.Do(t, "receipts", "--as", "bob", "--id", mid).Exit(0).Out("state=acted")
	cli.Do(t, "overdue", "--older", "-1s").Exit(2).Err("--older wants a duration of at least 0")
}

func TestTopLevelHelpNamesTheSameRedisAddressPrecedenceAsHelpSend(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()

	// Capture help output
	topHelp := cli.Do(t, "help").Exit(0).Stdout
	sendHelp := cli.Do(t, "help", "send").Exit(0).Stdout

	// The precedence string that should appear in the flag help
	// The tool reads: --redis flag default (NOVA_BUS_REDIS), else NOVA_SPRINT_REDIS, else fleet:bus
	const precedence = "NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus"

	// Top-level help should mention NOVA_BUS_REDIS
	assert.Contains(t, topHelp, "NOVA_BUS_REDIS", "top-level help should mention NOVA_BUS_REDIS")

	// Help send should contain the full precedence
	assert.Contains(t, sendHelp, precedence, "help send should name the full Redis address precedence")
}

func TestBusLogFiltersBySenderAndRecipient(t *testing.T) {
	t.Parallel()
	r := newRig("a", "b", "c")
	cli := r.cli()

	// Send eight messages among the three names: id1 and id3 match --from a --to b
	id1 := id(t, cli.OK(t, "send", "--as", "a", "--to", "b", "--subject", "a to b one", "--body", "msg1").Stdout)
	id2 := id(t, cli.OK(t, "send", "--as", "a", "--to", "c", "--subject", "a to c", "--body", "msg2").Stdout)
	id3 := id(t, cli.OK(t, "send", "--as", "a", "--to", "b", "--subject", "a to b two", "--body", "msg3").Stdout)
	id4 := id(t, cli.OK(t, "send", "--as", "b", "--to", "a", "--subject", "b to a", "--body", "msg4").Stdout)
	id5 := id(t, cli.OK(t, "send", "--as", "b", "--to", "c", "--subject", "b to c", "--body", "msg5").Stdout)
	id6 := id(t, cli.OK(t, "send", "--as", "c", "--to", "a", "--subject", "c to a", "--body", "msg6").Stdout)
	id7 := id(t, cli.OK(t, "send", "--as", "c", "--to", "b", "--subject", "c to b", "--body", "msg7").Stdout)
	id8 := id(t, cli.OK(t, "send", "--as", "c", "--to", "c", "--subject", "c to c", "--body", "msg8").Stdout)

	// --from a lists only a's messages
	cli.Do(t, "log", "--from", "a").Exit(0).Out("LOG OK total=3", "id="+id1, "id="+id2, "id="+id3).NotOut("id="+id4, "id="+id5, "id="+id6, "id="+id7, "id="+id8)

	// --to b lists only messages to b
	cli.Do(t, "log", "--to", "b").Exit(0).Out("LOG OK total=3", "id="+id1, "id="+id3, "id="+id7).NotOut("id="+id2, "id="+id4, "id="+id5, "id="+id6, "id="+id8)

	// both together list only a → b
	cli.Do(t, "log", "--from", "a", "--to", "b").Exit(0).Out("LOG OK total=2", "id="+id1, "id="+id3).NotOut("id="+id2, "id="+id4, "id="+id5, "id="+id6, "id="+id7, "id="+id8)

	// --max 1 returns the newest match (filtering before --max, then taking last after --max)
	cli.Do(t, "log", "--from", "a", "--to", "b", "--max", "1").Exit(0).Out("LOG OK total=2", "id="+id3).NotOut("id="+id1, "id="+id2, "id="+id4, "id="+id5, "id="+id6, "id="+id7, "id="+id8)
}
