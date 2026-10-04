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
// one line, to the wake file the open session's monitor is tailing. It is
// accepted (exit 0) once the line is in the file and the tail was running
// under that session; the turn runs after Deliver returns, since nothing
// hands its end back.
type Grok struct {
	Dir  string    // the friend's directory: the session's cwd
	Wake string    // the wake file, when named; else the one the session's monitor tails
	Run  Exec      // runs ps
	Out  io.Writer // the daemon's record, when set
	Home string    // the grok home, ~/.grok when empty
}

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
	home, err := g.home()
	if err != nil {
		return 0, err
	}
	active, err := os.ReadFile(filepath.Join(home, "active_sessions.json"))
	if err != nil {
		return 0, fmt.Errorf("no grok session is open: %w", err)
	}
	listing, exit, err := g.Run(ctx, g.Dir, "ps", []string{"-axww", "-o", "pid=,ppid=,args="}, "")
	if err != nil {
		return 0, fmt.Errorf("ps: %w", err)
	}
	if exit != 0 {
		return 0, fmt.Errorf("ps exited %d", exit)
	}
	// the harness records the window's cwd as it was given or resolved; either is this directory
	dirs := []string{g.Dir}
	if real, err := filepath.EvalSymlinks(g.Dir); err == nil && real != g.Dir {
		dirs = append(dirs, real)
	}
	var wake string
	for _, dir := range dirs {
		if wake, err = WakeOf(string(active), listing, dir, g.Wake); !errors.Is(err, ErrNoSession) {
			break // found, or refused by the window that is open
		}
	}
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(wake, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 0, err
	}
	_, err = f.WriteString(WakeLine(text) + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if g.Out != nil {
		fmt.Fprintln(g.Out, "one monitor event appended to "+wake+"; the turn runs after this")
	}
	return 0, nil
}

// WakeOf is the wake file a delivery goes to: the file a `tail` tails under
// the grok session open in dir, from the harness's active_sessions.json
// (pid and cwd per open window) and the process listing
// (`ps -axww -o pid=,ppid=,args=`). A named wake must be that file. A
// session whose pid is not in the listing is a stale record, not a session.
// No window in dir is ErrNoSession.
func WakeOf(active, listing, dir, wake string) (string, error) {
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
		if filepath.Base(f[2]) == "tail" {
			for _, a := range f[3:] {
				if strings.HasSuffix(a, ".wake") {
					tails[pid] = a
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
			if wake != "" && file != wake {
				continue
			}
			for p, hops := pid, 0; p > 1 && hops < 64; p, hops = parent[p], hops+1 {
				if p == s.PID {
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
	return "", fmt.Errorf("the grok session in %s runs no monitor over %s; in that session: monitor `tail -n 0 -F %s`", dir, wake, wake)
}

// ErrNoSession is WakeOf's refusal when no grok window is open in the directory.
var ErrNoSession = errors.New("no grok session is open")

// WakeLine is text as the one line a monitor event is: the monitor makes an
// event per line, and a flood of lines is how the harness stops a monitor
// (its guide's "Volume Control"), so a newline in the text becomes " ⏎ ".
func WakeLine(text string) string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	return "nova-friend: " + strings.ReplaceAll(text, "\n", " ⏎ ")
}
