package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var start = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// rig is the tool over one fake store with ada and bob known: no socket, no
// clock, no seat. exec is what --exec's command does with the text it is
// handed; signals is the loop's context, which a test cancels.
type rig struct {
	store   *bus2.Fake
	env     map[string]string
	exec    func(stdin string) int
	execIn  []string
	cancel  context.CancelFunc
	opened  int
	openErr error
	login   string // the user the store logs in as; "" is a store with no users
}

func newRig(names ...string) *rig {
	return &rig{store: bus2.NewFake(start, names...), env: map[string]string{RedisEnv: "store.test:6379"}}
}

func (r *rig) world() world {
	return world{
		getenv: func(k string) string { return r.env[k] },
		open: func(context.Context, string) (bus2.Store, string, func(), error) {
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
	newRig().cli().Do(t).Exit(2).Err("BUS2 REFUSED", "nova-bus2 help")
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
		{"no store named", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}, map[string]string{}, []string{"--redis is required", RedisEnv}},
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
			assert.Equal(t, 0, r.store.Len(bus2.LogKey), "a refused send writes nothing")
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
	assert.Regexp(t, `^SEND OK id=[0-9A-Z]{26} to=bob cc=ada at=2026-10-03T12:00:01Z login=none\n$`, sent.Stdout)
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
	r.store.Advance(bus2.ClaimAfter)
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
	r.store.Advance(bus2.ClaimAfter)
	cli.Do(t, "recv", "--as", "bob", "--forever", "--exec", "deliver").Exit(1).Err("--exec exited 1")
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

func TestTheRedisDefaultIsTheBusVariableAlone(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.env = map[string]string{"NOVA_SPRINT_REDIS": "b:1", "NOVA_REDIS_ADDR": "c:1"}
	r.cli().Do(t, "names").Exit(2).Err("--redis is required", RedisEnv)
	r.env = map[string]string{RedisEnv: "a:1"}
	r.cli().Do(t, "names").Exit(0).Out("NAMES OK count=2")
}

func TestSendReadsTheBodyFromStdinAndBoundsIt(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	cli.OKIn(t, "from stdin\n", "send", "--as", "ada", "--to", "bob", "--subject", "s", "--stdin")
	cli.Do(t, "log", "--bodies").Exit(0).Out(`body="from stdin\n"`)
	big := strings.Repeat("x", bus2.MaxBody+7)
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", big).Exit(2).Err("the body is 1048583 bytes, at most 1048576")
	cli.DoIn(t, big, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--stdin").Exit(2).Err("the body on stdin is over 1 MiB; at most 1048576 bytes")
}

// The identity is the store's login user, never a word on the line
// (SPEC-BUS2.md, the identity): with a login, --as may repeat it or be left
// out and any other name is refused; with none, --as is required and every
// write says login=none.
func TestTheIdentityIsTheLoginUser(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.login = "ada"
	cli := r.cli()
	sent := cli.OK(t, "send", "--to", "bob", "--subject", "s", "--body", "x")
	assert.Regexp(t, `^SEND OK id=[0-9A-Z]{26} to=bob cc=- at=2026-10-03T12:00:01Z\n$`, sent.Stdout, "no login=none with a login")
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
	assert.Equal(t, 2, r.store.Len(bus2.LogKey), "a refused send writes nothing")
	cli.Do(t, "peek").Exit(0).Out("PEEK OK pending=0 new=0")
	cli.Do(t, "recv").Exit(1).Err("RECV NONE: nothing for ada")
	cli.Do(t, "ack", "--id", "X").Exit(0).Out("ACK OK acked=0 asked=1").NotOut("login=none")

	bob := newRig("ada", "bob")
	bob.login = "bob"
	bob.cli().Do(t, "recv").Exit(1).Err("nothing for bob")
	bob.cli().Do(t, "recv", "--as", "ada").Exit(2).Err("--as ada is not the login user bob")
}
