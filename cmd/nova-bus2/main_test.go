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
}

func newRig(names ...string) *rig {
	return &rig{store: bus2.NewFake(start, names...), env: map[string]string{RedisEnv: "store.test:6379"}}
}

func (r *rig) world() world {
	return world{
		getenv:   func(k string) string { return r.env[k] },
		hostname: func() string { return "host-1" },
		open: func(context.Context, string) (bus2.Store, func(), error) {
			r.opened++
			if r.openErr != nil {
				return nil, nil, r.openErr
			}
			return r.store, func() {}, nil
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
		{"nothing given", []string{"send"}, nil, []string{"--as is required", "--to is required", "--subject is required", "exactly one of --body"}},
		{"two bodies", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--stdin"}, nil, []string{"exactly one of --body"}},
		{"unknown recipient and bad name", []string{"send", "--as", "ada", "--to", "zed,B", "--subject", "s", "--body", "x"}, nil, []string{`"B" is not lowercase`}},
		{"unknown recipient", []string{"send", "--as", "ada", "--to", "zed", "--subject", "s", "--body", "x"}, nil, []string{"zed is no known name", "nova-config friend add zed"}},
		{"no store named", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}, map[string]string{}, []string{"--redis is required", RedisEnv}},
		{"empty body", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", " "}, nil, []string{"the body is empty"}},
		{"missing file", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--file", "{dir}/none.txt"}, nil, []string{"--file:", "none.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig("ada", "bob")
			if c.env != nil {
				r.env = c.env
			}
			dir := t.TempDir()
			args := make([]string, len(c.args))
			for i, a := range c.args {
				args[i] = strings.ReplaceAll(a, "{dir}", dir)
			}
			got := r.cli().Do(t, args...).Exit(2).Err("SEND REFUSED")
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
	assert.Regexp(t, `^SEND OK id=[0-9A-Z]{26} to=bob cc=ada at=2026-10-03T12:00:01Z\n$`, sent.Stdout)
	mid := id(t, sent.Stdout)

	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=0 new=1", "PEEK MESSAGE state=new id="+mid+" from=ada at=2026-10-03T12:00:01Z subject=\"hello there\"")

	got := cli.OK(t, "recv", "--as", "bob")
	assert.Equal(t, "RECV OK id="+mid+" from=ada to=bob cc=ada re=- at=2026-10-03T12:00:01Z entry=1791028801000-1 subject=\"hello there\"\n\nline one\nline two\n", got.Stdout)

	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=0", "state=pending")
	cli.Do(t, "recv", "--as", "ada").Exit(0).Out("RECV OK id=" + mid + " from=ada to=bob cc=ada").NotErr("REFUSED")

	cli.Do(t, "ack", "--as", "bob", "--id", mid+",NOPE").Exit(0).Out("ACK OK acked=1 asked=2", "ACK ID id="+mid+" acked=true", "ACK ID id=NOPE acked=false")
	cli.Do(t, "ack", "--as", "bob", "--id", mid).Exit(0).Out("ACK OK acked=0 asked=1", "acked=false")
	cli.Do(t, "recv", "--as", "bob").Exit(1).Err("RECV NONE: nothing for bob within --block 0s")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=0 new=0")

	cli.Do(t, "log", "--from", "ada", "--bodies").Exit(0).Out("LOG OK total=1", "LOG MESSAGE id="+mid+" from=ada to=bob cc=ada re=- at=2026-10-03T12:00:01Z subject=\"hello there\" body=\"line one\\nline two\\n\"")
	cli.Do(t, "log", "--to", "zed").Exit(0).Out("LOG OK total=0")
	cli.Do(t, "log", "--since", "yesterday").Exit(2).Err("--since \"yesterday\" is no instant")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2", "NAMES NAME name=ada", "NAMES NAME name=bob")
	cli.Do(t, "names", "--json").Exit(0).Out(`"status":"ok"`, `"name":"ada"`)
}

func TestRecvExecAcksOnZeroAndKeepsThePendingMessageOnFailure(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	mid := id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "the body").Stdout)

	r.exec = func(string) int { return 3 }
	cli.Do(t, "recv", "--as", "bob", "--exec", "deliver").Exit(1).Err("RECV FAIL id=" + mid + " exec_exit=3: --exec exited 3, so the message stays pending")
	require.Len(t, r.execIn, 1)
	assert.Equal(t, "RECV OK id="+mid+" from=ada to=bob cc=- re=- at=2026-10-03T12:00:01Z entry=1791028801000-1 subject=\"s\"\n\nthe body", r.execIn[0], "the command reads what recv prints")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=0")

	r.exec = func(string) int { return 0 }
	cli.Do(t, "recv", "--as", "bob", "--exec", "deliver").Exit(0).Out("RECV OK id="+mid, "acked=true exec_exit=0")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=0 new=0")
}

func TestRecvForeverWantsExecAndStopsOnASignal(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	cli.Do(t, "recv", "--as", "bob", "--forever").Exit(2).Err("--forever wants --exec")
	cli.Do(t, "recv", "--as", "bob", "--block", "-1s").Exit(2).Err("--block must be zero or more")
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
	got.Out(`subject="one"`, `subject="two"`, "RECV OK delivered=2 stopped=signal").NotOut(`subject="three"`)
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=0 new=1")

	r.cancel = nil
	r.exec = func(string) int { return 1 }
	cli.Do(t, "recv", "--as", "bob", "--forever", "--exec", "deliver").Exit(1).Err("--exec exited 1")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=0")
}

func TestTheRedisDefaultIsTheBusVariableThenTheSprintsThenTheGeneral(t *testing.T) {
	t.Parallel()
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{RedisEnv: "a:1", "NOVA_SPRINT_REDIS": "b:1", "NOVA_REDIS_ADDR": "c:1"}, "a:1"},
		{map[string]string{"NOVA_SPRINT_REDIS": "b:1", "NOVA_REDIS_ADDR": "c:1"}, "b:1"},
		{map[string]string{"NOVA_REDIS_ADDR": "c:1"}, "c:1"},
		{map[string]string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			r := newRig()
			r.env = c.env
			assert.Equal(t, c.want, r.world().redisDefault())
		})
	}
}

func TestSendReadsTheBodyFromAFileOrStdin(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	path := t.TempDir() + "/note.txt"
	testkit.WriteFile(t, path, "from a file\n")
	cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "f", "--file", path)
	cli.OKIn(t, "from stdin\n", "send", "--as", "ada", "--to", "bob", "--subject", "s", "--stdin")
	cli.Do(t, "log", "--bodies").Exit(0).Out(`body="from a file\n"`, `body="from stdin\n"`)
	big := strings.Repeat("x", bus2.MaxBody+1)
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", big).Exit(2).Err("at most 1048576")
}
