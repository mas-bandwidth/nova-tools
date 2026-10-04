package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// runVerb runs one command line on a and is its exit code, stdout and stderr.
func runVerb(a *app, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := a.run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// selftest land, run by this binary in a directory of the test's own, lands the
// canned card: every verb of the flow is this build's, its land merges and pushes to
// the bare origin there, and the base holds the landing (docs/SPEC-SPRINT.md section
// 14). With no --dir its scratch directory is made under the selftest root, beside
// land's clones, and removed when green; a --dir that is not empty is refused, nothing run.
func TestSelftestLandIsGreenOnThisBuild(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	a := newApp(func(string) string { return "" })
	a.landRoot = func() (string, error) { return filepath.Join(cache, "land"), nil }
	code, out, errs := runVerb(a, "selftest", "land")
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, out, "SELFTEST OK land card="+sprint.SelftestCard+" base="+sprint.SelftestBase)
	left, err := os.ReadDir(filepath.Join(cache, "selftest"))
	require.NoError(t, err)
	assert.Empty(t, left, "a green run's scratch directory is removed")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x"), nil, 0o600))
	code, _, errs = runVerb(a, "selftest", "land", "--dir", dir)
	assert.Equal(t, 2, code, "a used directory: %s", errs)
	assert.Contains(t, errs, "--dir wants an empty directory")
}

// switchApp is an app whose server switch runs on an install in the test's own
// directory: the new build's selftest is told, the restart is a fake service manager
// that writes what the restarted server's land loop says to its log, and the window's
// clock moves only when the switch sleeps.
type switchApp struct {
	*app
	install, build, log string
	restarts            []string
}

func newSwitchApp(t *testing.T, selftest error, landSays ...string) *switchApp {
	t.Helper()
	dir := t.TempDir()
	s := &switchApp{app: newApp(func(string) string { return "" }), install: filepath.Join(dir, "nova-sprint"), build: filepath.Join(dir, "nova-sprint.new"), log: filepath.Join(dir, "run.log")}
	require.NoError(t, os.WriteFile(s.install, []byte("old build"), 0o755))
	require.NoError(t, os.WriteFile(s.build, []byte("new build"), 0o755))
	require.NoError(t, os.WriteFile(s.log, []byte("15:00:00 LAND FAILED before the switch, never read\n"), 0o600))
	now := time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.sleep = func(d time.Duration) { now = now.Add(d) }
	s.selftestRun = func(context.Context, string) error { return selftest }
	s.restart = func(_ context.Context, command string) error {
		s.restarts = append(s.restarts, command)
		if len(s.restarts) == 1 && len(landSays) > 0 {
			f, err := os.OpenFile(s.log, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer func() { require.NoError(t, f.Close()) }()
			_, err = f.WriteString(strings.Join(landSays, "\n") + "\n")
			return err
		}
		return nil
	}
	return s
}

func (s *switchApp) installed(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(s.install)
	require.NoError(t, err)
	return string(b)
}

// server switch through the verb: green on a landing, rolled back (exit 1) on a land
// that fails after the restart, refused with nothing changed on a red selftest, put
// back by --rollback, and a dry run that touches nothing; a usage refusal names every
// problem at once. No real service manager runs: the restart is the test's.
func TestServerSwitchKeepsTheOldBinaryAndRollsBack(t *testing.T) {
	t.Parallel()
	args := func(s *switchApp, more ...string) []string {
		return append([]string{"server", "switch", s.build, "--install", s.install, "--log", s.log, "--restart", "restart-the-server", "--window", "2m", "--every", "30s"}, more...)
	}
	t.Run("a landing confirms the switch", func(t *testing.T) {
		t.Parallel()
		s := newSwitchApp(t, nil, "15:00:30 LAND OK stream=s1 cards=1 base=main tip=abc ids=s1-1")
		code, out, errs := runVerb(s.app, args(s)...)
		require.Equal(t, 0, code, "%s%s", out, errs)
		assert.Contains(t, out, "SWITCH OK install=")
		assert.Contains(t, out, "confirmed=landed")
		assert.Equal(t, "new build", s.installed(t))
		kept, err := os.ReadFile(s.install + ".prev")
		require.NoError(t, err)
		assert.Equal(t, "old build", string(kept))
		assert.Equal(t, []string{"restart-the-server"}, s.restarts)
	})
	t.Run("a failed land in the window rolls back", func(t *testing.T) {
		t.Parallel()
		s := newSwitchApp(t, nil, "15:00:30 LAND FAILED the merge queue could not be read: EOF")
		code, out, errs := runVerb(s.app, args(s)...)
		assert.Equal(t, 1, code, "%s%s", out, errs)
		assert.Contains(t, errs, "SWITCH FAILED rolled_back=yes")
		assert.Contains(t, errs, "NOTE the land line: 15:00:30 LAND FAILED")
		assert.Equal(t, "old build", s.installed(t))
		assert.Len(t, s.restarts, 2)
	})
	t.Run("a red selftest changes nothing", func(t *testing.T) {
		t.Parallel()
		s := newSwitchApp(t, errors.New("SELFTEST FAILED land step=15"))
		code, _, errs := runVerb(s.app, args(s)...)
		assert.Equal(t, 1, code, errs)
		assert.Contains(t, errs, "SWITCH REFUSED: the new build's selftest land is red")
		assert.Equal(t, "old build", s.installed(t))
		assert.NoFileExists(t, s.install+".prev")
		assert.Empty(t, s.restarts)
	})
	t.Run("rollback and the dry run", func(t *testing.T) {
		t.Parallel()
		s := newSwitchApp(t, nil)
		code, out, errs := runVerb(s.app, args(s, "--dry-run")...)
		require.Equal(t, 0, code, errs)
		assert.Contains(t, out, "SWITCH OK dry_run=yes")
		assert.Equal(t, "old build", s.installed(t))
		code, out, errs = runVerb(s.app, args(s)...)
		require.Equal(t, 0, code, "%s%s", out, errs)
		assert.Contains(t, out, "confirmed=quiet", "a window with no land keeps the new binary")
		code, out, errs = runVerb(s.app, "server", "switch", "--rollback", "--install", s.install, "--restart", "restart-the-server")
		require.Equal(t, 0, code, "%s%s", out, errs)
		assert.Contains(t, out, "SWITCH OK rolled_back=yes")
		assert.Equal(t, "old build", s.installed(t))
	})
	t.Run("usage names every problem", func(t *testing.T) {
		t.Parallel()
		code, _, errs := runVerb(newApp(func(string) string { return "" }), "server", "switch")
		assert.Equal(t, 2, code)
		for _, want := range []string{"--install wants", "wants one word, the new build's path", "--log wants"} {
			assert.Contains(t, errs, want)
		}
	})
}
