package main

// react_test.go drives the react verb with miniredis as the bus and a fake forge, so the
// test reaches no network and starts no redis server of its own.

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

type reactFakeForge struct{ snap ci.Snapshot }

func (f *reactFakeForge) Snapshot() (ci.Snapshot, error) { return f.snap, nil }

func reactWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// TestReactRefusesMissingRedis: --redis is required and its absence is exit 2.
func TestReactRefusesMissingRedis(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"react"}, &out, &errb, production())
	if code != 2 {
		t.Fatalf("react without --redis exit = %d, want 2; stderr=%s", code, errb.String())
	}
	if !bytes.Contains(errb.Bytes(), []byte("--redis")) {
		t.Errorf("the refusal does not name --redis: %s", errb.String())
	}
}

// TestReactOnceEnqueuesAGreenPR: a pr-checks-done message lands the PR in the merge queue
// set. The producer is not subscribed until the verb is running, so the test republishes
// until it is consumed, which is what a durable bus would do with the message instead.
func TestReactOnceEnqueuesAGreenPR(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	deps := Deps{
		Now:  func() time.Time { return time.Now().UTC() },
		Dial: func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(_, _ string, _ time.Duration) ci.Forge {
			return &reactFakeForge{}
		},
	}
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"react", "--redis", mr.Addr(), "--once", "--deadline", "60"}, &out, &errb, deps)
	}()

	payload := `{"number":42,"head":"a1b2","conclusion":"SUCCESS"}`
	wait := reactWait()
	deadline := time.After(wait)
	for {
		if rdb.SIsMember(ctx, "merge:queue", "42").Val() {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("react never enqueued PR 42 within %s", wait)
		default:
		}
		if err := rdb.Publish(ctx, ci.ChannelPRChecksDone, payload).Err(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code := <-done; code != 0 {
		t.Fatalf("react --once exit = %d, stderr=%s", code, errb.String())
	}
}
