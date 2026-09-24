package dealer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mirrorWithTwoCommits builds a throwaway repo standing in for a bench mirror
// and answers its directory and two commit shas (older, newer).
func mirrorWithTwoCommits(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "cut")
	old := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "tip")
	return dir, old, run("rev-parse", "HEAD")
}

func writeCard(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "card.txt")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readCard(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDealPinsAndStampsThenIsIdempotent(t *testing.T) {
	mirror, old, tip := mirrorWithTwoCommits(t)
	now = func() time.Time { return time.Date(2026, 9, 23, 17, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	card := writeCard(t, "CARD x\nbase-repo: nova-tools\nbase-sha: "+old+"\nDONE-WHEN: y\n")
	res, err := Deal(card, mirror, tip)
	if err != nil || !res.Pinned || res.Base != tip {
		t.Fatalf("first deal: %+v %v", res, err)
	}
	want := "CARD x\nbase-repo: nova-tools\nbase-sha: " + tip + "\npinned-at-deal: " + tip + " 2026-09-23T17:00:00Z\nDONE-WHEN: y\n"
	if got := readCard(t, card); got != want {
		t.Fatalf("card after pin:\n%s\nwant:\n%s", got, want)
	}
	res, err = Deal(card, mirror, tip)
	if err != nil || res.Pinned {
		t.Fatalf("second deal wrote: %+v %v", res, err)
	}
	if got := readCard(t, card); got != want {
		t.Fatalf("second deal changed the card:\n%s", got)
	}
}

func TestDealReplacesAnEarlierStamp(t *testing.T) {
	mirror, old, tip := mirrorWithTwoCommits(t)
	card := writeCard(t, "base-sha: "+old+"\npinned-at-deal: "+old+" 2026-09-20T00:00:00Z\nrest\n")
	if _, err := Deal(card, mirror, tip); err != nil {
		t.Fatal(err)
	}
	got := readCard(t, card)
	if strings.Count(got, "pinned-at-deal:") != 1 || !strings.Contains(got, "pinned-at-deal: "+tip+" ") {
		t.Fatalf("stamp not replaced:\n%s", got)
	}
}

func TestDealKeepsCutBaseWithPinCut(t *testing.T) {
	mirror, old, tip := mirrorWithTwoCommits(t)
	body := "base-sha: " + old + "\npin: cut\nrest\n"
	card := writeCard(t, body)
	res, err := Deal(card, mirror, tip)
	if err != nil || res.Pinned || !res.KeptCut || res.Base != old {
		t.Fatalf("pin: cut deal: %+v %v", res, err)
	}
	if got := readCard(t, card); got != body {
		t.Fatalf("pin: cut card changed:\n%s", got)
	}
}

func TestDealRefusesATipOrBaseNotOnTheMirror(t *testing.T) {
	mirror, old, _ := mirrorWithTwoCommits(t)
	absent := strings.Repeat("de", 20)
	card := writeCard(t, "base-sha: "+old+"\n")
	if _, err := Deal(card, mirror, absent); err == nil || !strings.Contains(err.Error(), "DEAL REFUSED") {
		t.Fatalf("absent tip dealt: %v", err)
	}
	card = writeCard(t, "base-sha: "+absent+"\n")
	if _, err := Deal(card, mirror, old); err == nil || !strings.Contains(err.Error(), "DEAL REFUSED") {
		t.Fatalf("absent base dealt: %v", err)
	}
}
