package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// sweepCalls is INFO commandstats' call count of every command but INFO.
func sweepCalls(t *testing.T, c *redis.Client) int64 {
	t.Helper()
	raw, err := c.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, line := range strings.Split(raw, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.HasPrefix(name, "cmdstat_") || name == "cmdstat_info" {
			continue
		}
		for _, kv := range strings.Split(rest, ",") {
			if v, ok := strings.CutPrefix(kv, "calls="); ok {
				k, _ := strconv.ParseInt(v, 10, 64)
				n += k
			}
		}
	}
	return n
}

// TestBenchSweepVerbRefusesBeforeRedis (#2632): the jobs root comes only from
// NOVA_CARD_JOBS, and a root in argv, a positional argument, --dry-run with
// --loop, or a bad NOVA_CARD_JOBS exits 2 before any Redis command.
func TestBenchSweepVerbRefusesBeforeRedis(t *testing.T) {
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	good := t.TempDir()
	cases := []struct {
		env  string
		args []string
	}{
		{good, []string{"--bench", "b1", "--redis", addr, "--jobs-root", good}},
		{good, []string{"--bench", "b1", "--redis", addr, good}},
		{good, []string{"--bench", "b1", "--redis", addr, "--dry-run", "--loop", "10"}},
		{"", []string{"--bench", "b1", "--redis", addr}},
		{"relative/jobs", []string{"--bench", "b1", "--redis", addr}},
		{"/", []string{"--bench", "b1", "--redis", addr}},
		{good + "/../" + good[strings.LastIndex(good, "/")+1:], []string{"--bench", "b1", "--redis", addr}},
	}
	for _, tc := range cases {
		t.Setenv("NOVA_CARD_JOBS", tc.env)
		before := sweepCalls(t, c)
		var out, errOut bytes.Buffer
		code := runBenchSweep(context.Background(), tc.args, &out, &errOut)
		if code != 2 {
			t.Fatalf("env=%q args=%q: exit %d, want 2 (out %q err %q)", tc.env, tc.args, code, out.String(), errOut.String())
		}
		if after := sweepCalls(t, c); after != before {
			t.Fatalf("env=%q args=%q: %d Redis commands before refusing", tc.env, tc.args, after-before)
		}
	}
}

// TestBenchSweepVerbEmptyBench: a good root and no ended cards is one
// summary line, exit 0, and the lock released.
func TestBenchSweepVerbEmptyBench(t *testing.T) {
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_CARD_JOBS", t.TempDir())
	for _, dry := range []bool{false, true} {
		args := []string{"--bench", "b1", "--redis", addr}
		want := "SWEEP bench=b1 swept=0 kept=0 at="
		if dry {
			args = append(args, "--dry-run")
			want = "SWEEP bench=b1 dry-run=1 would=0 kept=0 at="
		}
		var out, errOut bytes.Buffer
		if code := runBenchSweep(context.Background(), args, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), want) {
			t.Fatalf("dry=%v: exit %d out %q err %q", dry, code, out.String(), errOut.String())
		}
	}
	if n, err := c.Exists(context.Background(), "bench:b1:sweep:lock").Result(); err != nil || n != 0 {
		t.Fatalf("lock left (%d, %v)", n, err)
	}
}
