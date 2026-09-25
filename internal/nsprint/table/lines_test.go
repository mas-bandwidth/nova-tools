package table_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// lineFixture seeds the machine, ci, clock and process lines of section 6
// (#3045). Every timestamp is written relative to the Redis server's second at
// seed time so the render's ages are deterministic; the test reads the
// snapshot's own time record and builds the golden from the same deltas.
func lineFixture(seedSec int64) [][]string {
	at := func(deltaSec int64) string {
		return strconv.FormatInt((seedSec-deltaSec)*1000, 10)
	}
	return [][]string{
		{"SADD", "machines", "m1"},
		{"HSET", "machine:m1:ceiling", "slots", "8", "load1", "1.25"},
		{"SADD", "benches", "b1"},
		{"HSET", "bench:b1:desired", "slots", "4", "machine", "m1"},
		{"HSET", "bench:b1:beat", "at", "1"},
		{"ZADD", "bench:b1:living", at(0), "l1"},
		{"SADD", "sprints", "s1"},
		{"HSET", "s:s1", "status", "open", "clock", "60"},
		{"ZADD", "s:s1:clock", at(75), "oldest", at(10), "young"},
		{"SADD", "ci:cards", "r:cut1", "r:cut2", "r:run", "r:pending",
			"r:ok", "r:fail", "r:flaky", "r:missing", "r:blocked"},
		{"HSET", "ci:r:cut1", "state", "cut", "at", at(90)},
		{"HSET", "ci:r:cut2", "state", "cut", "at", at(0)},
		{"HSET", "ci:r:run", "state", "running", "at", at(0)},
		{"HSET", "ci:r:pending", "state", "PENDING", "at", at(0)},
		{"HSET", "ci:r:ok", "state", "OK", "at", at(0)},
		{"HSET", "ci:r:fail", "state", "FAIL", "at", at(0)},
		{"HSET", "ci:r:flaky", "state", "FLAKY", "at", at(0)},
		{"HSET", "ci:r:missing", "state", "MISSING-at-head", "at", at(0)},
		{"HSET", "ci:r:blocked", "state", "blocked-no-alternate-bench", "at", at(0)},
		{"HSET", "proc:reconciler", "pass_at", at(30)},
		{"HSET", "proc:router", "pass_at", at(0)},
		{"HSET", "proc:harvest:b1", "pass_at", at(0), "start_at", at(75)},
	}
}

func TestTableMachineCiClockLines(t *testing.T) {
	ctx := context.Background()
	client := controlStore(t)

	tm, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	seedSec := tm.Unix()
	seedCommands(t, client, lineFixture(seedSec))

	lines, err := table.ReadLines(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines.Errors) != 0 {
		t.Fatalf("clean fixture errors: %v", lines.Errors)
	}
	drift := lines.Time - seedSec

	// The machine line (2.4.5): ceiling, desired sum, living sum, load1.
	wantMachine := "machine:m1 | 8 | 4 | 1 | 1.25\n"

	// The ci line (6.4): cut 2, one each of the rest, oldest = drift+90 s.
	wantCi := "ci: cut 2, running 1, PENDING 1, OK 1, FAIL 1, FLAKY 1, MISSING-at-head 1, blocked-no-alternate-bench 1, oldest " +
		strconv.FormatInt(drift+90, 10) + "s\n"

	// The clock line: oldest item is 75 s old, past the 60 s clock.
	wantClock := "RED clock s1 oldest=" + strconv.FormatInt(drift+75, 10) + "s breach\n"

	// The process lines, sorted: backpressure, harvest:b1, reconciler, router.
	reconAge := strconv.FormatInt(drift+30, 10)
	startAge := strconv.FormatInt(drift+75, 10)
	upAge := strconv.FormatInt(drift, 10)
	wantProcs := "proc backpressure down age=? why=missing: pass\n" +
		"RED proc harvest:b1 up age=" + upAge + "s why=start " + startAge + "s\n" +
		"proc reconciler ? age=" + reconAge + "s\n" +
		"proc router up age=" + upAge + "s\n"

	want := wantMachine + wantCi + wantClock + wantProcs
	if got := lines.Render(); got != want {
		t.Fatalf("lines output differs\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestTableRendererLease(t *testing.T) {
	ctx := context.Background()
	client := controlStore(t)

	dir := t.TempDir()
	out := filepath.Join(dir, "table.txt")

	first := table.NewRenderer(client, out, time.Minute)
	ok, err := first.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("first renderer did not take the lease")
	}
	t.Cleanup(func() { _ = first.Release(ctx) })

	// A second renderer on the same --out refuses.
	second := table.NewRenderer(client, out, time.Minute)
	ok, err = second.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second renderer on the same --out was not refused")
	}

	// The atomic write: a reader racing the publisher never sees a partial
	// body, only the complete old or complete new one.
	bodyA := strings.Repeat("a", 1<<20)
	bodyB := strings.Repeat("b", 1<<20)
	if err := first.Publish(bodyA); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(out); err != nil {
		t.Fatal(err)
	} else if string(data) != bodyA {
		t.Fatalf("published body does not match (len=%d)", len(data))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				data, err := os.ReadFile(out)
				if err != nil {
					continue
				}
				s := string(data)
				if s != bodyA && s != bodyB {
					t.Errorf("partial write observed: len=%d", len(s))
					return
				}
			}
		}
	}()
	for i := 0; i < 60; i++ {
		if i%2 == 0 {
			if err := first.Publish(bodyB); err != nil {
				t.Fatal(err)
			}
		} else if err := first.Publish(bodyA); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
