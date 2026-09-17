package pulse

// The beat verb's red tests. The token item was a coordinator window that never restarted
// at a beat and grew to 650k tokens a turn. `beat` appends one compact section to the cairn
// file -- the queue counts, the reds, the STOP, the HUMAN count and the last merged PR --
// writes the resume rule, commits the cairn when the cairn is in a git repo, and prints the
// line a fresh window restarts from.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// beatQueue writes a queue with two pending, one running, one done and one failed card,
// two reds, a STOP, three human lines and two merges: the counts are the section's contract.
func beatQueue(t *testing.T) string {
	t.Helper()
	queue := t.TempDir()
	for _, f := range []string{
		"pending/card-1.md", "pending/card-2.md", "launched/card-3.md",
		"done/card-4.md", "failed/card-5.md",
	} {
		write(t, filepath.Join(queue, f), "RESULT x\n")
	}
	write(t, filepath.Join(queue, "REDS"), "r1\nr2\n")
	write(t, filepath.Join(queue, "STOP"), "MAIN-RED aaaa1111: revert first\n")
	write(t, filepath.Join(queue, "HUMAN"), "h1\nh2\nh3\n")
	write(t, filepath.Join(queue, "MERGED"),
		"2026-09-16T10:00:00Z\tMERGED\tmas-bandwidth/nova-tools#800\n"+
			"2026-09-17T09:00:00Z\tMERGED\tmas-bandwidth/nova-tools#812\n")
	return queue
}

// gitRepo makes a temp git repo with an identity, so a beat can commit without the test
// reaching the network.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "beat@example.test"},
		{"config", "user.name", "beat test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func readBody(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestBeatAppendsTheQueueSection(t *testing.T) {
	queue := beatQueue(t)
	repo := gitRepo(t)
	cairn := filepath.Join(repo, "CAIRN.md")
	now := time.Date(2026, 9, 17, 4, 45, 0, 0, time.UTC)

	var out, errb bytes.Buffer
	code := Beat(BeatInput{Queue: queue, Cairn: cairn, Title: "beat one", Now: func() time.Time { return now }, Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("beat exit = %d, stderr=%s", code, errb.String())
	}
	body := readBody(t, cairn)
	for _, want := range []string{
		"## 2026-09-17T04:45:00Z beat one",
		"pending=2 running=1 done=1 failed=1",
		"reds=2",
		"stop=yes",
		"human=3",
		"merged=mas-bandwidth/nova-tools#812",
		"Resume rule: " + DefaultResume,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("cairn lacks %q:\n%s", want, body)
		}
	}
	// The commit: the cairn is in a git repo, so the beat commits it and never pushes.
	if got := strings.TrimSpace(gitOut(t, repo, "log", "--format=%s", "-1")); !strings.Contains(got, "beat one") {
		t.Errorf("last commit = %q, want the beat's title", got)
	}
	if got := gitOut(t, repo, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(got, "CAIRN.md") {
		t.Errorf("the commit does not carry the cairn: %q", got)
	}
	if got := strings.TrimSpace(gitOut(t, repo, "status", "--porcelain")); got != "" {
		t.Errorf("the cairn is left uncommitted: %q", got)
	}
	// The output: one line with the cairn and its line count, then the restart line.
	wantLines := fmt.Sprintf("lines=%d", strings.Count(body, "\n"))
	if !strings.Contains(out.String(), "BEAT OK cairn="+cairn+" "+wantLines+"\n") {
		t.Errorf("stdout = %q, want BEAT OK cairn=<file> %s", out.String(), wantLines)
	}
	if !strings.Contains(out.String(), "RESTART: exit this window; the next window boots from "+cairn+"\n") {
		t.Errorf("stdout = %q, want the restart line naming the cairn", out.String())
	}
}

func TestBeatAppendsASecondSection(t *testing.T) {
	queue := beatQueue(t)
	repo := gitRepo(t)
	cairn := filepath.Join(repo, "CAIRN.md")
	base := time.Date(2026, 9, 17, 4, 45, 0, 0, time.UTC)

	var out bytes.Buffer
	for i := 0; i < 2; i++ {
		when := base.Add(time.Duration(i) * time.Minute)
		if code := Beat(BeatInput{Queue: queue, Cairn: cairn, Title: fmt.Sprintf("beat %d", i+1), Now: func() time.Time { return when }, Stdout: &out, Stderr: &out}); code != 0 {
			t.Fatalf("beat %d exit = %d: %s", i+1, code, out.String())
		}
	}
	body := readBody(t, cairn)
	if n := strings.Count(body, "## "); n != 2 {
		t.Errorf("cairn has %d sections, want 2:\n%s", n, body)
	}
	for _, want := range []string{"## 2026-09-17T04:45:00Z beat 1", "## 2026-09-17T04:46:00Z beat 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("cairn lacks %q:\n%s", want, body)
		}
	}
	if got := strings.Count(strings.TrimSpace(gitOut(t, repo, "log", "--oneline")), "\n") + 1; got != 2 {
		t.Errorf("commits = %d, want 2 (one per beat)", got)
	}
}

func TestBeatWithoutGitStillSaysOK(t *testing.T) {
	queue := beatQueue(t)
	// No git on PATH is the without-git case: the beat must still append and report.
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	dir := t.TempDir()
	cairn := filepath.Join(dir, "CAIRN.md")

	var out, errb bytes.Buffer
	code := Beat(BeatInput{Queue: queue, Cairn: cairn, Title: "no repo", Now: func() time.Time { return time.Unix(0, 0).UTC() }, Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("beat without git exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "BEAT OK cairn="+cairn) {
		t.Errorf("stdout = %q, want BEAT OK naming the cairn", out.String())
	}
	if !strings.Contains(out.String(), "RESTART: exit this window; the next window boots from "+cairn) {
		t.Errorf("stdout = %q, want the restart line", out.String())
	}
	body := readBody(t, cairn)
	if !strings.Contains(body, "## 1970-01-01T00:00:00Z no repo") {
		t.Errorf("cairn lacks the section header:\n%s", body)
	}
	if !strings.Contains(body, "stop=yes") || !strings.Contains(body, "merged=mas-bandwidth/nova-tools#812") {
		t.Errorf("cairn lacks the queue facts:\n%s", body)
	}
}
