//go:build functional

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstrun_test.go pins the onboarding standard for this binary: the
// docs/TESTS.md `### First run` transcript is RUN, line for line and in order,
// through the one comparator (onboarding.CompareTranscript), and the usage
// banner's `example:` block is that same sitting. The sitting needs a store
// whose nova-config rows name ada and bob, so it runs on a throwaway
// redis-server with those two names in the `friends` set: the functional
// tier's. The documented lines name no --redis: each step runs with the
// throwaway store in NOVA_BUS_REDIS, which changes what the tool dials and
// nothing it prints. The run-owned values are the message's id (a ULID from
// the store's time) and its at, named from the one shared table.

// firstRunStore is a throwaway redis-server whose roster names ada and bob,
// each with a proven inbox push as its friend daemon writes it (bus.PushKey).
// The proofs are stamped a minute past the store's time, so names reads
// age=0s for the whole sitting however long the steps take.
func firstRunStore(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	require.NoError(t, c.SAdd(ctx, "friends", "ada", "bob").Err())
	now, err := c.Time(ctx).Result()
	require.NoError(t, err)
	for _, n := range []string{"ada", "bob"} {
		raw, err := json.Marshal(bus.PushProof{Name: n, Harness: "claude", Nonce: "first-run", Proven: now, Up: true, At: now.Add(time.Minute).UTC()})
		require.NoError(t, err)
		require.NoError(t, c.HSet(ctx, bus.PushKey, n, string(raw)).Err())
	}
	return addr
}

// documented runs this binary's own entry point with the documented
// arguments, the real store opened at the throwaway address.
func documented(addr string) onboarding.Runner {
	w := realWorld()
	w.getenv = func(k string) string {
		if k == RedisEnv {
			return addr
		}
		return ""
	}
	return func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb strings.Builder
		code := run(s.Args, strings.NewReader(""), &out, &errb, w)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	// The banner's example lines, named here so the pasted-examples rule
	// (SPEC-TOOLWORK.md documents rule 6) reads the command text in this test;
	// the transcript runs the same lines.
	documentedExamples := []string{
		"nova-bus wait --as bob --timeout 1s",
		`nova-bus send --as ada --to bob --subject hello --body "are you there?"`,
		"nova-bus peek --as bob",
		"nova-bus recv --as bob --exec true",
		"nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"nova-bus log --max 5",
		"nova-bus names",
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-bus")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-bus", lines)
	require.NoError(t, err)
	require.NotEmpty(t, steps, "the `### First run` block of docs/TESTS.md holds no nova-bus command")
	for _, verb := range []string{"wait", "send", "peek", "recv", "ack", "log", "names"} {
		found := false
		for _, s := range steps {
			found = found || (len(s.Args) > 0 && s.Args[0] == verb)
		}
		assert.True(t, found, "the first run never runs `nova-bus %s`", verb)
	}
	run := documented(firstRunStore(t))
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		res, err := run(s)
		require.NoError(t, err)
		got = append(got, res)
	}
	for _, p := range onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "id"}, {Name: "at"}}) {
		assert.Failf(t, "documented transcript differs", "docs/TESTS.md: %s", p)
	}

	// the banner's example block is that same sitting, line for line
	var out, errb strings.Builder
	require.Equal(t, 0, run0([]string{"help"}, &out, &errb), errb.String())
	examples, err := onboarding.ExampleLines(out.String(), "nova-bus")
	require.NoError(t, err)
	var doc []string
	for _, s := range steps {
		doc = append(doc, strings.TrimPrefix(s.Line, "$ "))
	}
	assert.Equal(t, doc, examples, "the banner's example block is not docs/TESTS.md's first run")
	assert.Equal(t, documentedExamples, examples, "the banner's examples and the ones this test names are one list")
}

func run0(args []string, out, errb *strings.Builder) int {
	return run(args, strings.NewReader(""), out, errb, realWorld())
}
