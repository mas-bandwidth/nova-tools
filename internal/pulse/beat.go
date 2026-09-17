package pulse

// beat is SPEC-PULSE's "The beat" verb: the window's restart point, one section each way.
// The token item was the coordinator window that never restarted at a beat and grew to 650k
// tokens a turn. `beat` appends one compact section to the cairn file -- the queue's
// pending/running/done/failed counts, the REDS line count, whether STOP stands, the HUMAN
// line count and the last merged PR -- then the resume rule, commits the cairn file when the
// cairn is in a git repo (never a push), and prints the one line a fresh window restarts
// from. It makes no model call: every number comes from a file under --queue.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultResume is the rule a beat writes when --resume names none: the first thing the
// next window does with the section it just read.
const DefaultResume = "read this section, run nova-pulse status, act on REDS/STOP/HUMAN first"

// BeatInput is everything the beat verb needs, held apart from flag parsing so a test can
// drive it against a temp queue and a temp git repo.
type BeatInput struct {
	Queue  string // the queue directory: pending, launched, done, failed and the state files
	Cairn  string // the cairn file this beat is appended to
	Title  string // the one-line title of this beat
	Resume string // the resume rule; empty takes DefaultResume
	Now    func() time.Time
	Stdout io.Writer
	Stderr io.Writer
}

// Beat appends one section to the cairn file, commits it when the cairn lives in a git repo
// (git add and one commit, never a push), and prints the two lines the next window reads:
// BEAT OK with the cairn's line count, then the restart command.
func Beat(in BeatInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.Queue, "queue", "the queue directory holding pending, launched, done, failed and the state files"},
		{in.Cairn, "cairn", "the cairn file this beat is appended to"},
		{in.Title, "title", "the one-line title of this beat"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "BEAT", fmt.Errorf("missing --%s; refusing to guess (%s)", r.name, r.wants))
		}
	}
	resume := strings.TrimSpace(in.Resume)
	if resume == "" {
		resume = DefaultResume
	}
	if err := appendBeat(in.Cairn, beatSection(in.Queue, in.Title, resume, in.Now())); err != nil {
		return refusal(in.Stderr, "BEAT", err)
	}
	commitBeat(in.Cairn, in.Title)
	fmt.Fprintf(in.Stdout, "BEAT OK cairn=%s lines=%d\n", oneline.Field(in.Cairn), countFileLines(in.Cairn))
	fmt.Fprintf(in.Stdout, "RESTART: exit this window; the next window boots from %s\n", oneline.Escape(in.Cairn))
	return 0
}

// beatSection is the markdown a beat appends: the heading, one line per queue fact, and the
// resume rule. The four counts are status's own queue counts; running is its launched count.
func beatSection(queue, title, resume string, now time.Time) []string {
	q := readQueue(queue)
	stop := "no"
	if _, err := os.Stat(filepath.Join(queue, "STOP")); err == nil {
		stop = "yes"
	}
	merged := lastMerged(filepath.Join(queue, "MERGED"))
	if merged == "" {
		merged = "-"
	}
	return []string{
		fmt.Sprintf("## %s %s", now.UTC().Format(time.RFC3339), oneline.Escape(strings.TrimSpace(title))),
		fmt.Sprintf("pending=%d running=%d done=%d failed=%d", q.pending, q.launched, q.done, q.failed),
		fmt.Sprintf("reds=%d", len(readLines(filepath.Join(queue, "REDS")))),
		fmt.Sprintf("stop=%s", stop),
		fmt.Sprintf("human=%d", len(readLines(filepath.Join(queue, "HUMAN")))),
		fmt.Sprintf("merged=%s", oneline.Field(merged)),
		"Resume rule: " + oneline.Escape(resume),
	}
}

// lastMerged is the last line of <queue>/MERGED reduced to its PR: the sweep writes
// `<stamp>\tMERGED\t<owner/repo>#<n>`, and a line of another shape is taken whole.
func lastMerged(path string) string {
	lines := readLines(path)
	if len(lines) == 0 {
		return ""
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if f := strings.Split(last, "\t"); len(f) >= 3 {
		return strings.TrimSpace(f[2])
	}
	return last
}

// appendBeat lands the section at the end of the cairn, separated from what is already there
// by one blank line, and creates the file and its directory when they are missing.
func appendBeat(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(string(raw), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// commitBeat commits the cairn file when it is in a git repo. It is deliberately best-effort:
// a cairn outside a repo, or a repo that cannot commit, still leaves a durable section and
// the BEAT OK line. It never pushes.
func commitBeat(path, title string) {
	dir := filepath.Dir(path)
	top, err := gitRun(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	root := strings.TrimSpace(top)
	if root == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	if _, err := gitRun(root, "add", "--", abs); err != nil {
		return
	}
	_, _ = gitRun(root, "commit", "-q", "-m", "beat: "+strings.TrimSpace(title))
}

// gitRun runs one git child in dir and returns its combined output.
func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// countFileLines is the cairn's length after the append: every section line ends in a
// newline, so counting them is the file's line count.
func countFileLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}
