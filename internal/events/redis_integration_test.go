package events

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// redis_integration_test.go is Johnny's bar 3 against the real store, and it is the only
// file here that reaches a network. It is OFF unless NOVA_REDIS_TEST=1, and it names no
// host: the address, the ACL user and the password come from the environment the caller
// built with `nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD`, so no credential is ever
// in this tree and no unit test ever dials anything (AGENTS.md rule 2).
//
// Every key it writes is under `ev:test-<stamp>`, never `cards:done`, so a live sprint's
// stream cannot be touched by a test run, and the stream is deleted at the end.
const (
	testEnable   = "NOVA_REDIS_TEST"
	testAddrEnv  = "NOVA_REDIS_ADDR"
	testUserEnv  = "NOVA_REDIS_USER"
	testPassEnv  = "NOVA_REDIS_BENCH_PASSWORD"
	testPrefix   = "ev:test-"
	testDoneBar  = 100
	testKillAt   = 50
	testReadSize = 10
)

// liveStore dials the fleet store, or skips. It returns the store and the stream name it
// minted for this run.
func liveStore(t *testing.T) (*RedisStore, string, Dial) {
	t.Helper()
	if os.Getenv(testEnable) != "1" {
		t.Skipf("set %s=1 (and %s, %s) to run the fold against the fleet store", testEnable, testAddrEnv, testPassEnv)
	}
	addr := os.Getenv(testAddrEnv)
	if addr == "" {
		t.Fatalf("%s=1 but %s is empty; it wants the fleet store as host:port", testEnable, testAddrEnv)
	}
	user := os.Getenv(testUserEnv)
	if user == "" {
		user = "bench"
	}
	stream := fmt.Sprintf("%s%s", testPrefix, time.Now().UTC().Format("20060102T150405Z"))
	dial := Dial{Addr: addr, Username: user, Password: os.Getenv(testPassEnv), Stream: stream}
	store, err := Open(context.Background(), dial)
	if err != nil {
		t.Fatalf("dialing the fleet store: %v", err)
	}
	t.Cleanup(func() {
		// The stream goes when the run does: a test that leaves keys behind is a test that
		// fills someone else's store.
		rdb := redis.NewClient(&redis.Options{Addr: dial.Addr, Username: dial.Username, Password: dial.Password})
		defer func() { _ = rdb.Close() }()
		if err := rdb.Del(context.Background(), stream).Err(); err != nil {
			t.Logf("could not delete %s: %v (the bench user may not hold DEL)", stream, err)
		}
		_ = store.Close()
	})
	return store, stream, dial
}

// cancelAfter is the kill: it cancels the fold's context once n entries have been read, in
// the middle of a batch, with rows committed, rows in flight and an ack not yet sent.
type cancelAfter struct {
	Reader
	n      int
	seen   int
	cancel context.CancelFunc
}

func (c *cancelAfter) Next(ctx context.Context, group, consumer string, count int, block time.Duration) ([]Entry, error) {
	entries, err := c.Reader.Next(ctx, group, consumer, count, block)
	c.seen += len(entries)
	if c.seen >= c.n && c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	return entries, err
}

// TestAHundredDonesAgainstTheFleetStore is bar 3 verbatim: a hundred DONEs are produced,
// the fold is killed half way through and restarted, and the count is a hundred. Then the
// same stream is rebuilt into a fresh file and the two row sets are compared byte for byte.
func TestAHundredDonesAgainstTheFleetStore(t *testing.T) {
	t.Parallel()

	store, stream, dial := liveStore(t)
	ctx := context.Background()
	group := "fold-test"
	dir := t.TempDir()

	for i := 0; i < testDoneBar; i++ {
		if _, err := store.Emit(ctx, Event{
			Label:     fmt.Sprintf("bench:test-card-%03d", i),
			Attempt:   1,
			Bench:     "studio",
			Model:     []string{"fable", "sonnet"}[i%2],
			Route:     []string{"studio", "hulk"}[i%2],
			Kind:      OK,
			TokensIn:  Int64(1000),
			TokensOut: Int64(100),
			USD:       Float64(0.01),
		}); err != nil {
			t.Fatalf("emitting DONE %d: %v", i, err)
		}
	}
	t.Logf("BAR3 emitted %d ok events to %s", testDoneBar, stream)

	db, err := OpenDB(ctx, filepath.Join(dir, "fold.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// The first fold, killed by a context cancel once fifty entries have been read.
	runCtx, cancel := context.WithCancel(ctx)
	killer := &cancelAfter{Reader: store, n: testKillAt, cancel: cancel}
	first := &Folder{Reader: killer, DB: db, Group: group, Consumer: "fold-a", Count: testReadSize}
	if err := first.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	firstStats, err := first.Run(runCtx, 10*time.Millisecond, nil, stream)
	cancel()
	if err != nil {
		t.Fatalf("the first fold: %v", err)
	}
	killed, err := db.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("BAR3 killed after read=%d new=%d acked=%d; the file holds %d rows", firstStats.Read, firstStats.Inserted, firstStats.Acked, killed)
	if killed >= testDoneBar {
		t.Fatalf("the kill left %d rows of %d folded; nothing was left for the restart to prove", killed, testDoneBar)
	}

	// The restart: a new process is a new consumer name, so the reclaim has to be the
	// group's pending list and not this consumer's memory.
	second, err := Open(ctx, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	restarted := &Folder{Reader: second, DB: db, Group: group, Consumer: "fold-b", Count: testReadSize}
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var after Stats
	for {
		s, err := restarted.Once(ctx)
		if err != nil {
			t.Fatalf("the restarted fold: %v", err)
		}
		if s.Read == 0 {
			break
		}
		after.Add(s)
	}

	count, err := db.CountKind(ctx, OK)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("BAR3 restarted read=%d new=%d acked=%d; the fold holds ok=%d", after.Read, after.Inserted, after.Acked, count)
	if count != testDoneBar {
		t.Fatalf("BAR3 FAIL the fold holds %d DONEs after a kill and a restart, want %d", count, testDoneBar)
	}
	t.Logf("BAR3 OK a hundred DONEs, the fold killed and restarted, the count is %d", count)

	// The rebuild: the whole stream replayed into a file that did not exist, compared with
	// the incremental fold's rows.
	rebuilt, err := OpenDB(ctx, filepath.Join(dir, "rebuild.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rebuilt.Close() }()
	stats, err := Rebuild(ctx, second, rebuilt, testReadSize)
	if err != nil {
		t.Fatalf("the rebuild: %v", err)
	}
	var a, b bytes.Buffer
	if err := db.Dump(ctx, &a); err != nil {
		t.Fatal(err)
	}
	if err := rebuilt.Dump(ctx, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("BAR3 FAIL the rebuild's rows differ from the fold's:\nfold:\n%s\nrebuild:\n%s", firstLines(a.String()), firstLines(b.String()))
	}
	t.Logf("BAR3 OK the rebuild read=%d new=%d and its %d bytes of rows are the fold's, byte for byte", stats.Read, stats.Inserted, a.Len())
}
