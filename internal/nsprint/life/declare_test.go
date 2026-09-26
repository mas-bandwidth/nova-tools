//go:build functional

package life_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// cmdLog is a go-redis hook that records every command name, and how many
// pipelines and single commands reached the server.
type cmdLog struct {
	mu        sync.Mutex
	names     []string
	pipelines int
	singles   int
	before    func(redis.Cmder) // runs before the first FCALL, once
}

func (l *cmdLog) DialHook(next redis.DialHook) redis.DialHook { return next }

func (l *cmdLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		l.mu.Lock()
		l.singles++
		l.names = append(l.names, cmd.Name())
		b := l.before
		if cmd.Name() == "fcall" {
			l.before = nil
		} else {
			b = nil
		}
		l.mu.Unlock()
		if b != nil {
			b(cmd)
		}
		return next(ctx, cmd)
	}
}

func (l *cmdLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		l.mu.Lock()
		l.pipelines++
		for _, c := range cmds {
			l.names = append(l.names, c.Name())
		}
		l.mu.Unlock()
		return next(ctx, cmds)
	}
}

// hookedStore is a store on the control Redis whose client carries log.
func hookedStore(t *testing.T, addr string, log *cmdLog) *store.Store {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr, PoolSize: 1})
	t.Cleanup(func() { _ = c.Close() })
	// One connection, opened before the hook, so its handshake is not counted.
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	c.AddHook(log)
	return store.New(c)
}

// declRepo is a committed fixture fleet repo on branch main.
func declRepo(t *testing.T) (dir string, git func(args ...string) string, commit func(body string) string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "fleet")
	git = realGit(t, filepath.Dir(dir))
	git("init", "-q", dir)
	file := filepath.Join(dir, "all.yml")
	commit = func(body string) string {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		git("-C", dir, "add", "-A")
		git("-C", dir, "commit", "-q", "-m", "fleet")
		return git("-C", dir, "rev-parse", "HEAD")
	}
	return dir, git, commit
}

const humans = "friends:\n  stella: { wake: human, notify: glenn }\n  walter: { wake: human, notify: sms }\n"

func readSource(t *testing.T, dir string) life.DeclSource {
	t.Helper()
	src, err := life.ReadDeclSource(context.Background(), filepath.Join(dir, "all.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestDeclareOneRoundTrip: a declare is one pipelined read and one FCALL.
func TestDeclareOneRoundTrip(t *testing.T) {
	t.Parallel()

	_, _, addr := controlRedis(t)
	dir, _, commit := declRepo(t)
	commit(humans)
	log := &cmdLog{}
	res, err := life.Declare(context.Background(), hookedStore(t, addr, log), readSource(t, dir))
	if err != nil || res.N != 2 || res.Human != 2 {
		t.Fatalf("declare: %+v %v", res, err)
	}
	if log.pipelines != 1 || log.singles != 1 || log.names[len(log.names)-1] != "fcall" {
		t.Fatalf("pipelines %d singles %d commands %v; want one pipelined read and one FCALL", log.pipelines, log.singles, log.names)
	}
}

// onlyReads fails when log saw anything but reads.
func onlyReads(t *testing.T, log *cmdLog) {
	t.Helper()
	for _, n := range log.names {
		switch n {
		case "hmget", "hget", "smembers", "hgetall", "time", "exists":
		default:
			t.Fatalf("a read-only path sent %q (all: %v)", n, log.names)
		}
	}
}

// TestDeclareUnchangedNoWrites: the same rev and digest write nothing.
func TestDeclareUnchangedNoWrites(t *testing.T) {
	t.Parallel()

	st, _, addr := controlRedis(t)
	dir, _, commit := declRepo(t)
	commit(humans)
	src := readSource(t, dir)
	if _, err := life.Declare(context.Background(), st, src); err != nil {
		t.Fatal(err)
	}
	log := &cmdLog{}
	res, err := life.Declare(context.Background(), hookedStore(t, addr, log), src)
	if err != nil || !res.Unchanged {
		t.Fatalf("second declare: %+v %v; want unchanged", res, err)
	}
	onlyReads(t, log)
}

// TestDeclareCheckReadsOnly: --check diffs and writes nothing.
func TestDeclareCheckReadsOnly(t *testing.T) {
	t.Parallel()

	st, _, addr := controlRedis(t)
	dir, _, commit := declRepo(t)
	commit(humans)
	if _, err := life.Declare(context.Background(), st, readSource(t, dir)); err != nil {
		t.Fatal(err)
	}
	commit("friends:\n  stella: { wake: human, notify: pager }\n  zed: { wake: human, notify: z }\n")
	log := &cmdLog{}
	c, err := life.CheckDecl(context.Background(), hookedStore(t, addr, log), readSource(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Add, ",") != "zed" || strings.Join(c.Change, ",") != "stella" || strings.Join(c.Remove, ",") != "walter" {
		t.Fatalf("check = %+v; want add zed, change stella, remove walter", c)
	}
	onlyReads(t, log)
}

// TestDeclareCASOneWinner: two declarers at sibling revs r2a and r2b (both
// children of r1) both read prev_rev=r1; exactly one FCALL writes, the other
// gets CONFLICT with nothing written, and its retry is refused stale.
func TestDeclareCASOneWinner(t *testing.T) {
	t.Parallel()

	st, client, addr := controlRedis(t)
	ctx := context.Background()
	dir, git, commit := declRepo(t)
	r1 := commit(humans)
	if _, err := life.Declare(ctx, st, readSource(t, dir)); err != nil {
		t.Fatal(err)
	}
	git("-C", dir, "checkout", "-q", "-b", "a")
	r2a := commit("friends:\n  stella: { wake: human, notify: a }\n")
	srcA := readSource(t, dir)
	git("-C", dir, "checkout", "-q", "main")
	git("-C", dir, "checkout", "-q", "-b", "b")
	r2b := commit("friends:\n  stella: { wake: human, notify: b }\n")
	srcB := readSource(t, dir)
	if r2a == r2b || srcA.Rev != r2a || srcB.Rev != r2b {
		t.Fatal("fixture: two sibling revs")
	}

	// A has read prev_rev=r1; B runs whole (it too reads r1) before A's FCALL.
	log := &cmdLog{}
	var bErr error
	log.before = func(redis.Cmder) { _, bErr = life.Declare(ctx, st, srcB) }
	_, aErr := life.Declare(ctx, hookedStore(t, addr, log), srcA)
	if bErr != nil {
		t.Fatalf("B: %v", bErr)
	}
	if !errors.Is(aErr, life.ErrDeclConflict) {
		t.Fatalf("A: %v, want CONFLICT", aErr)
	}
	if rev := client.HGet(ctx, "friends:decl", "rev").Val(); rev != r2b {
		t.Fatalf("registry rev %s, want the one winner %s (r1 %s)", rev, r2b, r1)
	}
	if n := client.HGet(ctx, life.WakePathKey("stella"), "notify").Val(); n != "b" {
		t.Fatalf("stella notify %q, want B's", n)
	}
	_, err := life.Declare(ctx, st, srcA)
	if !errors.Is(err, life.ErrDeclStale) {
		t.Fatalf("A's retry: %v, want stale (r2b is not an ancestor of r2a)", err)
	}
	names := client.SMembers(ctx, "friends:declared").Val()
	sort.Strings(names)
	if strings.Join(names, ",") != "stella" {
		t.Fatalf("friends:declared %v", names)
	}
}
