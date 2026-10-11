//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstrun_test.go pins the onboarding standard for this binary: the
// docs/TESTS.md `### First run` transcript is RUN, line for line and in order,
// through the one comparator (onboarding.CompareTranscript), and the usage
// banner's `example:` block is that same sitting. The sitting is the canary
// by hand with no daemon: a dry-run install, a dry-run uninstall, a dry-run
// host, a ping as the coordinator, the session's pong, the wait for it, and
// the status of a directory no daemon has run in. It needs a store whose nova-config rows
// name ada and bob, so it runs on a throwaway redis-server with those two
// names in the `friends` set, its address in NOVA_BUS_REDIS, which changes
// what the tool dials and nothing it prints. `./` is a directory of the
// test's own, and the home directory and uid are the test's, so the plist
// path reads as the document writes it; bob's directory is made there first,
// since install refuses a --dir that is not a real directory. The run-owned values are the
// message ids, their at, and how long the wait took.

func firstRunStore(t *testing.T) string {
	t.Helper()
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	require.NoError(t, c.SAdd(context.Background(), "friends", "ada", "bob").Err())
	return addr
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	documentedExamples := []string{
		"nova-friend install --as bob --harness opencode --dir ./bob --dry-run",
		"nova-friend uninstall --as bob --dry-run",
		"nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider",
		"nova-friend ping --as ada --to bob --nonce abc123",
		"nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4",
		"nova-friend wait-pong --from bob --nonce abc123 --timeout 2s",
		"nova-friend status --as bob --dir ./bob",
	}
	raw := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md"))
	lines, err := onboarding.FirstRun(raw, "nova-friend")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-friend", lines)
	require.NoError(t, err)
	require.NotEmpty(t, steps, "the `### First run` block of docs/TESTS.md holds no nova-friend command")
	var commands []string
	for _, s := range steps {
		commands = append(commands, "nova-friend "+strings.Join(s.Args, " "))
	}
	require.Equal(t, strings.Join(documentedExamples, "\n"), strings.Join(commands, "\n"), "the transcript and the examples this test names are one list")

	dir := t.TempDir()
	// bob's directory is there, as it is for a friend being installed: install,
	// --dry-run too, refuses a --dir that is not a real directory
	// (friend.ErrNotRealDir) and never makes it.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "bob"), 0o755))
	w := realWorld()
	addr := firstRunStore(t)
	w.getenv = func(k string) string {
		if k == RedisEnv {
			return addr
		}
		return ""
	}
	w.home, w.uid = filepath.Join(dir, "home"), 501
	w.binary = func() (string, error) { return filepath.Join(dir, "nova-friend"), nil }
	stand := strings.NewReplacer("./", dir+"/")
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		args := []string{}
		for _, a := range s.Args {
			args = append(args, stand.Replace(a))
		}
		var out, errb strings.Builder
		code := run(args, strings.NewReader(""), &out, &errb, w)
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	volatile := []onboarding.Field{{Name: "tmpdir", Doc: ".", Run: dir}, {Name: "id"}, {Name: "at"}, {Name: "took"}}
	for _, p := range onboarding.CompareTranscript(steps, got, volatile) {
		assert.Failf(t, "documented transcript differs", "docs/TESTS.md: %s", p)
	}

	var out, errb strings.Builder
	require.Equal(t, 0, run([]string{"help"}, strings.NewReader(""), &out, &errb, w), errb.String())
	examples, err := onboarding.ExampleLines(out.String(), "nova-friend")
	require.NoError(t, err)
	var doc []string
	for _, s := range steps {
		doc = append(doc, strings.TrimPrefix(s.Line, "$ "))
	}
	assert.Equal(t, doc, examples, "the banner's example block is not docs/TESTS.md's first run")
	assert.Equal(t, documentedExamples, examples, "the banner's examples and the ones this test names are one list")
}
