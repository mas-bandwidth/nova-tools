package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Grok delivers into the Grok Build TUI (xAI's `grok`), which has no deliver
// verb, no leader socket unless leader mode is on, and no way to send into
// an open window: `grok -p <text> --resume <id>` runs the turn in a second
// process over the same transcript, not in the window the friend is in.
// What the window has is the monitor tool: a background task whose every
// new output line "becomes a notification delivered to the conversation"
// and wakes the agent for a turn (the harness's own guide,
// ~/.grok/docs/user-guide/20-background-tasks.md). The friend's session
// runs one over a wake file, `tail -n 0 -F <file>.wake`, and a line
// appended to that file arrives in the session as a <monitor-event> user
// turn (measured 2026-10-04 in a friend's session: a user_message_chunk at a
// new promptIndex in its updates.jsonl). So Deliver appends the text, as
// one line, to the wake file the open session's monitor is tailing. A
// backlog is many Deliver calls. Lines written with no gap are the flood
// that stops the monitor, so each line after the first waits out what
// remains of wakePace. It is accepted (exit 0) once the line is in the file
// and the tail was running under that session; the turn runs after Deliver
// returns, since nothing hands its end back. While no monitor runs (no window, or a window with
// no tail) Deliver defers: nothing is written, the message stays pending,
// and the reason carries the one line the session runs. A wake path that
// is not absolute is a refusal and nothing is written.
type Grok struct {
	Dir  string    // the friend's directory: the session's cwd
	Wake string    // the wake file, when named; else the one the session's monitor tails
	Run  Exec      // runs ps
	Out  io.Writer // the daemon's record, when set
	Home string    // the grok home, ~/.grok when empty

	// now and wait are the clock seam. Nil is the wall clock. Tests set both.
	now  func() time.Time
	wait func(context.Context, time.Duration) error

	mu   sync.Mutex
	next time.Time // when the last reserved line is due; zero before the first
}

// wakePace is the gap between wake lines from one adapter. One Deliver is
// already one line (WakeLine). Twelve of those written together are the
// burst that stops the monitor (docs/SPEC-FRIEND.md, the Grok adapter: a
// flood of lines is how the harness stops a monitor). The first line is
// immediate. A later line waits out whatever remains of this gap.
const wakePace = time.Second

func (g *Grok) home() (string, error) {
	if g.Home != "" {
		return g.Home, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".grok"), nil
}

func (g *Grok) Deliver(ctx context.Context, text string) (int, error) {
	wake, deferred, err := g.classify(ctx)
	if err != nil {
		return 0, err
	}
	if deferred != nil {
		return 0, Deferred{Reason: deferred.Error()}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	gap, undo := g.reserveWake(g.clock())
	if gap > 0 {
		if err := g.waitGap(ctx, gap); err != nil {
			undo()
			return 0, err
		}
	}
	if err := ctx.Err(); err != nil {
		undo()
		return 0, err
	}
	f, err := os.OpenFile(wake, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		undo()
		return 0, err
	}
	_, err = f.WriteString(WakeLine(text) + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		undo()
		return 0, err
	}
	if g.Out != nil {
		fmt.Fprintln(g.Out, "one monitor event appended to "+wake+"; the turn runs after this")
	}
	return 0, nil
}

// clock is the adapter's clock. A nil seam is the wall clock.
func (g *Grok) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// waitGap waits for the pace gap. A nil seam waits on the wall clock and
// returns as soon as ctx ends.
func (g *Grok) waitGap(ctx context.Context, gap time.Duration) error {
	if g.wait != nil {
		return g.wait(ctx, gap)
	}
	timer := time.NewTimer(gap)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// reserveWake books the next wake line. The first line is due now. A line
// that arrives before a full wakePace has passed since the previous booking
// is due then, and the returned gap is how long that still is. undo gives
// the booking back when the line is not written. docs/SPEC-FRIEND.md, the
// Grok adapter.
func (g *Grok) reserveWake(now time.Time) (gap time.Duration, undo func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	prev := g.next
	slot := now
	if !prev.IsZero() && now.Before(prev.Add(wakePace)) {
		slot = prev.Add(wakePace)
		gap = slot.Sub(now)
	}
	g.next = slot
	undo = func() {
		g.mu.Lock()
		if g.next.Equal(slot) {
			g.next = prev
		}
		g.mu.Unlock()
	}
	return gap, undo
}

// WakeOf is the wake file a delivery goes to: the file a `tail` tails under
// the grok session open in dir, from the harness's active_sessions.json
// (pid and cwd per open window) and the process listing
// (`ps -axww -o pid=,ppid=,args=`). A named wake must be that file. A
// session whose pid is not in the listing is a stale record, not a session.
// No window in dir is ErrNoSession.
func WakeOf(active, listing, dir, wake string) (string, error) {
	if wake != "" && (!filepath.IsAbs(wake) || strings.ContainsAny(wake, " \t\r\n")) {
		return "", fmt.Errorf("the monitor's wake path must be absolute")
	}
	var sessions []struct {
		PID int    `json:"pid"`
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(active), &sessions); err != nil {
		return "", fmt.Errorf("active_sessions.json: not a JSON list: %v", err)
	}
	parent := map[int]int{}
	tails := map[int]string{}
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		parent[pid] = ppid
		if len(f) >= 7 && filepath.Base(f[2]) == "tail" && f[3] == "-n" && f[4] == "0" && f[5] == "-F" {
			for _, a := range f[6:] {
				if strings.HasSuffix(a, ".wake") {
					// ps does not preserve argv boundaries: keep the whole operand
					// so a path with spaces cannot become a different absolute file.
					tails[pid] = strings.Join(f[6:], " ")
				}
			}
		}
	}
	dir = filepath.Clean(dir)
	open := 0
	for _, s := range sessions {
		if _, alive := parent[s.PID]; !alive || filepath.Clean(s.Cwd) != dir {
			continue
		}
		open++
		for pid, file := range tails {
			for p, hops := pid, 0; p > 1 && hops < 64; p, hops = parent[p], hops+1 {
				if p == s.PID {
					if !filepath.IsAbs(file) || strings.ContainsAny(file, " \t\r\n") {
						return "", fmt.Errorf("the monitor's wake path must be absolute")
					}
					if wake != "" && file != wake {
						break
					}
					return file, nil
				}
			}
		}
	}
	if open == 0 {
		return "", fmt.Errorf("%w in %s; start grok there", ErrNoSession, dir)
	}
	if wake == "" {
		wake = "<file>.wake"
	}
	return "", noMonitorError{fmt.Sprintf("the grok session in %s runs no monitor over %s; in that session: %s", dir, wake, GrokMonitorLine(wake))}
}

// ErrNoSession is WakeOf's refusal when no grok window is open in the directory.
var ErrNoSession = errors.New("no grok session is open")

// noMonitorError is WakeOf's refusal when a window is open and no tail of the
// wake file runs under it. Deliver turns it into Deferred; the text stays the
// sentence callers already match.
type noMonitorError struct{ msg string }

func (e noMonitorError) Error() string { return e.msg }

// GrokMonitorLine is the one line the open session runs so a delivery is a
// turn in that window. An empty wake is the placeholder the session replaces
// with its own absolute path. It is a command run in the session, not a
// flag or a wrapper at app start.
func GrokMonitorLine(wake string) string {
	if wake == "" {
		wake = "<file>.wake"
	}
	return "monitor `tail -n 0 -F " + wake + "`"
}

// GrokInstallLine is the NOTE install prints for harness grok. Every other
// harness gets none. session is --session, the wake file, empty when the
// session chooses the path.
func GrokInstallLine(harness, session string) string {
	if harness != "grok" {
		return ""
	}
	return GrokMonitorLine(session)
}

// Route is what status says. push: a tail of an absolute .wake file runs
// under the open window's pid, so a delivery now is a turn in that window.
// defer: no such tail; line is the monitor line the session runs. A listing
// or session file that cannot be read is defer, never a silent push, and
// err says why.
func (g *Grok) Route(ctx context.Context) (route, line string, err error) {
	line = GrokMonitorLine(g.Wake)
	file, deferred, err := g.classify(ctx)
	if err != nil {
		return "defer", line, err
	}
	if deferred != nil {
		return "defer", monitorLineFrom(deferred, g.Wake), nil
	}
	return "push", GrokMonitorLine(file), nil
}

// classify is the open window's monitor. wake is the file a tail under that
// window is reading. deferred is set when no monitor runs (no window, or a
// window with no tail): nothing has failed and nothing is written. err is a
// real failure (the process listing, a session file that cannot be read, a
// wake path the listing cannot be trusted on).
func (g *Grok) classify(ctx context.Context) (string, error, error) {
	home, err := g.home()
	if err != nil {
		return "", nil, err
	}
	active, err := os.ReadFile(filepath.Join(home, "active_sessions.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no grok session is open: %s; in that session: %s", err.Error(), GrokMonitorLine(g.Wake)), nil
		}
		return "", nil, fmt.Errorf("no grok session is open: %w", err)
	}
	if g.Run == nil {
		return "", nil, errors.New("ps: no process listing")
	}
	listing, exit, err := g.Run(ctx, g.Dir, "ps", []string{"-axww", "-o", "pid=,ppid=,args="}, "")
	if err != nil {
		return "", nil, fmt.Errorf("ps: %w", err)
	}
	if exit != 0 {
		return "", nil, fmt.Errorf("ps exited %d", exit)
	}
	// the harness records the window's cwd as it was given or resolved; either is this directory
	dirs := []string{g.Dir}
	if real, e := filepath.EvalSymlinks(g.Dir); e == nil && real != g.Dir {
		dirs = append(dirs, real)
	}
	var file string
	var wakeErr error
	for _, dir := range dirs {
		file, wakeErr = WakeOf(string(active), listing, dir, g.Wake)
		if wakeErr == nil || !errors.Is(wakeErr, ErrNoSession) {
			break // found, or answered by the window that is open
		}
	}
	if wakeErr == nil {
		return file, nil, nil
	}
	var nm noMonitorError
	if errors.Is(wakeErr, ErrNoSession) || errors.As(wakeErr, &nm) {
		return "", monitorDeferred(wakeErr, g.Wake), nil
	}
	return "", nil, wakeErr
}

// monitorDeferred is a no-monitor answer that carries the line the session runs.
func monitorDeferred(err error, wake string) error {
	if strings.Contains(err.Error(), "monitor `tail -n 0 -F") {
		return err
	}
	return fmt.Errorf("%s; in that session: %s", err.Error(), GrokMonitorLine(wake))
}

// monitorLineFrom is the monitor line at the end of a no-monitor answer.
func monitorLineFrom(err error, wake string) string {
	const mark = "monitor `tail -n 0 -F"
	if err != nil {
		if i := strings.LastIndex(err.Error(), mark); i >= 0 {
			return err.Error()[i:]
		}
	}
	return GrokMonitorLine(wake)
}

// WakeLine is text as the one line a monitor event is: the monitor makes an
// event per line, and a flood of lines is how the harness stops a monitor
// (its guide's "Volume Control"), so a newline in the text becomes " ⏎ ".
func WakeLine(text string) string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	return "nova-friend: " + strings.ReplaceAll(text, "\n", " ⏎ ")
}
