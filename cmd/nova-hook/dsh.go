package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

type dshRoute struct {
	route dshdesktop.Route
	since time.Time
	// profile is the harness desktop profile the include is mounted into.
	profile string
	// follow repoints the receiver at the friend's newest live session; nil
	// when --dsh-session pins one.
	follow *dshdesktop.Follower
	// session is the discovered session, for the report.
	session dshdesktop.Session
	// made lists the default directories to create before validation.
	made []string
}

var dshName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// dshConfig refuses partial routes before any hook record or lease is written.
// --harness deepseek (or its alias dsh) selects the route. Alone it is enough:
// the events directory defaults to ~/.local/state/nova-hook/<as>-dsh (private),
// the include to <dsh home>/<as>-hook/hooks.json, and the session is the newest
// live one in the friend's harness workspace, followed when she starts another.
// The --dsh-* flags override each default; --dsh-session pins and stops following.
// No gateway and no cookie: delivery uses the harness agent registry.
func (w world) dshConfig(c *tool.Call) (*dshRoute, error) {
	harness := c.Str("harness")
	r := &dshRoute{
		route: dshdesktop.Route{
			EventsDir: c.Str("dsh-events-dir"),
			HooksFile: c.Str("dsh-hooks-file"),
			SessionID: c.Str("dsh-session"),
		},
	}
	rawSince := c.Str("dsh-since")
	if rawSince != "" {
		var err error
		r.since, err = time.Parse(time.RFC3339, rawSince)
		if err != nil || rawSince != r.since.UTC().Format(time.RFC3339) {
			return nil, errors.New("dsh cutover wants --dsh-since as a fixed UTC timestamp, for example 2026-10-14T20:00:00Z; retain it in launch configuration across restarts")
		}
	}
	if harness == "" && r.route.EventsDir == "" && r.route.HooksFile == "" && r.route.SessionID == "" && rawSince == "" {
		return nil, nil
	}
	if harness != "" && harness != "deepseek" && harness != "dsh" {
		return nil, errors.New("--harness wants deepseek (or dsh) for the --dsh-* flags; other harnesses keep their own route")
	}
	if c.Bool("once") {
		return nil, errors.New("dsh delivery wants a running hook client; omit --once")
	}
	if w.dshValidate == nil || w.dshDeliver == nil {
		return nil, errors.New("dsh delivery wants a configured local native receiver")
	}
	as := c.Str("as")
	if !dshName.MatchString(as) {
		return nil, errors.New("dsh delivery wants --as naming the friend, letters, digits, hyphen or underscore")
	}
	home := w.getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		return nil, errors.New("dsh delivery wants HOME to find the harness and the events directory")
	}
	dshHome, err := dshdesktop.Home(w.getenv)
	if err != nil {
		return nil, err
	}
	r.profile = dshdesktop.ProfileDir(dshHome)
	if r.route.EventsDir == "" {
		r.route.EventsDir = filepath.Join(home, ".local", "state", "nova-hook", as+"-dsh")
		r.made = append(r.made, r.route.EventsDir)
	}
	if r.route.HooksFile == "" {
		r.route.HooksFile = filepath.Join(dshHome, as+"-hook", "hooks.json")
		r.made = append(r.made, filepath.Dir(r.route.HooksFile))
	}
	if r.route.SessionID == "" {
		r.follow = &dshdesktop.Follower{Home: dshHome, Name: as, EventsDir: r.route.EventsDir, Held: w.dshHeld}
		s, err := dshdesktop.Discover(dshHome, as, w.dshHeld)
		if err != nil {
			return nil, errors.New(err.Error() + " (or pin one with --dsh-session)")
		}
		r.session = s
		r.route.SessionID = s.ID
	}
	if c.DryRun() {
		if err := dshdesktop.ValidateRoute(r.route); err != nil {
			return nil, err
		}
		return r, nil
	}
	for _, dir := range r.made {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err := w.dshValidate(r.route); err != nil {
		return nil, err
	}
	return r, nil
}

// dshRestartNote is the one line printed when the harness must restart to load
// a changed include, module or profile mount.
const dshRestartNote = "restart DeepSeek Harness once to load the nova-hook plugin"

// dshSetup writes the native modules and include, points the receiver at the
// session and mounts the include in the desktop profile. It reports whether a
// running harness must restart to load what changed.
func dshSetup(r *dshRoute) (restart bool, err error) {
	restart, err = dshdesktop.Install(r.route)
	if err != nil {
		return false, err
	}
	mounted, err := dshdesktop.Mount(r.profile, r.route.HooksFile)
	if err != nil {
		return false, err
	}
	return restart || mounted, nil
}

// shim's Follow: rediscover before each push.
func (r *dshRoute) followFunc() func() error {
	if r.follow == nil {
		return nil
	}
	f := *r.follow
	return func() error { _, _, err := f.Follow(); return err }
}
