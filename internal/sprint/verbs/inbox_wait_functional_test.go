//go:build functional

package verbs

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

// jrWaitBound is the allowed poll bound: NOVA_TEST_WAIT when set, thirty
// seconds otherwise.
func jrWaitBound(t *testing.T) time.Duration {
	t.Helper()
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("NOVA_TEST_WAIT=%q: %v", v, err)
		}
		return d
	}
	return 30 * time.Second
}

// TestInboxWaitWakesOnTheStore: on a real store, a wait blocked in XREAD on
// the log wakes when a step appends a JUDGMENT line: the step's XADD, in Layer
// 2's shape (id <seq>-0, fields n and d), is answered to the blocked reader at
// once, after the DECIDED line before it was read past. The time from the
// XADD to the wait's return is logged, not asserted (a wall-clock bound is a
// load test, docs/SPEC-CI.md's waits rule); the block the wait asked for is
// the poll bound, so a return with the judgment is the append's wake. Then
// the blocked reader's connection is killed and a judgment appended: the wait
// reads again, on a new connection, from its cursor, and returns it. It needs
// the stream only, not Layer 2's functions, so it runs without them.
func TestInboxWaitWakesOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	reader := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	admin := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { _ = reader.Close(); _ = admin.Close() })
	ctx := context.Background()
	bound := jrWaitBound(t)
	key := logKey(jrPrefix, 0)

	type outcome struct {
		got waited
		err error
		at  time.Time
	}
	start := func(after uint64) <-chan outcome {
		done := make(chan outcome, 1)
		go func() {
			got, err := waitJudgment(ctx, RedisNotes{C: reader}, jrPrefix, 0, after, time.Now().Add(bound), time.Now)
			done <- outcome{got: got, err: err, at: time.Now()}
		}()
		return done
	}
	// blocked is the id of the client blocked in XREAD, polled for up to the
	// bound.
	blocked := func() string {
		t.Helper()
		deadline := time.Now().Add(bound)
		for time.Now().Before(deadline) {
			list, err := admin.ClientList(ctx).Result()
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(list, "\n") {
				f := map[string]string{}
				for _, kv := range strings.Fields(line) {
					if k, v, ok := strings.Cut(kv, "="); ok {
						f[k] = v
					}
				}
				if f["cmd"] == "xread" && strings.Contains(f["flags"], "b") {
					return f["id"]
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.Fatalf("no reader blocked in XREAD within %s", bound)
		return ""
	}
	xadd := func(seq, kind string) time.Time {
		t.Helper()
		d, err := json.Marshal(map[string]any{"k": "n", "ms": "1790000000000", "about": []string{"deal"},
			"meta": map[string]any{"kind": kind, "type": typeStepRefused, "cause": "LIMIT", "op": "open", "text": "raised by the test"}})
		if err != nil {
			t.Fatal(err)
		}
		at := time.Now()
		if err := admin.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: seq + "-0", Values: []string{"n", "0", "d", string(d)}}).Err(); err != nil {
			t.Fatal(err)
		}
		return at
	}
	result := func(done <-chan outcome) outcome {
		t.Helper()
		select {
		case o := <-done:
			if o.err != nil {
				t.Fatal(o.err)
			}
			return o
		case <-time.After(bound):
			t.Fatalf("the wait did not return within %s", bound)
		}
		return outcome{}
	}

	done := start(0)
	blocked()
	xadd("1", sprint.Decided)
	blocked()
	appended := xadd("2", sprint.Judgment)
	o := result(done)
	t.Logf("the blocked wait returned %s after the judgment's XADD", o.at.Sub(appended))
	if !o.got.Found || o.got.ID != "n2" || o.got.Last != 2 || o.got.Line.meta("type") != typeStepRefused {
		t.Fatalf("the wait returned %+v, want the judgment n2", o.got)
	}

	done = start(2)
	id := blocked()
	if err := admin.ClientKillByFilter(ctx, "ID", id).Err(); err != nil {
		t.Fatal(err)
	}
	xadd("3", sprint.Judgment)
	o = result(done)
	if !o.got.Found || o.got.ID != "n3" {
		t.Fatalf("after the reader's connection was killed the wait returned %+v, want n3 from its cursor", o.got)
	}
}
