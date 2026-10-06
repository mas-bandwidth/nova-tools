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
// sits in shares the store. Measured 2026-10-04 and 2026-10-05 (docs/SPEC-FRIEND.md,
// the dsh row): a session under an agent preset is refused by the one-shot
// runner whatever the text, before any write, its transcript hash unchanged
// (the runner adopts only a session with no preset, and a session never
// returns to none). From 2026-10-04 that refusal was printed and the process
// exited 0, so a check that required a nonzero exit counted the turn
// delivered. The same for a missing provider key (MISSING_CREDENTIAL in the
// output). Either text, whatever the exit code, is DSHDeaf: the message
// stays pending, and the friend's row reads down until a turn succeeds
// (docs/SPEC-FRIEND.md, a dsh turn the session cannot take). On the survey machine,
// deliver.log records 1339+ deferred deliveries against Zhi's real open session,
// and the desktop app exposes no local listener or IPC socket. No route into
// the open desktop session exists, so Route answers defer; the session reads the
// bus itself with nova-bus wait or nova-bus recv.
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
	if err == nil {
		if deaf, ok := dshDeafFrom(id, d.Dir, out); ok {
			return 0, deaf // not written out: the daemon says the reason once, not every recheck
		}
	}
	if d.Out != nil && out != "" {
		fmt.Fprintln(d.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	return refused(id, out, exit, err)
}

// Route is what status says: always defer. No live push into the open
// desktop session was found (docs/SPEC-FRIEND.md, the dsh row), and a
// headless turn is a separate process, not the friend's open chat. line is
// what the session runs itself: a blocking read of the bus.
func (d *DSH) Route(ctx context.Context) (route, line string, err error) {
	return "defer", "nova-bus wait --as <friend>", nil
}

// DSHNoPresetRemedy is what a friend whose dsh session runs under an agent
// preset does: nova-friend install and run refuse that session with it
// (PushProof), since no delivery into it can succeed.
func DSHNoPresetRemedy(dir string) string {
	return "start a session in " + dir + " with no agent preset and name it with --session <id>"
}

// dshPresetRefusal is the one-shot runner's refusal of a session under an
// agent preset, the preset its group.
var dshPresetRefusal = regexp.MustCompile(`runs under agent preset "([^"]*)", which the one-shot runner does not compose`)

// DSHDeaf is a dsh headless turn the session cannot take at all: the output
// carries the agent-preset refusal or MISSING_CREDENTIAL, whatever the exit
// code. It is a Deferred, so the message stays pending and is never given
// up, and Down is the presence reason the daemon marks the row down with on
// the first such turn (docs/SPEC-FRIEND.md, a dsh turn the session cannot
// take). No credential value is in it.
type DSHDeaf struct {
	Deferred
	Session string
	Down    string
}

func (d DSHDeaf) Unwrap() error { return d.Deferred }

// dshDeafFrom reads a headless turn's output. ok is false when the turn
// does not say the session cannot take one. dir is the friend's directory,
// used in the preset remedy; empty leaves that remedy unnamed.
func dshDeafFrom(session, dir, out string) (DSHDeaf, bool) {
	if m := dshPresetRefusal.FindStringSubmatch(out); m != nil {
		reason := fmt.Sprintf("session %s runs under agent preset %q, which dsh's headless runner does not compose (it adopts only a session with no agent preset); the message stays pending: start a session in %s without an agent preset and name it with --session, or read the bus with nova-bus recv", session, m[1], dir)
		return DSHDeaf{
			Deferred: Deferred{Reason: reason, Remedy: DSHNoPresetRemedy(dir)},
			Session:  session,
			Down:     fmt.Sprintf("dsh session %s: agent preset %s", session, m[1]),
		}, true
	}
	if strings.Contains(out, "MISSING_CREDENTIAL") {
		return DSHDeaf{
			Deferred: Deferred{
				Reason: "dsh: missing credential; the message stays pending, and no credential value is printed",
				Remedy: "set the provider key the headless profile reads; nothing here prints the key",
			},
			Session: session,
			Down:    "dsh: missing credential",
		}, true
	}
	return DSHDeaf{}, false
}

func dshHome() string {
	if h := os.Getenv("DSH_HOME"); h != "" {
		return filepath.Join(h, "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dsh", "sessions")
}
