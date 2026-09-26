//go:build functional

package reconcile

// FG-A fix 4: only exit 1 of cat-file -e means the sha is absent from the
// mirror. A git that did not answer -- a directory git cannot open, the time
// limit -- is an error naming the mirror, the git command and its stderr,
// with nova-sprint mirror check / mirror refresh as the next action, never a
// WAIT that reads as "not fetched yet" on every pass.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// bareMirror is a bare repository with one commit on dev.
func bareMirror(t *testing.T) (mirror, sha string) {
	t.Helper()
	dir := t.TempDir()
	git := func(in string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = in
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	work := filepath.Join(dir, "work")
	git(dir, "init", "-q", "-b", "dev", work)
	if err := os.WriteFile(filepath.Join(work, "a"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(work, "add", "a")
	git(work, "commit", "-q", "-m", "a")
	sha = git(work, "rev-parse", "HEAD")
	mirror = filepath.Join(dir, "m.git")
	git(dir, "clone", "-q", "--bare", work, mirror)
	return mirror, sha
}

func TestOnBaseAbsentShaIsStillNotYet(t *testing.T) {
	t.Parallel()
	mirror, _ := bareMirror(t)
	d := &DoneAlready{Mirror: func(string) string { return mirror }}
	verdict, why := d.onBase(context.Background(), "nova-tools", strings.Repeat("0", 40), "dev")
	if verdict != "wait" || !strings.HasSuffix(why, "is not in the mirror of nova-tools yet") {
		t.Fatalf("an absent sha in a working mirror: %q %q; want wait, not yet", verdict, why)
	}
}

// An abbreviated sha the mirror cannot resolve is absent too (rev-parse exit
// 1), not a broken mirror.
func TestOnBaseAbbreviatedAbsentShaIsNotYet(t *testing.T) {
	t.Parallel()
	mirror, _ := bareMirror(t)
	d := &DoneAlready{Mirror: func(string) string { return mirror }}
	verdict, why := d.onBase(context.Background(), "nova-tools", "0123456", "dev")
	if verdict != "wait" || !strings.HasSuffix(why, "is not in the mirror of nova-tools yet") {
		t.Fatalf("an absent abbreviated sha: %q %q; want wait, not yet", verdict, why)
	}
}

// A sha the mirror holds that is not a commit is a no, named, never a yes or
// a wait.
func TestOnBaseBlobShaIsNo(t *testing.T) {
	t.Parallel()
	mirror, _ := bareMirror(t)
	cmd := exec.Command("git", "--git-dir", mirror, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader("x\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hash-object: %v %s", err, out)
	}
	blob := strings.TrimSpace(string(out))
	d := &DoneAlready{Mirror: func(string) string { return mirror }}
	verdict, why := d.onBase(context.Background(), "nova-tools", blob, "dev")
	if verdict != "no" || !strings.Contains(why, "is a blob, not a commit") {
		t.Fatalf("a blob sha: %q %q; want no naming the type", verdict, why)
	}
}

func TestOnBasePresentShaIsYes(t *testing.T) {
	t.Parallel()
	mirror, sha := bareMirror(t)
	d := &DoneAlready{Mirror: func(string) string { return mirror }}
	if verdict, why := d.onBase(context.Background(), "nova-tools", sha, "dev"); verdict != "yes" {
		t.Fatalf("the dev tip: %q %q; want yes", verdict, why)
	}
}

func TestOnBaseBrokenMirrorNamesCommandAndRepair(t *testing.T) {
	t.Parallel()
	mirror := filepath.Join(t.TempDir(), "nova-tools.git")
	if err := os.MkdirAll(filepath.Join(mirror, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &DoneAlready{Mirror: func(string) string { return mirror }}
	verdict, why := d.onBase(context.Background(), "nova-tools", strings.Repeat("0", 40), "dev")
	if verdict != "error" {
		t.Fatalf("a mirror git cannot open: %q %q; want error", verdict, why)
	}
	for _, want := range []string{"git --git-dir " + mirror + " rev-parse --verify -q", "exit status 128", "not a git repository", "nova-sprint mirror check", "nova-sprint mirror refresh"} {
		if !strings.Contains(why, want) {
			t.Fatalf("why %q does not name %q", why, want)
		}
	}
}

func TestOnBaseTimeLimitIsAnErrorNamingTheLimit(t *testing.T) {
	t.Parallel()
	mirror, sha := bareMirror(t)
	d := &DoneAlready{Mirror: func(string) string { return mirror }, Git: time.Nanosecond}
	verdict, why := d.onBase(context.Background(), "nova-tools", sha, "dev")
	if verdict != "error" || !strings.Contains(why, "did not finish within 1ns") || !strings.Contains(why, "nova-sprint mirror check") {
		t.Fatalf("a git that hit the limit: %q %q; want error naming the limit and the repair", verdict, why)
	}
}

// The duty: a card whose mirror git cannot open is the pass's error naming
// the card, not a WAIT outcome.
func TestOneBrokenMirrorIsTheCardsError(t *testing.T) {
	t.Parallel()
	mirror := filepath.Join(t.TempDir(), "nova-tools.git")
	if err := os.MkdirAll(filepath.Join(mirror, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	d := &DoneAlready{Client: c, Mirror: func(string) string { return mirror }}
	card := doneAlreadyCard{label: "L1", line2: "ABSTAIN done-already " + strings.Repeat("0", 40), origin: "mas-bandwidth/nova-tools#1", repo: "nova-tools", base: "dev"}
	o, err := d.one(context.Background(), "tok", "S", card)
	if err == nil || !strings.Contains(err.Error(), "done-already S/L1: git --git-dir "+mirror+" rev-parse") || !strings.Contains(err.Error(), "nova-sprint mirror check") {
		t.Fatalf("err %v outcome %+v; want the card's error naming the git command and the repair", err, o)
	}
	if o.Action == "WAIT" {
		t.Fatalf("a broken mirror reads as WAIT: %+v", o)
	}
}
