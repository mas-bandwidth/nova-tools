package wake

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Ten minutes silent is offline (rule 2).
//
// --line is NOT a fourth source: it is a view over the bus checkout's commits,
// polled at --interval like the bus itself. A line's LAST SIGN is the stamp of
// the newest commit on the checkout's branch whose author is that name -- a
// note, a receipt, or any other commit the line made -- and a line whose last
// sign is older than --offline-after is reported once.
//
// The hurt: Johnny ran out of credits at 00:35Z and the board said he held his
// items for an hour. The prototype watched notes, checks and files, and a line
// that stopped writing all three looked exactly like a line that was busy.

// DefaultOfflineAfter is the one duration here with a default, because it is
// the family's rule and not a fact about one window: every line must agree
// about it, and it is the same number as nova-board --stale. An interval, by
// contrast, is a fact about one window's round trip, which is why --interval
// and --entry-interval have no defaults at all.
const DefaultOfflineAfter = 10 * time.Minute

// Lines is the --line view.
type Lines struct {
	Bus     string
	Names   []string
	After   time.Duration
	Timeout time.Duration
	// Start is when this watch began, and it is what a line with NO commit on
	// the branch is judged against: it is OFFLINE at the first poll after the
	// watch has run for --offline-after.
	Start time.Time
}

// Poll reads the checkout's log once, read-only, under the timeout, and nothing
// else. The sign is a COMMIT STAMP and never a date in a commit's subject: a
// time that reaches this tool inside text is data (rule 9).
func (l *Lines) Poll(ctx context.Context, now time.Time) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()
	// No commit cap. Rule 2 names none, and a cap makes a line whose newest
	// commit is older than it read as a line with NO sign at all -- OFFLINE,
	// or BACK, from missing history rather than from the world.
	cmd := exec.CommandContext(ctx, "git", "-C", l.Bus, "log",
		"--format=%H%x1f%an%x1f%cI")
	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("git log on the bus checkout timed out after %s", Dur(l.Timeout))
		}
		var ee *exec.ExitError
		detail := err.Error()
		if ok := asExit(err, &ee); ok && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return Result{}, fmt.Errorf("git log on the bus checkout: %s", oneLineOf(detail))
	}
	newest := map[string][2]string{} // name -> {sha, stamp}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) != 3 {
			continue
		}
		if _, seen := newest[f[1]]; seen {
			continue // the log is newest first, so the first sighting is the last sign
		}
		newest[f[1]] = [2]string{f[0], f[2]}
	}
	res := Result{}
	for _, name := range l.Names {
		sign, ok := newest[name]
		sha, stamp := "", ""
		last := l.Start
		if ok {
			sha, stamp = sign[0], sign[1]
			if t, err := time.Parse(time.RFC3339, stamp); err == nil {
				last = t
			}
			stamp = Stamp(last)
		}
		state := "BACK"
		if now.Sub(last) >= l.After {
			state = "OFFLINE"
		}
		res.Items = append(res.Items, Item{
			Kind:  KindLine,
			Key:   "line:" + name,
			Value: Compose(sha, state, stamp),
		})
	}
	return res, nil
}
