package wake

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The bounds of the bounded read, asserted rather than trusted -- the "Asserted
// with it" half of the spec's test 19, which a transcript assertion cannot
// reach: what a poll SPENDS is not on the line, so it is measured here, at the
// primitive, against a real lane.

func laneRepo(t *testing.T) (dir, anchor string) {
	t.Helper()
	dir = t.TempDir()
	rungit(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "participants.json"),
		`{"participants":[{"name":"Rowan","lane":"from-rowan","git_name":"Rowan","git_email":"rowan@x"},`+
			`{"name":"peer","lane":"from-peer","git_name":"peer","git_email":"peer@x"}]}`)
	rungit(t, dir, "add", "-A")
	rungit(t, dir, "-c", "user.name=Rowan", "-c", "user.email=rowan@x", "commit", "-q", "-m", "roster")
	return dir, head(t, dir)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rungit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".none"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func head(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".none"))
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

// noteText is one lane note with a body of the size given, so "no note body is
// opened" can be measured rather than asserted.
func noteText(from, to, subject string, body int) string {
	return fmt.Sprintf("From: %s\nTo: %s\nDate: Sun Sep 13 10:00:00 UTC 2026\nId: %s-000000000001\nSubject: %s\n\n%s\n",
		from, to, strings.ToLower(from), subject, strings.Repeat("b", body))
}

// TestTheBoundedReadSpendsWhatItSaysItSpends is the spec's "Asserted with it"
// clause list, at the primitive: the items under --correlate-max, the bytes
// under --correlate-bytes, no note body opened, the wall clock under the whole
// read's bound, and a peak resident that is FLAT across polls rather than a
// function of how deep the lane is.
func TestTheBoundedReadSpendsWhatItSaysItSpends(t *testing.T) {
	dir, anchor := laneRepo(t)
	// 900 notes IN ONE COMMIT, which is the spec's own separately named
	// fixture: nine hundred notes added in one commit are one commit and three
	// polls of work, and a caller reading a commit count would expect one.
	for i := 1; i <= 900; i++ {
		to := "Somebody-Else"
		if i == 850 {
			to = "Rowan"
		}
		writeFile(t, filepath.Join(dir, "from-peer", fmt.Sprintf("n%04d.md", i)),
			noteText("peer", to, "note", 8<<10))
	}
	rungit(t, dir, "add", "-A")
	rungit(t, dir, "-c", "user.name=peer", "-c", "user.email=peer@x", "commit", "-q", "-m", "900 notes")

	const maxItems, maxBytes = 300, 1 << 20
	bookmark := "-"
	var peaks []int
	for poll := 1; poll <= 3; poll++ {
		r := &LaneRead{Dir: dir, Ref: "HEAD", Lane: "from-peer", Anchor: anchor,
			Caller: "Rowan", PingID: "rowan-00000000000a",
			MaxItems: maxItems, MaxBytes: maxBytes, Wall: GitWall, Whole: WholeReadCap}
		res := r.Run(context.Background(), bookmark)
		if res.Items > maxItems {
			t.Errorf("poll %d took %d items, over --correlate-max %d", poll, res.Items, maxItems)
		}
		if res.Bytes > maxBytes {
			t.Errorf("poll %d consumed %d bytes, over --correlate-bytes %d", poll, res.Bytes, maxBytes)
		}
		if res.Peak > HeaderCap {
			t.Errorf("poll %d held %d bytes of one item, over the %d-byte header cap", poll, res.Peak, HeaderCap)
		}
		if res.Peak > 512 {
			t.Errorf("poll %d decoded %d bytes of a note whose header is under 200; the read stops at the blank line and opens no body", poll, res.Peak)
		}
		if res.Elapsed > WholeReadCap {
			t.Errorf("poll %d ran %s, over the whole read's %s bound", poll, res.Elapsed, WholeReadCap)
		}
		peaks = append(peaks, res.Peak)
		if poll < 3 {
			if res.Answered {
				t.Fatalf("poll %d answered early", poll)
			}
			if res.Complete {
				t.Errorf("poll %d covered 300 of 900 items and says complete", poll)
			}
			// The notes here carry an 8 KiB body each, so it is the BYTE
			// budget that binds and not the item one; what this test asserts
			// is that BOTH are respected and that the header read stops at the
			// blank line however large the body is. The item arithmetic --
			// remaining=600 then 300 over the same 900 notes in one commit --
			// is asserted in the test below, where the bodies are small enough
			// for --correlate-max to be the bound that bites.
			if res.Remaining == "-" {
				t.Errorf("poll %d could not count what remains inside its allowance", poll)
			}
		}
		bookmark = res.Bookmark
	}
	for i := 1; i < len(peaks); i++ {
		if peaks[i] != peaks[0] {
			t.Errorf("the peak resident bytes moved between polls: %v; one item of buffer at a time is a constant", peaks)
		}
	}
}

// TestTheAnswerIsFoundOnThePollThatReachesIt walks the same lane to the answer.
func TestTheAnswerIsFoundOnThePollThatReachesIt(t *testing.T) {
	dir, anchor := laneRepo(t)
	for i := 1; i <= 900; i++ {
		to := "Somebody-Else"
		if i == 850 {
			to = "Rowan"
		}
		writeFile(t, filepath.Join(dir, "from-peer", fmt.Sprintf("n%04d.md", i)),
			noteText("peer", to, "note", 32))
	}
	rungit(t, dir, "add", "-A")
	rungit(t, dir, "-c", "user.name=peer", "-c", "user.email=peer@x", "commit", "-q", "-m", "900 notes")

	bookmark := "-"
	for poll := 1; poll <= 3; poll++ {
		r := &LaneRead{Dir: dir, Ref: "HEAD", Lane: "from-peer", Anchor: anchor,
			Caller: "Rowan", PingID: "rowan-00000000000a",
			MaxItems: 300, MaxBytes: 1 << 20, Wall: GitWall, Whole: WholeReadCap}
		res := r.Run(context.Background(), bookmark)
		if poll < 3 {
			want := fmt.Sprintf("%d", 900-poll*300)
			if res.Remaining != want {
				t.Errorf("poll %d remaining=%s, want %s -- in ITEMS, and the 900 are ONE commit", poll, res.Remaining, want)
			}
		}
		if poll == 3 {
			if !res.Answered {
				t.Fatalf("the 850th note is reached on the third poll; remaining=%s", res.Remaining)
			}
			if res.Complete {
				t.Errorf("fifty items stand behind the answer, so the read is not complete")
			}
			return
		}
		if res.Answered {
			t.Fatalf("poll %d answered early", poll)
		}
		bookmark = res.Bookmark
	}
}

// TestAWedgedProcessIsKilledByTheWholeReadBound proves the deadline reaches the
// process that decodes every item, without a fake: a whole-read bound of a few
// milliseconds must end the poll and leave nothing running.
func TestAWedgedProcessIsKilledByTheWholeReadBound(t *testing.T) {
	dir, anchor := laneRepo(t)
	for i := 1; i <= 20; i++ {
		writeFile(t, filepath.Join(dir, "from-peer", fmt.Sprintf("n%02d.md", i)),
			noteText("peer", "Somebody-Else", "note", 64))
	}
	rungit(t, dir, "add", "-A")
	rungit(t, dir, "-c", "user.name=peer", "-c", "user.email=peer@x", "commit", "-q", "-m", "notes")

	r := &LaneRead{Dir: dir, Ref: "HEAD", Lane: "from-peer", Anchor: anchor,
		Caller: "Rowan", PingID: "rowan-00000000000a",
		MaxItems: 300, MaxBytes: 1 << 20, Wall: time.Millisecond, Whole: 5 * time.Millisecond}
	started := time.Now()
	res := r.Run(context.Background(), "-")
	if d := time.Since(started); d > 5*time.Second {
		t.Errorf("the read ran %s past a 5ms whole-read bound", d)
	}
	if res.Complete {
		t.Errorf("a read the clock ended is never complete")
	}
}
