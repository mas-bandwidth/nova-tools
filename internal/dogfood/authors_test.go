package dogfood

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAuthorsReadsAMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "authors.txt")
	body := "# who wrote what\n\nnova-check links = Rowan\nnova-fuse lift quarantine =  Stella \n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	authors, err := ParseAuthors(path)
	if err != nil {
		t.Fatalf("ParseAuthors: %v", err)
	}
	if got := authors.Author("nova-check links"); got != "Rowan" {
		t.Fatalf("author = %q, want Rowan", got)
	}
	if got := authors.Author("nova-fuse lift quarantine"); got != "Stella" {
		t.Fatalf("a two-word verb mapped to %q, want Stella", got)
	}
	if got := authors.Author("nova-fuse  lift   quarantine"); got != "Stella" {
		t.Fatalf("spacing changed the key: %q", got)
	}
}

func TestParseAuthorsRefusesALineItCannotRead(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no equals", "nova-check links Rowan\n", "`=`"},
		{"empty name", "nova-check links =\n", "empty"},
		{"empty verb", " = Rowan\n", "empty"},
		{"mapped twice", "nova-check links = Rowan\nnova-check links = Stella\n", "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "authors.txt")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ParseAuthors(path)
			if err == nil {
				t.Fatal("a mapping that is half wrong was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not say %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), ":1") && !strings.Contains(err.Error(), ":2") {
				t.Fatalf("refusal %q names no line", err)
			}
		})
	}
}

func TestAuthorsFromGitTakesTheCommitThatIntroducedTheVerb(t *testing.T) {
	var asked [][]string
	run := func(ctx context.Context, dir string, args ...string) (string, error) {
		asked = append(asked, args)
		// The commit that introduced the verb is the first line: --reverse.
		return "Rowan Claude\nSomebody Later\n", nil
	}
	authors, err := AuthorsFromGit(context.Background(), "/repo", verbs("nova-check links"), run, nil)
	if err != nil {
		t.Fatalf("AuthorsFromGit: %v", err)
	}
	if got := authors.Author("nova-check links"); got != "Rowan Claude" {
		t.Fatalf("author = %q, want the first commit's author", got)
	}
	if len(asked) != 1 {
		t.Fatalf("git was asked %d times, want once per verb", len(asked))
	}
	joined := strings.Join(asked[0], " ")
	for _, want := range []string{"log", "--reverse", "-S", "cmd/nova-check"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("git call %q is missing %q", joined, want)
		}
	}
}

func TestAuthorsFromGitLeavesAVerbItCannotPlaceUnowned(t *testing.T) {
	run := func(ctx context.Context, dir string, args ...string) (string, error) {
		return "", exec.ErrNotFound
	}
	authors, err := AuthorsFromGit(context.Background(), "/repo", verbs("nova-check links"), run, nil)
	if err != nil {
		t.Fatalf("one unplaceable verb failed the whole run: %v", err)
	}
	if got := authors.Author("nova-check links"); got != "" {
		t.Fatalf("author = %q, want none", got)
	}
}

func TestAuthorsFromGitRefusesWithNoRepo(t *testing.T) {
	if _, err := AuthorsFromGit(context.Background(), " ", verbs("nova-check links"), nil, nil); err == nil {
		t.Fatal("an empty --repo was accepted; every path comes from a flag")
	}
}

func TestAuthorsFromGitStopsOnADeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := func(ctx context.Context, dir string, args ...string) (string, error) { return "Rowan\n", nil }
	if _, err := AuthorsFromGit(ctx, "/repo", verbs("nova-check links"), run, nil); err == nil {
		t.Fatal("a cancelled read ran on; a wait with no deadline is a line that is stuck")
	}
}

func TestAuthorsFromGitReportsProgress(t *testing.T) {
	var last int
	run := func(ctx context.Context, dir string, args ...string) (string, error) { return "Rowan\n", nil }
	_, err := AuthorsFromGit(context.Background(), "/repo",
		verbs("nova-check links", "nova-check nocode"), run,
		func(done, total int) { last = done },
	)
	if err != nil {
		t.Fatal(err)
	}
	if last != 2 {
		t.Fatalf("progress stopped at %d of 2", last)
	}
}

// One end-to-end read against a real git repository built here, so the flags
// this passes to git are the flags git actually accepts. Local only: nothing
// in this package reaches the network.
func TestAuthorsFromGitAgainstARealRepository(t *testing.T) {
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
			"GIT_COMMITTER_NAME=Rowan Claude", "GIT_COMMITTER_EMAIL=rowan@mas-bandwidth.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Rowan Claude")
	git("config", "user.email", "rowan@mas-bandwidth.com")
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "nova-check"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "cmd", "nova-check", "main.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package main\n\nfunc dispatch(v string) {\n\tswitch v {\n\tcase \"links\":\n\t}\n}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "the verb arrives")
	write("package main\n\nfunc dispatch(v string) {\n\tswitch v {\n\tcase \"links\":\n\tcase \"nocode\":\n\t}\n}\n")
	git("-c", "user.name=Somebody Later", "-c", "user.email=later@example.com",
		"commit", "-qam", "a second verb, by somebody else")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	authors, err := AuthorsFromGit(ctx, repo, verbs("nova-check links", "nova-check nocode"), nil, nil)
	if err != nil {
		t.Fatalf("AuthorsFromGit: %v", err)
	}
	if got := authors.Author("nova-check links"); got != "Rowan Claude" {
		t.Fatalf("links author = %q, want Rowan Claude", got)
	}
	if got := authors.Author("nova-check nocode"); got != "Somebody Later" {
		t.Fatalf("nocode author = %q, want Somebody Later", got)
	}
}

// A fake clock, so the progress policy is tested and the suite waits for
// nothing: the whole of both tests below is arithmetic.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time       { return c.at }
func (c *fakeClock) tick(d time.Duration) { c.at = c.at.Add(d) }

func TestProgressSaysNothingOnARunThatAnswersAtOnce(t *testing.T) {
	clock := &fakeClock{at: time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)}
	said := 0
	progress := NewProgress(clock.now, 100*time.Millisecond, 2*time.Second, func(done, total int) { said++ })
	clock.tick(10 * time.Millisecond)
	progress(1, 2)
	clock.tick(10 * time.Millisecond)
	progress(2, 2)
	if said != 0 {
		t.Fatalf("a run that answered at once printed %d progress lines", said)
	}
}

func TestProgressSpeaksUpOnARunThatLooksLikeAHangAndThenHoldsItsPace(t *testing.T) {
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
	if len(said) != len(want) {
		t.Fatalf("progress lines %v, want %v", said, want)
	}
	for i := range want {
		if said[i] != want[i] {
			t.Fatalf("progress lines %v, want %v", said, want)
		}
	}
}
