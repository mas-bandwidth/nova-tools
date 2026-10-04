package sprint

// server switch: the server's binary replaced by a new build, the previous one kept,
// and rolled back when a land fails in the window after (docs/SPEC-SPRINT.md section
// 14, switching the server's binary). The steps, in order: the new build's selftest
// land (red: nothing is touched), the installed binary kept beside it, the new one
// written in its place, the server restarted, then its log watched for the window: a
// land that fails puts the kept binary back and restarts again; a land that lands, or
// a window that passes quiet, keeps the new one.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// The ends of a switch or a rollback.
const (
	SwitchOK         = "ok"          // the new binary serves (a rollback: the kept one does)
	SwitchRefused    = "refused"     // nothing was changed
	SwitchRolledBack = "rolled-back" // the kept binary is back in its place
	SwitchFailed     = "failed"      // the rollback itself failed: the install is in doubt
)

// Switch is one switch of the server's binary. Selftest runs `<binary> selftest land`
// (nil: the build is not selftested, which a caller never chooses); Restart is the
// server's supervisor's restart (nil: none, and the run loop's own stop on a replaced
// binary is the restart, section 14); Lines is the server's log lines written since the
// last call; Now and Sleep are the clock the window is watched on.
type Switch struct {
	Binary, Install, Keep string
	Selftest              func(ctx context.Context, binary string) error
	Restart               func(ctx context.Context) error
	Lines                 func() ([]string, error)
	Window, Every         time.Duration
	Now                   func() time.Time
	Sleep                 func(ctx context.Context, d time.Duration) error
}

// SwitchResult is how a switch or a rollback ended: Land is the land line that ended
// the watch (a failure, or the landing that confirmed the binary), Landed says a land
// landed in the window, Restarts how many restarts were asked.
type SwitchResult struct {
	Status   string
	Why      string
	Land     string
	Landed   bool
	Restarts int
}

// LandLine reads a line of the server's log (`15:04:05 LAND ...`, landloop's lines):
// failed when it is a land that failed through no card (LAND FAILED, or a LAND REFUSED
// that records no fact: git failed for a reason of its own, which blames the lander),
// landed when it is a batch that landed. A refusal with a fact (conflict, red,
// rejected) is a card's, and neither.
func LandLine(line string) (failed, landed bool) {
	f := strings.Fields(line)
	if len(f) > 0 && strings.Count(f[0], ":") == 2 {
		f = f[1:]
	}
	if len(f) < 2 || f[0] != "LAND" {
		return false, false
	}
	switch strings.TrimSuffix(f[1], ":") {
	case "OK":
		return false, true
	case "FAILED":
		return true, false
	case "REFUSED":
		for _, w := range f[2:] {
			if strings.HasPrefix(w, "fact=") {
				return false, false
			}
		}
		return true, false
	}
	return false, false
}

// Run switches: the new build's selftest first, then keep, install, restart and watch.
func (s Switch) Run(ctx context.Context) SwitchResult {
	install, why := s.paths()
	if why != "" {
		return SwitchResult{Status: SwitchRefused, Why: why}
	}
	if fi, err := os.Stat(s.Binary); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return SwitchResult{Status: SwitchRefused, Why: s.Binary + " is no executable file: the new build to switch to"}
	}
	if s.Selftest == nil {
		return SwitchResult{Status: SwitchRefused, Why: "no selftest: a build is switched to only after its selftest land is green"}
	}
	if err := s.Selftest(ctx, s.Binary); err != nil {
		return SwitchResult{Status: SwitchRefused, Why: "the new build's selftest land is red, nothing was changed: " + err.Error()}
	}
	if err := copyBinary(install, s.Keep); err != nil {
		return SwitchResult{Status: SwitchRefused, Why: "the installed binary could not be kept, nothing was installed: " + err.Error()}
	}
	if err := copyBinary(s.Binary, install); err != nil {
		return SwitchResult{Status: SwitchRefused, Why: "the new build could not be installed (the installed binary is unchanged): " + err.Error()}
	}
	r := SwitchResult{Status: SwitchOK}
	if err := s.restart(ctx, &r); err != nil {
		return s.back(ctx, install, r, "the restart on the new binary failed: "+err.Error())
	}
	end := s.Now().Add(s.Window)
	for {
		lines, err := s.Lines()
		if err != nil {
			return s.back(ctx, install, r, "the server's log could not be read, so no land can be seen: "+err.Error())
		}
		for _, line := range lines {
			failed, landed := LandLine(line)
			switch {
			case failed:
				r.Land = line
				return s.back(ctx, install, r, "a land failed in the window")
			case landed:
				r.Land, r.Landed = line, true
				return r
			}
		}
		if !s.Now().Before(end) {
			return r
		}
		if err := s.Sleep(ctx, min(s.Every, end.Sub(s.Now()))); err != nil {
			return s.back(ctx, install, r, "the watch was cut short: "+err.Error())
		}
	}
}

// Rollback puts the kept binary back in its place and restarts the server.
func (s Switch) Rollback(ctx context.Context) SwitchResult {
	install, why := s.paths()
	if why != "" {
		return SwitchResult{Status: SwitchRefused, Why: why}
	}
	if fi, err := os.Stat(s.Keep); err != nil || !fi.Mode().IsRegular() {
		return SwitchResult{Status: SwitchRefused, Why: "no binary is kept at " + s.Keep + ": a switch keeps one; nothing was changed"}
	}
	return s.back(ctx, install, SwitchResult{}, "asked")
}

// back puts the kept binary in place and restarts: rolled back, or failed when either
// step did not take.
func (s Switch) back(ctx context.Context, install string, r SwitchResult, why string) SwitchResult {
	r.Status, r.Why = SwitchRolledBack, why
	if err := copyBinary(s.Keep, install); err != nil {
		r.Status, r.Why = SwitchFailed, why+"; the kept binary could not be put back: "+err.Error()
		return r
	}
	if err := s.restart(ctx, &r); err != nil {
		r.Status, r.Why = SwitchFailed, why+"; the kept binary is back and the restart on it failed: "+err.Error()
	}
	return r
}

func (s Switch) restart(ctx context.Context, r *SwitchResult) error {
	if s.Restart == nil {
		return nil
	}
	r.Restarts++
	return s.Restart(ctx)
}

// paths is the installed binary's real path (a link is followed: the file it names is
// what the supervisor runs), or why the paths are refused.
func (s Switch) paths() (string, string) {
	if s.Install == "" || s.Keep == "" {
		return "", "the installed binary's path and the path to keep it at are both wanted"
	}
	install, err := filepath.EvalSymlinks(s.Install)
	if err != nil {
		return "", "no binary is installed at " + s.Install + ": " + err.Error()
	}
	if fi, err := os.Stat(install); err != nil || !fi.Mode().IsRegular() {
		return "", install + " is no file: the installed binary"
	}
	if keep, err := filepath.Abs(s.Keep); err != nil || keep == install {
		return "", "the binary is kept at a path of its own, not " + s.Keep
	}
	return install, ""
}

// copyBinary writes the file at from to the path to, whole or not at all, executable.
func copyBinary(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return errors.New(from + " is empty")
	}
	if err := atomicfile.WriteFile(filepath.Clean(to), b, 0o755, atomicfile.ExactMode()); err != nil {
		return fmt.Errorf("writing %s: %w", to, err)
	}
	return nil
}
