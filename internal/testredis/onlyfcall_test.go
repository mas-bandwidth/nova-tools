package testredis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The unit tier of OnlyFCALL: a fake processor stands where the store would,
// and the hook is called the way go-redis calls it. No test here starts a
// process or opens a socket; the one test that runs a real client gives it an
// in-memory pipe.

// The counts these tests read.
const (
	never = 0
	once  = 1
)

// failures is a testing.TB that writes down what a test reports with Error or
// Errorf instead of failing the test that provoked it. Everything else is the
// real test's.
type failures struct {
	testing.TB
	mu   sync.Mutex
	said []string
}

func (f *failures) Errorf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.said = append(f.said, fmt.Sprintf(format, args...))
}

func (f *failures) Error(args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.said = append(f.said, fmt.Sprint(args...))
}

func (f *failures) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.said)
}

// errStore is what the fake processor answers, so a test can tell an answer
// from the store apart from a refusal by the hook.
var errStore = errors.New("the store answered")

// processor is the store a hook sends to. It answers every command and batch
// with errStore and writes down what reached it, one entry per call: the
// commands of the call, each as its words.
type processor struct {
	mu   sync.Mutex
	seen [][]string
}

func (p *processor) note(cmds ...redis.Cmder) {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, len(cmds))
	for i, cmd := range cmds {
		names[i] = strings.ToUpper(cmd.Name())
	}
	p.seen = append(p.seen, names)
}

func (p *processor) process(_ context.Context, cmd redis.Cmder) error {
	p.note(cmd)
	return errStore
}

func (p *processor) pipeline(_ context.Context, cmds []redis.Cmder) error {
	p.note(cmds...)
	return errStore
}

func (p *processor) calls() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

// rig is one OnlyFCALL in front of one fake processor, with the failures the
// test that owns the hook has been given.
type rig struct {
	said  *failures
	store *processor
	hook  redis.Hook
}

func newRig(t *testing.T) *rig {
	t.Helper()
	said := &failures{TB: t}
	return &rig{said: said, store: &processor{}, hook: OnlyFCALL(said)}
}

// do sends one command through the hook the way Process does.
func (r *rig) do(words ...any) (redis.Cmder, error) {
	cmd := redis.NewCmd(context.Background(), words...)
	return cmd, r.hook.ProcessHook(r.store.process)(context.Background(), cmd)
}

// batch sends commands through the hook the way Exec of a pipeline does.
func (r *rig) batch(cmds ...redis.Cmder) error {
	return r.hook.ProcessPipelineHook(r.store.pipeline)(context.Background(), cmds)
}

func command(words ...any) redis.Cmder {
	return redis.NewCmd(context.Background(), words...)
}

// wantFailure reads the one failure a test was given and checks it names every
// one of the words.
func wantFailure(t *testing.T, said *failures, words ...string) {
	t.Helper()
	if total := nextFailure(t, said, never, words...); total != once {
		t.Fatalf("the test was failed %d times; want once", total)
	}
}

// nextFailure checks that the test has been failed once more since it had been
// failed before times, and that the newest failure names every one of the
// words. It returns how many times the test has been failed now.
func nextFailure(t *testing.T, said *failures, before int, words ...string) int {
	t.Helper()
	got := said.all()
	if len(got) != before+once {
		t.Fatalf("the test was failed %d times: %q; want %d", len(got), got, before+once)
	}
	newest := got[len(got)-once]
	for _, w := range words {
		if !strings.Contains(newest, w) {
			t.Fatalf("the failure %q does not name %q", newest, w)
		}
	}
	return len(got)
}

func TestOnlyFCALLFailsASetAndNamesIt(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	cmd, err := r.do("set", "k", "v")
	wantFailure(t, r.said, "SET")
	if err == nil || !strings.Contains(err.Error(), "SET") || errors.Is(err, errStore) {
		t.Fatalf("the caller of a refused SET was given %v; want a refusal that names SET", err)
	}
	if cmd.Err() == nil || cmd.Err().Error() != err.Error() {
		t.Fatalf("the refused command carries the error %v; want the one its caller was given, %v", cmd.Err(), err)
	}
	if got := r.store.calls(); len(got) != never {
		t.Fatalf("a refused SET reached the store: %q", got)
	}
}

func TestOnlyFCALLPassesAGet(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	_, err := r.do("get", "k")
	if got := r.said.all(); len(got) != never {
		t.Fatalf("a GET failed the test: %q", got)
	}
	// The hook leaves the answer alone: what the store said is what the caller
	// reads.
	if !errors.Is(err, errStore) {
		t.Fatalf("the caller of a GET was given %v; want the store's answer, %v", err, errStore)
	}
	if got := r.store.calls(); len(got) != once || !slices.Equal(got[0], []string{"GET"}) {
		t.Fatalf("the store was sent %q; want the one GET", got)
	}
}

func TestOnlyFCALLPassesFCALLAndTheReadOnlyCalls(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	sent := []string{"FCALL", "fcall", "Fcall", "FCALL_RO", "EVAL_RO", "EVALSHA_RO"}
	for _, name := range sent {
		if _, err := r.do(name, "fn", "1", "key", "arg"); !errors.Is(err, errStore) {
			t.Errorf("%s was given %v; want the store's answer", name, err)
		}
	}
	if got := r.said.all(); len(got) != never {
		t.Fatalf("FCALL or a read-only call failed the test: %q", got)
	}
	if got := r.store.calls(); len(got) != len(sent) {
		t.Fatalf("the store was sent %d calls %q; want %d", len(got), got, len(sent))
	}
}

func TestOnlyFCALLFailsAPipelineHoldingOneHSETAndNamesIt(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	batch := []redis.Cmder{
		command("get", "k"),
		command("hset", "h", "field", "value"),
		command("fcall", "fn", "0"),
	}
	err := r.batch(batch...)
	wantFailure(t, r.said, "HSET", "command 2 of 3")
	if err == nil || !strings.Contains(err.Error(), "HSET") || errors.Is(err, errStore) {
		t.Fatalf("the caller of the pipeline was given %v; want a refusal that names HSET", err)
	}
	for _, cmd := range batch {
		if cmd.Err() == nil || !strings.Contains(cmd.Err().Error(), "HSET") {
			t.Errorf("%s of the refused pipeline carries the error %v; want the refusal", cmd.Name(), cmd.Err())
		}
	}
	// None of it is sent: the reads and the FCALL beside the write are refused
	// with it, so the store is left as it was.
	if got := r.store.calls(); len(got) != never {
		t.Fatalf("a refused pipeline reached the store: %q", got)
	}
}

func TestOnlyFCALLNamesEveryWriteOfAPipelineInOneFailure(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	err := r.batch(
		command("set", "k", "v"),
		command("get", "k"),
		command("del", "k"),
		command("xgroup", "create", "s", "g", "$"),
	)
	wantFailure(t, r.said, "SET (command 1 of 4)", "DEL (command 3 of 4)", "XGROUP CREATE (command 4 of 4)")
	if err == nil || strings.Contains(r.said.all()[0], "GET") {
		t.Fatalf("err = %v, failure = %q; want a refusal and the read left out of the writes", err, r.said.all())
	}
}

func TestOnlyFCALLPassesAPipelineOfReadsAndFCALL(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	batch := []redis.Cmder{command("get", "k"), command("fcall", "fn", "0"), command("fcall_ro", "fn", "0")}
	if err := r.batch(batch...); !errors.Is(err, errStore) {
		t.Fatalf("the caller of the pipeline was given %v; want the store's answer", err)
	}
	if got := r.said.all(); len(got) != never {
		t.Fatalf("a pipeline of a read and FCALLs failed the test: %q", got)
	}
	if got := r.store.calls(); len(got) != once || !slices.Equal(got[0], []string{"GET", "FCALL", "FCALL_RO"}) {
		t.Fatalf("the store was sent %q; want the one pipeline of the three", got)
	}
}

// A transaction reaches the hook wrapped in MULTI and EXEC: those two pass and
// the commands between them are judged.
func TestOnlyFCALLJudgesATransactionByWhatIsBetweenMultiAndExec(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	if err := r.batch(command("multi"), command("fcall", "fn", "0"), command("exec")); !errors.Is(err, errStore) {
		t.Fatalf("a transaction of one FCALL was given %v; want the store's answer", err)
	}
	if got := r.said.all(); len(got) != never {
		t.Fatalf("a transaction of one FCALL failed the test: %q", got)
	}
	if err := r.batch(command("multi"), command("hset", "h", "f", "v"), command("exec")); err == nil || errors.Is(err, errStore) {
		t.Fatalf("a transaction of one HSET was given %v; want a refusal", err)
	}
	wantFailure(t, r.said, "HSET (command 2 of 3)")
	if got := r.store.calls(); len(got) != once {
		t.Fatalf("the store was sent %q; want the transaction of the FCALL and not the one of the HSET", got)
	}
}

// Every name of the list fails, each as itself: the list is what the hook
// judges by, so a name that is listed and passes is a hole.
func TestOnlyFCALLFailsEveryCommandOfTheList(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	failed := never
	for _, g := range writeGroups {
		for _, name := range g.commands {
			words := []any{}
			for _, w := range strings.Fields(name) {
				words = append(words, w)
			}
			words = append(words, "k", "v")
			_, err := r.do(words...)
			failed++
			got := r.said.all()
			if len(got) != failed || !strings.Contains(got[failed-1], "testredis: OnlyFCALL: "+name+" is a write command") {
				t.Errorf("%s (%s): the test was failed %d times, the last %q; want %d and the failure to name %s", name, g.name, len(got), got, failed, name)
			}
			if err == nil || errors.Is(err, errStore) {
				t.Errorf("%s (%s): the caller was given %v; want a refusal", name, g.name, err)
			}
		}
	}
	if got := r.store.calls(); len(got) != never {
		t.Fatalf("the list reached the store: %q", got)
	}
	if failed != len(writes) {
		t.Fatalf("%d commands were tried; the list has %d", failed, len(writes))
	}
}

func TestOnlyFCALLReadsACommandWithoutRegardToCaseOrType(t *testing.T) {
	t.Parallel()

	name := "SET"
	r := newRig(t)
	tried := [][]any{
		{"set", "k", "v"},
		{"SET", "k", "v"},
		{"SeT", "k", "v"},
		{[]byte("Set"), "k", "v"},
		{&name, "k", "v"},
		{"xGroup", "Create", "s", "g", "$"},
		{"function", []byte("load"), "code"},
	}
	for _, words := range tried {
		if _, err := r.do(words...); err == nil || errors.Is(err, errStore) {
			t.Errorf("%q was given %v; want a refusal", words, err)
		}
	}
	if got := r.said.all(); len(got) != len(tried) {
		t.Fatalf("the test was failed %d times %q; want %d", len(got), got, len(tried))
	}
	if got := r.store.calls(); len(got) != never {
		t.Fatalf("a write reached the store: %q", got)
	}
}

// What is not in the list passes, and with it the commands that share a name
// with a listed one: a subcommand is a write by its pair.
func TestOnlyFCALLPassesWhatIsNotAWrite(t *testing.T) {
	t.Parallel()

	passes := [][]any{
		{"get", "k"}, {"mget", "a", "b"}, {"hget", "h", "f"}, {"hgetall", "h"},
		{"lrange", "l", "0", "-1"}, {"smembers", "s"}, {"zrange", "z", "0", "-1"},
		{"xrange", "s", "-", "+"}, {"xread", "streams", "s", "0"}, {"xlen", "s"},
		{"xinfo", "stream", "s"}, {"xgroup", "help"}, {"function", "list"},
		{"function", "stats"}, {"function", "dump"}, {"script", "load", "code"},
		{"scan", "0"}, {"type", "k"}, {"exists", "k"}, {"ttl", "k"}, {"dump", "k"},
		{"touch", "k"}, {"pfcount", "hll"}, {"geosearch", "g", "frommember", "m", "byradius", "1", "m"},
		{"sort_ro", "l"}, {"bitcount", "k"}, {"lcs", "a", "b"}, {"object", "encoding", "k"},
		{"hello", "3"}, {"ping"}, {"select", "0"}, {"multi"}, {"exec"}, {"discard"},
		{"watch", "k"}, {"unwatch"}, {"publish", "c", "m"}, {"info"}, {"dbsize"},
		{"config", "get", "save"}, {"client", "setinfo", "lib-name", "x"},
		{"fcall", "fn", "0"}, {"fcall_ro", "fn", "0"},
	}
	r := newRig(t)
	for _, words := range passes {
		if _, err := r.do(words...); !errors.Is(err, errStore) {
			t.Errorf("%q was given %v; want the store's answer", words, err)
		}
	}
	if got := r.said.all(); len(got) != never {
		t.Fatalf("what is not a write failed the test: %q", got)
	}
	if got := r.store.calls(); len(got) != len(passes) {
		t.Fatalf("the store was sent %d calls; want %d", len(got), len(passes))
	}
}

func TestWriteNameOfACommandThatHasNoWords(t *testing.T) {
	t.Parallel()

	if got := writeName(nil); got != "" {
		t.Fatalf("writeName of no words = %q; want none", got)
	}
	if got := writeName([]any{"set"}); got != "SET" {
		t.Fatalf("writeName of SET = %q; want SET", got)
	}
	// One word of a pair is not the pair: XGROUP alone is no command the
	// server runs, and the hook does not guess.
	if got := writeName([]any{"xgroup"}); got != "" {
		t.Fatalf("writeName of XGROUP alone = %q; want none", got)
	}
	var missing *string
	if got := writeName([]any{missing, "k"}); got != "" {
		t.Fatalf("writeName of a nil name = %q; want none", got)
	}
}

// The list is the file's own contract: capitals, one or two words, in order,
// and each name once. A reader finds a name by its place, and a name that is
// in two groups is a group that is wrong.
func TestTheWriteListIsWellFormed(t *testing.T) {
	t.Parallel()

	const most = 2 // words in a name: a command, or a command and its subcommand
	seen := map[string]string{}
	for _, g := range writeGroups {
		if g.name == "" || len(g.commands) == never {
			t.Errorf("a group of the list has the name %q and %d commands", g.name, len(g.commands))
		}
		if !slices.IsSorted(g.commands) {
			t.Errorf("the %s group is not in order: %q", g.name, g.commands)
		}
		for _, name := range g.commands {
			if name != strings.ToUpper(name) || name != strings.Join(strings.Fields(name), " ") || len(strings.Fields(name)) > most {
				t.Errorf("%q in the %s group is not a command in capitals with at most one subcommand", name, g.name)
			}
			if other, dup := seen[name]; dup {
				t.Errorf("%s is in the %s group and in the %s group", name, other, g.name)
			}
			seen[name] = g.name
		}
		if g.origin != flagged && g.origin != scripted && g.origin != bundled {
			t.Errorf("the %s group has the origin %d, which the functional test does not know", g.name, g.origin)
		}
	}
	if len(seen) != len(writes) {
		t.Fatalf("the set has %d names; the groups have %d", len(writes), len(seen))
	}
	// FCALL is the one door: no group may name it, or any read-only call.
	for _, door := range []string{"FCALL", "FCALL_RO", "EVAL_RO", "EVALSHA_RO"} {
		if writes[door] {
			t.Errorf("%s is in the list; it is what the hook lets through", door)
		}
	}
	// The names the task lists, and the two that run scripts, are all there.
	for _, name := range []string{"SET", "DEL", "HSET", "HDEL", "ZADD", "ZREM", "XADD", "XTRIM", "LPUSH", "RPUSH", "EXPIRE", "FLUSHALL", "FLUSHDB", "EVAL", "EVALSHA"} {
		if !writes[name] {
			t.Errorf("%s is not in the list", name)
		}
	}
}

// Connecting is not the hook's business: the dial hook is the one it was
// given.
func TestOnlyFCALLLeavesDialingAlone(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	var dialed atomic.Int64
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialed.Add(once)
		return nil, errStore
	}
	if _, err := r.hook.DialHook(dial)(context.Background(), "network", "address"); !errors.Is(err, errStore) || dialed.Load() != once {
		t.Fatalf("the dial hook gave %v after %d dials; want the dialer's own answer after one", err, dialed.Load())
	}
}

// The hook on a real go-redis client, over an in-memory pipe answered by the
// counter's own serving loop: every way go-redis sends (a command, a pipeline,
// a transaction) goes through the hook, and a refused one is never written to
// the pipe.
func TestOnlyFCALLOnARealClient(t *testing.T) {
	t.Parallel()

	var wire atomic.Int64
	var serving sync.WaitGroup
	client := redis.NewClient(&redis.Options{
		Protocol:        2,
		DisableIdentity: true,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			near, far := net.Pipe()
			serving.Add(once)
			go func() {
				defer serving.Done()
				defer far.Close()
				serveCounted(far, &wire)
			}()
			return near, nil
		},
	})
	said := &failures{TB: t}
	client.AddHook(OnlyFCALL(said))
	t.Cleanup(func() {
		_ = client.Close()
		serving.Wait()
	})
	ctx := context.Background()

	// A command that passes is written, and the pipe answers it.
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := client.Get(ctx, "k").Err(); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := client.FCall(ctx, "fn", []string{"k"}, "arg").Err(); err != nil {
		t.Fatalf("fcall: %v", err)
	}
	if got := said.all(); len(got) != never {
		t.Fatalf("a PING, a GET and an FCALL failed the test: %q", got)
	}
	baseline := wire.Load()

	// A command that writes is refused, and nothing is written.
	if err := client.Set(ctx, "k", "v", never).Err(); err == nil || !strings.Contains(err.Error(), "SET") {
		t.Fatalf("set was given %v; want a refusal that names SET", err)
	}
	failed := nextFailure(t, said, never, "SET")

	// A pipeline that holds one write is refused whole.
	pipe := client.Pipeline()
	pipe.Get(ctx, "k")
	hset := pipe.HSet(ctx, "h", "field", "value")
	if _, err := pipe.Exec(ctx); err == nil || !strings.Contains(err.Error(), "HSET") {
		t.Fatalf("the pipeline was given %v; want a refusal that names HSET", err)
	}
	if hset.Err() == nil {
		t.Fatal("the HSET of the refused pipeline carries no error")
	}
	failed = nextFailure(t, said, failed, "HSET (command 2 of 2)")

	// So is a transaction: go-redis wraps it in MULTI and EXEC, which pass.
	tx := client.TxPipeline()
	tx.HSet(ctx, "h", "field", "value")
	if _, err := tx.Exec(ctx); err == nil || !strings.Contains(err.Error(), "HSET") {
		t.Fatalf("the transaction was given %v; want a refusal that names HSET", err)
	}
	nextFailure(t, said, failed, "HSET (command 2 of 3)")

	if got := wire.Load(); got != baseline {
		t.Fatalf("%d commands were written after the refusals; want none", got-baseline)
	}
}
