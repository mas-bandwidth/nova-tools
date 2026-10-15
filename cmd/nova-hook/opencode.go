package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// hookHarnessOpenCode is the opencode row (docs/SPEC-OPENCODE-HOOK.md).
const hookHarnessOpenCode = "opencode"

// openCodeSessionEnv is what opencode's own shell tool sets in every command
// it runs for a session: the one-line setup, run by the friend from inside
// the session, names that session.
const openCodeSessionEnv = "OPENCODE_SESSION_ID"

// opencodeRoute is --harness opencode resolved: the CLI that reaches the
// running server, the session, the directory a new session is followed in
// (empty: pinned), and the private notice directory.
type opencodeRoute struct {
	bin, session, dir, notices string
}

// opencodeConfig resolves --harness opencode before any hook record is
// written: the CLI is found and made
// absolute (a supervised service's PATH is not the shell's), and the session
// is --opencode-session, else the session whose shell runs this command, else
// the newest root session in --opencode-dir.
func (w world) opencodeConfig(c *tool.Call, native *codexRoute) (*opencodeRoute, error) {
	if native != nil {
		return nil, errors.New("one harness at a time: --harness opencode or the Codex desktop route, not both")
	}
	if c.Bool("once") {
		return nil, errors.New("opencode delivery wants a running hook client; omit --once")
	}
	if w.opencodeAPI == nil || w.lookPath == nil {
		return nil, errors.New("opencode delivery wants a configured opencode CLI")
	}
	bin := strings.TrimSpace(c.Str("opencode-bin"))
	if bin == "" {
		bin = "opencode"
	}
	found, err := w.lookPath(bin)
	if err != nil {
		return nil, errors.New("opencode delivery wants the opencode CLI: " + bin + " is not on PATH; pass --opencode-bin <absolute path>")
	}
	if found, err = filepath.Abs(found); err != nil {
		return nil, err
	}
	r := &opencodeRoute{bin: found, session: strings.TrimSpace(c.Str("opencode-session")), dir: strings.TrimSpace(c.Str("opencode-dir"))}
	if r.dir != "" && (!filepath.IsAbs(r.dir) || filepath.Clean(r.dir) != r.dir) {
		return nil, errors.New("--opencode-dir wants a clean absolute directory")
	}
	if r.session == "" {
		r.session = strings.TrimSpace(w.getenv(openCodeSessionEnv))
	}
	if r.session == "" && r.dir != "" && !c.DryRun() {
		ctx, cancel := call()
		r.session, err = notice.LatestOpenCodeSession(ctx, w.opencodeAPI(r.bin), r.dir)
		cancel()
		if err != nil {
			return nil, errors.New("the opencode sessions in " + r.dir + " could not be listed: " + err.Error())
		}
	}
	if r.session == "" && !c.DryRun() {
		return nil, errors.New("opencode delivery wants the friend's session: run this from inside it (its shell sets " + openCodeSessionEnv + "), or pass --opencode-session ses_<id> or --opencode-dir <workspace>")
	}
	if r.session != "" && !notice.ValidOpenCodeSession(r.session) {
		return nil, errors.New("--opencode-session wants an id of the form ses_<letters and digits>")
	}
	return r, nil
}

// installOpenCode probes the session on the running server and makes the
// private notice directory. opencode needs no plugin and no config change:
// delivery is the server's documented session.synthetic operation, reached
// through the CLI, which holds the server's credentials itself.
func (w world) installOpenCode(c *tool.Call, r *opencodeRoute) *tool.Out {
	api := w.opencodeAPI(r.bin)
	ctx, cancel := call()
	err := notice.ProbeOpenCodeSession(ctx, api, r.session)
	cancel()
	if err != nil {
		o := tool.Refuse("the opencode session " + r.session + " is not on the running opencode server: " + err.Error())
		o.Remedy = "start opencode (its background service) and run the setup from inside the friend's session"
		return o
	}
	state := w.getenv("XDG_STATE_HOME")
	if state == "" {
		home, herr := w.antigravityHome()
		if herr != nil {
			return tool.Refuse("opencode setup could not find the home directory: " + herr.Error())
		}
		state = filepath.Join(home, ".local", "state")
	}
	if r.notices, err = notice.MakeDir(filepath.Join(state, "nova-hook", c.Str("as")+"-opencode")); err != nil {
		return tool.Refuse("opencode setup could not make the notice directory: " + err.Error())
	}
	o := tool.Done().As("STATE").Fact("state", "opencode-installed").Fact("session", r.session).Fact("notices", r.notices)
	if r.dir != "" {
		o.Fact("follow", r.dir)
	}
	line(c, o.Note("each notice is one session.synthetic (steer, resume) naming the saved file; a finished turn after it entered is the boundary"))
	return nil
}

// opencodeShim is the route's shim over the CLI.
func (w world) opencodeShim(r *opencodeRoute) notice.Shim {
	api := w.opencodeAPI(r.bin)
	s := &notice.OpenCode{Session: r.session, Notices: r.notices, API: api}
	if r.dir != "" {
		s.Discover = func(ctx context.Context) (string, error) { return notice.LatestOpenCodeSession(ctx, api, r.dir) }
	}
	return s
}

// serviceArgs are the resolved route a supervised service runs with: its
// environment has neither the friend's session nor the shell's PATH.
func (r *opencodeRoute) serviceArgs(args []string) []string {
	args = withFlag(args, "--opencode-bin", r.bin)
	return withFlag(args, "--opencode-session", r.session)
}

// withFlag sets one flag's value in args, replacing the value it had.
func withFlag(args []string, flag, value string) []string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			args[i+1] = value
			return args
		}
	}
	return append(args, flag, value)
}
