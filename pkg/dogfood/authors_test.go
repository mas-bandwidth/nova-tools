package dogfood

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// withoutGitAuthorEnv is os.Environ without the variables that tell git who the
// author and committer are: a repository built here names its own per commit
// through git's config, and the ambient names a bench exports would otherwise
// win over every commit's own.
func withoutGitAuthorEnv() []string {
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GIT_AUTHOR_") || strings.HasPrefix(name, "GIT_COMMITTER_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func TestParseAuthorsReadsAMapping(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "authors.txt")
	body := "# who wrote what\n\nnova-check links = Rowan\nnova-fuse lift quarantine =  Stella \n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	authors, err := ParseAuthors(path)
	require.NoError(t, err, "ParseAuthors: %v", err)
	got := authors.Author("nova-check links")
	require.Equal(t, "Rowan", got, "author = %q, want Rowan", got)
	got = authors.Author("nova-fuse lift quarantine")
	require.Equal(t, "Stella", got, "a two-word verb mapped to %q, want Stella", got)
	got = authors.Author("nova-fuse  lift   quarantine")
	require.Equal(t, "Stella", got, "spacing changed the key: %q", got)
}

func TestParseAuthorsRefusesALineItCannotRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body, want string }{
		{"no equals", "nova-check links Rowan\n", "`=`"},
		{"empty name", "nova-check links =\n", "empty"},
		{"empty verb", " = Rowan\n", "empty"},
		{"mapped twice", "nova-check links = Rowan\nnova-check links = Stella\n", "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "authors.txt")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o644))
			_, err := ParseAuthors(path)
			require.Error(t, err, "a mapping that is half wrong was accepted")
			require.ErrorContains(t, err, tc.want, "refusal %q does not say %q", err, tc.want)
			require.True(t, strings.Contains(err.Error(), ":1") || strings.Contains(err.Error(), ":2"), "refusal %q names no line", err)
		})
	}
}

func TestAuthorsFromGitTakesTheCommitThatIntroducedTheVerb(t *testing.T) {
	t.Parallel()

	var asked [][]string
	run := func(ctx context.Context, dir string, args ...string) (string, error) {
		asked = append(asked, args)
		// The commit that introduced the verb is the first line: --reverse.
		return "Rowan Claude\nSomebody Later\n", nil
	}
	authors, err := AuthorsFromGit(context.Background(), "/repo", verbs("nova-check links"), run, nil)
	require.NoError(t, err, "AuthorsFromGit: %v", err)
	got := authors.Author("nova-check links")
	require.Equal(t, "Rowan Claude", got, "author = %q, want the first commit's author", got)
	require.Len(t, asked, 1, "git was asked %d times, want once per verb", len(asked))
	joined := strings.Join(asked[0], " ")
	for _, want := range []string{"log", "--reverse", "-S", "cmd/nova-check"} {
		require.Contains(t, joined, want, "git call %q is missing %q", joined, want)
	}
}

func TestAuthorsFromGitLeavesAVerbItCannotPlaceUnowned(t *testing.T) {
	t.Parallel()

	run := func(ctx context.Context, dir string, args ...string) (string, error) {
		return "", exec.ErrNotFound
	}
	authors, err := AuthorsFromGit(context.Background(), "/repo", verbs("nova-check links"), run, nil)
	require.NoError(t, err, "one unplaceable verb failed the whole run: %v", err)
	got := authors.Author("nova-check links")
	require.Empty(t, got, "author = %q, want none", got)
}

func TestAuthorsFromGitRefusesWithNoRepo(t *testing.T) {
	t.Parallel()

	_, err := AuthorsFromGit(context.Background(), " ", verbs("nova-check links"), nil, nil)
	require.Error(t, err, "an empty --repo was accepted; every path comes from a flag")
}

func TestAuthorsFromGitStopsOnADeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	run := func(ctx context.Context, dir string, args ...string) (string, error) { return "Rowan\n", nil }
	_, err := AuthorsFromGit(ctx, "/repo", verbs("nova-check links"), run, nil)
	require.Error(t, err, "a cancelled read ran on; a wait with no deadline is a line that is stuck")
}

func TestAuthorsFromGitReportsProgress(t *testing.T) {
	t.Parallel()

	var last int
	run := func(ctx context.Context, dir string, args ...string) (string, error) { return "Rowan\n", nil }
	_, err := AuthorsFromGit(context.Background(), "/repo",
		verbs("nova-check links", "nova-check nocode"), run,
		func(done, total int) { last = done },
	)
	require.NoError(t, err)
	require.Equal(t, 2, last, "progress stopped at %d of 2", last)
}

// One end-to-end read against a real git repository built here, so the flags
// this passes to git are the flags git actually accepts. Local only: nothing
// in this package reaches the network.
func TestAuthorsFromGitAgainstARealRepository(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_DATE=2026-09-18T09:00:00Z", "GIT_COMMITTER_DATE=2026-09-18T09:00:00Z",
			"GIT_AUTHOR_NAME=Rowan Claude", "GIT_AUTHOR_EMAIL=rowan@mas-bandwidth.com",
			"GIT_COMMITTER_NAME=Rowan Claude", "GIT_COMMITTER_EMAIL=rowan@mas-bandwidth.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Rowan Claude")
	git("config", "user.email", "rowan@mas-bandwidth.com")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "cmd", "nova-check"), 0o755))
	write := func(body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(repo, "cmd", "nova-check", "main.go"), []byte(body), 0o644))
	}
	write("package main\n\nfunc dispatch(v string) {\n\tswitch v {\n\tcase \"links\":\n\t}\n}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "the verb arrives")
	write("package main\n\nfunc dispatch(v string) {\n\tswitch v {\n\tcase \"links\":\n\tcase \"nocode\":\n\t}\n}\n")
	// The second commit is somebody else's. The author and committer variables
	// the helper exports to git are unset for this one call, because a variable
	// outranks the config a `-c` sets and only git's own config can name another.
	gitNoAuthorEnv := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(withoutGitAuthorEnv(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
	}
	gitNoAuthorEnv("config", "user.name", "Somebody Later")
	gitNoAuthorEnv("config", "user.email", "later@example.com")
	gitNoAuthorEnv("commit", "-qam", "a second verb, by somebody else")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	authors, err := AuthorsFromGit(ctx, repo, verbs("nova-check links", "nova-check nocode"), nil, nil)
	require.NoError(t, err, "AuthorsFromGit: %v", err)
	got := authors.Author("nova-check links")
	require.Equal(t, "Rowan Claude", got, "links author = %q, want Rowan Claude", got)
	got = authors.Author("nova-check nocode")
	require.Equal(t, "Somebody Later", got, "nocode author = %q, want Somebody Later", got)
}

// A fake clock, so the progress policy is tested and the suite waits for
// nothing: the whole of both tests below is arithmetic.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time       { return c.at }
func (c *fakeClock) tick(d time.Duration) { c.at = c.at.Add(d) }

func TestProgressSaysNothingOnARunThatAnswersAtOnce(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)}
	said := 0
	progress := NewProgress(clock.now, 100*time.Millisecond, 2*time.Second, func(done, total int) { said++ })
	clock.tick(10 * time.Millisecond)
	progress(1, 2)
	clock.tick(10 * time.Millisecond)
	progress(2, 2)
	require.Equal(t, 0, said, "a run that answered at once printed %d progress lines", said)
}

func TestProgressSpeaksUpOnARunThatLooksLikeAHangAndThenHoldsItsPace(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)}
	var said [][2]int
	progress := NewProgress(clock.now, 100*time.Millisecond, 2*time.Second, func(done, total int) {
		said = append(said, [2]int{done, total})
	})
	clock.tick(500 * time.Millisecond)
	progress(1, 4) // past --after: the first line
	clock.tick(100 * time.Millisecond)
	progress(2, 4) // inside the interval: silence
	clock.tick(3 * time.Second)
	progress(3, 4) // past the interval: a second line
	clock.tick(10 * time.Millisecond)
	progress(4, 4) // the last one always speaks
	want := [][2]int{{1, 4}, {3, 4}, {4, 4}}
	require.Equal(t, want, said, "progress lines %v, want %v", said, want)
}

// A bare key (the tool's own invocation, Verb "") used to index an empty
// field list and panic (nova-tools #3160). It is placed by the commit that
// first added a file under cmd/<tool>, and a verb with words keeps its -S read.
func TestAuthorsFromGitBareKey(t *testing.T) {
	t.Parallel()

	var calls [][]string
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		return "Ada\nBob\n", nil
	}
	verbs := []Verb{{Tool: "nova-fix"}, {Tool: "nova-fix", Verb: "links"}}
	authors, err := AuthorsFromGit(context.Background(), "/repo", verbs, run, nil)
	require.NoError(t, err, "AuthorsFromGit: %v", err)
	require.Len(t, calls, 2, "%d git calls, want 2: %q", len(calls), calls)
	want := "log --no-textconv --no-ext-diff --reverse --diff-filter=A --format=%an -- cmd/nova-fix"
	got := strings.Join(calls[0], " ")
	require.Equal(t, want, got, "bare key read %q, want %q", got, want)
	for _, a := range calls[0] {
		require.NotEqual(t, "-S", a, "bare key read carries -S: %q", calls[0])
	}
	got = authors.Author("nova-fix")
	require.Equal(t, "Ada", got, "Author(nova-fix) = %q, want Ada", got)
	words := strings.Join(calls[1], " ")
	require.True(t, strings.Contains(words, "-S"), "nova-fix links read %q, want the -S read under cmd/nova-fix", words)
	require.True(t, strings.HasSuffix(words, "-- cmd/nova-fix"), "nova-fix links read %q, want the -S read under cmd/nova-fix", words)
	got = authors.Author("nova-fix links")
	require.Equal(t, "Ada", got, "Author(nova-fix links) = %q, want Ada", got)
}

// The authorship read must not run a program the repository's config names:
// the pickaxe -S diffs file content, a committed .gitattributes can select a
// diff driver for it, and the repository's local config can give that driver
// a textconv -- a program run as the invoking user for every file it diffs
// (security#77 finding 2, re-filed from security#56 finding 2).
func TestAuthorsFromGitDoesNotRunATextconvNamedInRepoConfig(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_DATE=2026-09-18T09:00:00Z", "GIT_COMMITTER_DATE=2026-09-18T09:00:00Z",
			"GIT_AUTHOR_NAME=Rowan Claude", "GIT_AUTHOR_EMAIL=rowan@mas-bandwidth.com",
			"GIT_COMMITTER_NAME=Rowan Claude", "GIT_COMMITTER_EMAIL=rowan@mas-bandwidth.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Rowan Claude")
	git("config", "user.email", "rowan@mas-bandwidth.com")
	// A diff driver the committed attributes select, whose textconv proves it
	// ran: both live in local config and the committed tree, exactly the
	// untrusted position the read must not execute.
	probe := filepath.Join(t.TempDir(), "probe")
	script := filepath.Join(t.TempDir(), "textconv.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ncat \"$1\"\necho ran >> "+probe+"\n"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "cmd", "nova-fake"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("cmd/nova-fake/*.go diff=boom\n"), 0o644))
	git("config", "diff.boom.textconv", script)
	write := func(body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(repo, "cmd", "nova-fake", "main.go"), []byte(body), 0o644))
	}
	write("package main\n\nfunc dispatch(v string) {\n\tswitch v {\n\tcase \"links\":\n\t}\n}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "the verb arrives")
	write("package main\n\nfunc dispatch(v string) {\n\tswitch v {\n\tcase \"links\":\n\tcase \"nocode\":\n\t}\n}\n")
	// The second commit is somebody else's, as in the real-repository test.
	gitNoAuthorEnv := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(withoutGitAuthorEnv(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
	}
	gitNoAuthorEnv("config", "user.name", "Somebody Later")
	gitNoAuthorEnv("config", "user.email", "later@example.com")
	gitNoAuthorEnv("commit", "-qam", "a second verb, by somebody else")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	authors, err := AuthorsFromGit(ctx, repo, verbs("nova-fake links", "nova-fake nocode"), GitRunner, nil)
	require.NoError(t, err, "AuthorsFromGit: %v", err)
	got := authors.Author("nova-fake links")
	require.Equal(t, "Rowan Claude", got, "links author = %q, want Rowan Claude", got)
	got = authors.Author("nova-fake nocode")
	require.Equal(t, "Somebody Later", got, "nocode author = %q, want Somebody Later", got)
	_, statErr := os.Stat(probe)
	require.True(t, os.IsNotExist(statErr), "the repository's textconv ran: %s exists", probe)

	// Both argv spellings refuse the diff helpers, whatever the config names.
	for _, v := range []Verb{
		{Tool: "nova-fake", Verb: "links nocode", Line: 1},
		{Tool: "nova-fake", Verb: "", Line: 2},
	} {
		args := authorArgs(v)
		require.Contains(t, args, "--no-textconv", "authorArgs(verb=%q) = %v", v.Verb, args)
		require.Contains(t, args, "--no-ext-diff", "authorArgs(verb=%q) = %v", v.Verb, args)
	}
}
