package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The late tick, pushed (the owner, 2026-10-10: no silent stops; the night of 2026-10-09 the
// tick ran 20 to 90 s late for hours and only the dashboard's machine line said so). A tick
// that runs late cannot tell of itself, and one that does not run at all writes nothing, so
// the push loop tells it: at every look it reads the machine's line (where's "running (tick
// late <n>s)") and, while the tick runs TickLatePush late or more, pushes one line to the
// seat, again every sprint.PassEvery while it stays late, with the worst lateness seen.

// TickLatePush is how late the tick must run before the push loop tells the seat.
const TickLatePush = 30 * time.Second

// tickLateRE reads the lateness off the machine's line: "machine: running (tick late 16s)".
var tickLateRE = regexp.MustCompile(`\btick late (\d+)s\b`)

// lateWatch is one push loop's watch of the tick's lateness: the episode's start, the last
// push (kept across episodes), the start of the run of looks that found the tick on time, the
// worst lateness since the last push, and the looks that found it late since then.
type lateWatch struct {
	since, last, onTime time.Time
	worst               time.Duration
	looks               int
	// loaded says last was read from the inbox's late pushes (lastTickLate), once a loop
	loaded bool
}

// step reads one look's machine line at now and says the text to push, "" when none is due.
// The machine line measures lateness from the last heartbeat, so a look right after a tick
// reads on time even while every gap between ticks is late (cold reader B, PR 5548: the
// episode ended at every tick and the seat was pushed sixty times an hour). So an episode ends
// only after sprint.PassEvery of looks that all found the tick on time, and the last push is
// kept across episodes: the seat is pushed at most once every sprint.PassEvery, whatever the
// pattern of the ticks.
func (w *lateWatch) step(machine string, now time.Time) string {
	m := tickLateRE.FindStringSubmatch(machine)
	late := time.Duration(0)
	if m != nil && lineRunning(machine) {
		n, _ := strconv.Atoi(m[1]) // ignored: the expression admits digits alone
		late = time.Duration(n) * time.Second
	}
	if late < TickLatePush {
		if w.since.IsZero() {
			return ""
		}
		if w.onTime.IsZero() {
			w.onTime = now
		}
		if now.Sub(w.onTime) >= sprint.PassEvery {
			// on time a whole window: the episode ends; the last push stays
			w.since, w.onTime, w.worst, w.looks = time.Time{}, time.Time{}, 0, 0
		}
		return ""
	}
	w.onTime = time.Time{}
	if w.since.IsZero() {
		w.since = now.Add(-late)
	}
	w.looks++
	w.worst = max(w.worst, late)
	if !w.last.IsZero() && now.Sub(w.last) < sprint.PassEvery {
		return ""
	}
	text := fmt.Sprintf("MACHINE TICK LATE: the machine's tick runs %s late (the worst %s, %d looks late, since about %s); the deal, the coordinator's pass and every push wait on the tick; check the server: nova-sprint where (its machine line), nova-sprint stats, and the server's log and load\n",
		late, w.worst, w.looks, w.since.UTC().Format(time.RFC3339))
	w.last, w.worst, w.looks = now, 0, 0
	return text
}

// pushLate is the push loop's look at the tick's lateness: a text due is written to the
// holder's inbox (or the fixed directory) as TICKLATE-<time>.md and, following the seat,
// delivered into the holder's session as the judgments are (pushJudgments).
func (a *app) pushLate(ctx context.Context, src inboxSource, p *pushTarget, w *lateWatch, look inboxLook, asJSON bool, stdout, stderr io.Writer) {
	now := a.now()
	dir := p.fixed
	if dir == "" {
		dir, _, _ = a.seatInbox(look.holder)
	}
	if !w.loaded && dir != "" {
		// a push loop that restarts reads its last late push off the inbox it wrote it to
		// (cold reader A, PR 5548): a restart never pushes again inside sprint.PassEvery
		w.last, w.loaded = lastTickLate(dir, w.last), true
	}
	text := w.step(look.machine, now)
	if text == "" {
		return
	}
	if dir != "" {
		if _, err := p.keys(dir); err == nil {
			path := filepath.Join(dir, tickLatePrefix+now.UTC().Format(tickLateStamp)+".md")
			if _, err := writeOnce(path, text+"clock: "+now.UTC().Format(time.RFC3339)+"\n"); err != nil {
				fmt.Fprintf(stderr, "%s inbox --push: %s\n", prog, oneline.Escape(err.Error()))
			} else if !asJSON {
				fmt.Fprintf(stdout, "INBOX OK pushed=tick-late file=%s\n", oneline.Field(path))
			}
		}
	}
	if p.fixed == "" {
		a.pushJudgments(ctx, src, look.holder, []string{text}, asJSON, stdout)
	}
}

// The late push's file in the seat's inbox: TICKLATE-<UTC time>.md, the time its name.
const (
	tickLatePrefix = "TICKLATE-"
	tickLateStamp  = "20060102T150405Z"
)

// lastTickLate is the newest late push the directory holds, by the time its file's name
// carries, or last when it holds none newer (or cannot be read: the watch starts as before).
func lastTickLate(dir string, last time.Time) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return last
	}
	for _, e := range entries {
		name, ok := strings.CutPrefix(e.Name(), tickLatePrefix)
		if !ok {
			continue
		}
		name, ok = strings.CutSuffix(name, ".md")
		if !ok {
			continue
		}
		if at, err := time.Parse(tickLateStamp, name); err == nil && at.After(last) {
			last = at
		}
	}
	return last
}
