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
// runner whatever the text, exit 1 before any write, its transcript hash
// unchanged (the runner adopts only a session with no preset, and a session
// never returns to none). Measured 2026-10-06: the same refusal also comes
// with exit 0. The refusal, and MISSING_CREDENTIAL, are read from the output
// whatever the exit (DSHRefusal): a SessionRefused, so the message stays
// pending instead of being given up after three refusals, and the daemon
// marks the session broken at once until a turn succeeds. On the survey machine,
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

	turns SessionTurns // the session's last turns, its liveness (alive.go)
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
	d.turns.begin()
	defer d.turns.end()
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
		if r, ok := DSHRefusal(id, d.Dir, out); ok {
			d.turns.saw(id, 0, r)
			return 0, r // the output is not kept: a credential's name and its lookup stay off the record
		}
	}
	if d.Out != nil && out != "" {
		fmt.Fprintln(d.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	exit, err = refused(id, out, exit, err)
	d.turns.saw(id, exit, err)
	return exit, err
}

// Route is what status and check say: push. Every delivery goes into the
// friend's session as a headless turn (dsh headless --session-id), the session
// check included; none waits on the open desktop app, which no route reaches
// (docs/SPEC-FRIEND.md, the dsh row). line is that turn's command.
func (d *DSH) Route(ctx context.Context) (route, line string, err error) {
	session := d.Session
	if session == "" {
		session = "<her newest session>"
	}
	return "push", "dsh " + strings.Join(DSHArgs(session), " "), nil
}

// DSHNoPresetRemedy is what a friend whose dsh session runs under an agent
// preset does: nova-friend install and run refuse that session with it
// (PushProof), since no delivery into it can succeed.
func DSHNoPresetRemedy(dir string) string {
	return "start a session in " + dir + " with no agent preset and name it with --session <id>"
}

// dshPresetRefusal is the one-shot runner's refusal of a session under an
// agent preset, the preset its group; dshMissingCredential is the runner's
// answer when the session's provider has no key.
var (
	dshPresetRefusal     = regexp.MustCompile(`runs under agent preset "([^"]*)", which the one-shot runner does not compose`)
	dshMissingCredential = regexp.MustCompile(`\bMISSING_CREDENTIAL\b`)
)

// DSHRefusal reads one headless turn's output, whatever its exit code, for
// an answer that the session cannot take a turn at all: the agent preset
// refusal, or MISSING_CREDENTIAL. Either is a failed delivery (SessionRefused),
// never a delivered turn: the finding of 2026-10-06, Zhi's session under preset
// "minimal" printed the refusal and exited 0, and four hours of messages
// counted as delivered while her row read up. The reason is fixed text; the
// output itself, which may name a credential, is never carried.
func DSHRefusal(session, dir, out string) (SessionRefused, bool) {
	if m := dshPresetRefusal.FindStringSubmatch(out); m != nil {
		return SessionRefused{Session: session, Reason: fmt.Sprintf("dsh session %s: agent preset %s", session, m[1]),
			Detail: fmt.Sprintf("session %s runs under agent preset %q, which dsh's headless runner does not compose (it adopts only a session with no agent preset); start a session in %s without an agent preset and name it with --session, or read the bus with nova-bus recv", session, m[1], dir),
			Remedy: DSHNoPresetRemedy(dir)}, true
	}
	if dshMissingCredential.MatchString(out) {
		return SessionRefused{Session: session, Reason: "dsh: missing credential",
			Detail: fmt.Sprintf("dsh has no key for session %s's provider; set the provider's key in dsh, then a turn succeeds", session)}, true
	}
	return SessionRefused{}, false
}

func dshHome() string {
	if h := os.Getenv("DSH_HOME"); h != "" {
		return filepath.Join(h, "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dsh", "sessions")
}
