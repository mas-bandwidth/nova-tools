package landed

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// deepOrigin is a 700-commit forge repository on disk, built in one git
// fast-import so the test stays fast. dev is c1..c700, one minute apart, each
// commit bumping n.txt:
//
//	pull/7   c400 + f.txt A -> B, 30 s after c400   (the old window)
//	c450     f.txt = B       the lander's commit for #7
//	c451     f.txt = C       a later PR rewrote it: the tip conflicts with #7
//	pull/8   c690 + g.txt X -> Y, 30 s after c690   (a window inside depth 50)
//	c695     g.txt = Y       the lander's commit for #8
//	c696     g.txt = Z
//
// #7's window starts 300 commits under the tip, past a --depth 50 clone; #8's
// starts inside it. It returns the file:// URL and each commit by its number
// (pull heads at 1007 and 1008).
func deepOrigin(t *testing.T) (string, map[int]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, filepath.Dir(dir), "init", "-q", "--bare", dir)
	gitIn(t, dir, "config", "uploadpack.allowFilter", "true")
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	at := func(i int) int64 { return start + int64(i)*60 }
	var b strings.Builder
	file := func(name, body string) {
		fmt.Fprintf(&b, "M 644 inline %s\ndata %d\n%s\n", name, len(body), body)
	}
	head := func(ref string, mark int, when int64, msg string) {
		fmt.Fprintf(&b, "commit %s\nmark :%d\ncommitter t <t@example.com> %d +0000\ndata %d\n%s\n",
			ref, mark, when, len(msg), msg)
	}
	f, g := "A\n", "X\n"
	for i := 1; i <= 700; i++ {
		switch i {
		case 450:
			f = "B\n"
		case 451:
			f = "C\n"
		case 695:
			g = "Y\n"
		case 696:
			g = "Z\n"
		}
		head("refs/heads/dev", i, at(i), "c"+strconv.Itoa(i))
		file("n.txt", strconv.Itoa(i)+"\n")
		file("f.txt", f)
		file("g.txt", g)
	}
	head("refs/pull/7/head", 1007, at(400)+30, "pull 7")
	fmt.Fprintf(&b, "from :400\n")
	file("f.txt", "B\n")
	head("refs/pull/8/head", 1008, at(690)+30, "pull 8")
	fmt.Fprintf(&b, "from :690\n")
	file("g.txt", "Y\n")
	marks := filepath.Join(t.TempDir(), "marks")
	cmd := exec.Command("git", "-C", dir, "fast-import", "--quiet", "--export-marks="+marks)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %v\n%s", err, out)
	}
	fh, err := os.Open(marks)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	shas := map[int]string{}
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		mark, sha, _ := strings.Cut(strings.TrimPrefix(sc.Text(), ":"), " ")
		n, _ := strconv.Atoi(mark)
		shas[n] = sha
	}
	if len(shas) != 702 {
		t.Fatalf("fast-import marked %d commits, want 702", len(shas))
	}
	return "file://" + dir, shas
}

func closedWhen(head string, at int64) string {
	return closedAt(head, time.Unix(at, 0).UTC())
}

// commitTime is a commit's committer time in the fixture, read back from origin.
func commitTime(t *testing.T, url, sha string) int64 {
	t.Helper()
	ts, err := strconv.ParseInt(gitIn(t, strings.TrimPrefix(url, "file://"), "show", "-s", "--format=%ct", sha), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// TestSetCheckShallowCloneDeepensForAnOldWindow is #3404's DONE-WHEN: a
// 700-commit repo and a window older than the --depth 50 cutoff. The evaluator's
// clone starts at depth 50, stays there for a window inside it, deepens for the
// window past it, answers the lander's commit, and is still shallow afterwards. A
// plain --depth 50 clone cannot answer: the lander's commit is not in it.
func TestSetCheckShallowCloneDeepensForAnOldWindow(t *testing.T) {
	url, c := deepOrigin(t)
	f := &forge{answers: map[string]string{
		"api repos/o/r/pulls/7": closedWhen(c[1007], commitTime(t, url, c[450])+300),
		"api repos/o/r/pulls/8": closedWhen(c[1008], commitTime(t, url, c[695])+300),
	}}
	ctx := context.Background()
	cache := t.TempDir()
	e := New(f.run, "o/r", "dev", cache)
	e.GitURL = func(string) string { return url }
	clone := filepath.Join(cache, "o", "r.git")

	// A window inside the first fetch: answered at depth 50, nothing deepened.
	if v := e.Landed(ctx, "pr:o/r#8"); !v.Holds || v.Why != "merge-changes-nothing-at:"+c[695][:12] {
		t.Fatalf("pr #8 (window inside depth 50) = %s why=%s", v.Word(), v.Why)
	}
	if d := e.depth["o/r"]; d != DefaultDepth {
		t.Errorf("a window inside the clone deepened it: depth %d, want %d", d, DefaultDepth)
	}
	if got := gitIn(t, clone, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("the first fetch is not shallow: is-shallow=%s", got)
	}

	// The window past the cutoff: deepened until c400 (the window start and the
	// merge base) is inside, then the lander's commit c450 answers.
	if v := e.Landed(ctx, "pr:o/r#7"); !v.Holds || v.Why != "merge-changes-nothing-at:"+c[450][:12] {
		t.Fatalf("pr #7 (window past depth 50) = %s why=%s", v.Word(), v.Why)
	}
	d := e.depth["o/r"]
	if d <= DefaultDepth || d >= 700 {
		t.Errorf("depth after the old window = %d, want past %d and short of 700", d, DefaultDepth)
	}
	if got := gitIn(t, clone, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Errorf("the clone was unshallowed: is-shallow=%s", got)
	}
	if n, _ := strconv.Atoi(gitIn(t, clone, "rev-list", "--count", "--first-parent", "refs/heads/dev")); n >= 700 || n <= DefaultDepth {
		t.Errorf("dev in the clone holds %d commits, want past %d and short of 700", n, DefaultDepth)
	}

	// With deepening forbidden the same question is unknown, never no.
	e3 := New(f.run, "o/r", "dev", t.TempDir())
	e3.GitURL, e3.MaxDeepen = e.GitURL, -1
	if v := e3.Landed(ctx, "pr:o/r#7"); v.Known || v.Why != "error:window-past-shallow-cutoff" {
		t.Errorf("pr #7 with no deepen = %s why=%s, want unknown why=error:window-past-shallow-cutoff", v.Word(), v.Why)
	}

	// The control: a plain --depth 50 clone lacks the lander's commit, and the
	// landing window it walks is empty, so it could only answer from its cutoff.
	plain := filepath.Join(t.TempDir(), "plain.git")
	gitIn(t, filepath.Dir(plain), "clone", "-q", "--bare", "--depth", "50", "--single-branch", "-b", "dev",
		"--filter=blob:none", url, plain)
	gitIn(t, plain, "fetch", "-q", "--depth", "50", "--filter=blob:none", "origin", "+refs/pull/7/head:refs/nova/pr/7")
	// Asked through the history, not by sha: a sha a partial clone lacks is
	// fetched on demand by cat-file, which would hide the cutoff.
	if strings.Contains(gitIn(t, plain, "rev-list", "--first-parent", "refs/heads/dev"), c[450]) {
		t.Errorf("the plain --depth 50 clone holds the lander's commit %s; the fixture no longer tests the cutoff", c[450][:12])
	}
	window := gitIn(t, plain, "rev-list", "--first-parent",
		"--since="+time.Unix(commitTime(t, url, c[1007]), 0).UTC().Format(time.RFC3339),
		"--until="+time.Unix(commitTime(t, url, c[450])+300, 0).Add(LandingSlack).UTC().Format(time.RFC3339),
		"refs/heads/dev")
	if window != "" {
		t.Errorf("a plain --depth 50 clone walked a landing window: %q", window)
	}
}
