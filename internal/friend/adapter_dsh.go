package friend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DSHProgram is where the DeepSeek Harness desktop app ships its CLI on this
// platform; the app installs no link on PATH.
const DSHProgram = "/Applications/DeepSeek Harness.app/Contents/Resources/runtime/cli/bin/dsh"

// DSH delivers through `dsh headless --session-id <id> -` run in Dir, the
// text on stdin: the headless profile adopts the persisted session (the same
// session directory under ~/.dsh/sessions/<key>/<id>, its record grows by one
// turn; measured 2026-10-04, v0.2.0-rc.2) and exits when the turn ends. An
// unknown id, a session recorded in another directory, and a missing
// provider key each exit 1. Without a session named, the newest session of
// Dir from the store (DSH_HOME, else ~/.dsh). The desktop app the friend
// sits in shares the store; whether it refuses a session it holds open is
// not measured. A session that has selected an agent preset is refused by
// the one-shot runner whatever the text (measured 2026-10-04 on Zhi's
// session, preset "minimal"; the runner adopts only a session with no preset,
// and a session never returns to none): that delivery is Deferred, so the
// message stays pending instead of being given up after three refusals.
type DSH struct {
	Dir, Session string
	Run          Exec
	Program      string    // DSHProgram when empty
	Sessions     string    // the sessions root; DSH_HOME/sessions, else ~/.dsh/sessions, when empty
	Out          io.Writer // where the turn's output goes, when set: the daemon's record
}

// DSHArgs is the argument list of one delivery; the text travels on stdin
// ("-"), so it is never parsed as an argument.
func DSHArgs(session string) []string {
	return []string{"headless", "--session-id", session, "-"}
}

// DSHSessionKey is the store's directory for the sessions of dir: the path
// with every separator a dash, one more dash in front and two behind
// (measured 2026-10-04 on the store: /Volumes/nova/ai/zhi is
// --Volumes-nova-ai-zhi--).
func DSHSessionKey(dir string) string {
	return "-" + strings.ReplaceAll(dir, "/", "-") + "--"
}

// NewestDSHSession is the most recently modified session of dir under the
// sessions root: the directory names are the session ids.
func NewestDSHSession(sessions, dir string) (string, error) {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("no dsh session for %s; start one there, or name one with --session", dir)
	}
	entries, err := os.ReadDir(filepath.Join(sessions, DSHSessionKey(realDir)))
	if err != nil {
		return "", fmt.Errorf("no dsh session for %s; start one there, or name one with --session", dir)
	}
	var best string
	var bestAt time.Time
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() || !strings.HasPrefix(e.Name(), "session-") {
			continue
		}
		if best == "" || info.ModTime().After(bestAt) {
			best, bestAt = e.Name(), info.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no dsh session for %s; start one there, or name one with --session", dir)
	}
	return best, nil
}

func (d *DSH) Deliver(ctx context.Context, text string) (int, error) {
	id := d.Session
	if id == "" {
		root := d.Sessions
		if root == "" {
			root = dshHome()
		}
		var err error
		if id, err = NewestDSHSession(root, d.Dir); err != nil {
			return 0, err
		}
	}
	program := d.Program
	if program == "" {
		program = DSHProgram
	}
	out, exit, err := d.Run(ctx, d.Dir, program, DSHArgs(id), text)
	if m := dshPresetRefusal.FindStringSubmatch(out); exit != 0 && err == nil && m != nil {
		return 0, Deferred{Reason: fmt.Sprintf("session %s runs under agent preset %q, which dsh's headless runner does not compose (it adopts only a session with no agent preset); the message stays pending: start a session in %s without an agent preset and name it with --session, or read the bus with nova-bus recv", id, m[1], d.Dir)}
	}
	if d.Out != nil && out != "" {
		fmt.Fprintln(d.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	return refused(id, out, exit, err)
}

// dshPresetRefusal is the one-shot runner's refusal of a session under an
// agent preset, the preset its group.
var dshPresetRefusal = regexp.MustCompile(`runs under agent preset "([^"]*)", which the one-shot runner does not compose`)

func dshHome() string {
	if h := os.Getenv("DSH_HOME"); h != "" {
		return filepath.Join(h, "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dsh", "sessions")
}
