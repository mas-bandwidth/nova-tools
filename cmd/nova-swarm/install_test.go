package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// install disk-guard writes a unit that runs one nova-swarm disk-guard pass every
// --every, with the verb's own flags after --, for macOS and Linux, into a fake home,
// loaded by the test's loader; uninstall removes it; mirror-refresh is refused until
// nova-swarm has the verb it would run (card every-unit-installed-by-a-verb).
func TestInstallDiskGuardWritesAUnitThatRunsTheVerbEveryPeriod(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			var calls []string
			h := unitHost{goos: goos, home: func() (string, error) { return home, nil },
				exe:    func() (string, error) { return "/opt/nova/bin/nova-swarm", nil },
				load:   func(_, op, p string) error { calls = append(calls, op+" "+filepath.Base(p)); return nil },
				getenv: func(string) string { return "" }}
			k, _ := sprint.UnitKindOf("disk-guard")
			path := filepath.Join(sprint.UnitDir(goos, home, h.getenv), k.File(goos))
			run := func(f func(unitHost, []string, *bytes.Buffer, *bytes.Buffer) int, args ...string) (int, string, string) {
				var out, errb bytes.Buffer
				code := f(h, args, &out, &errb)
				return code, out.String(), errb.String()
			}
			install := func(h unitHost, a []string, o, e *bytes.Buffer) int { return installUnit(h, a, o, e) }
			uninstall := func(h unitHost, a []string, o, e *bytes.Buffer) int { return uninstallUnit(h, a, o, e) }

			code, out, errs := run(install, "disk-guard", "--every", "15m", "--", "--root", "/srv/run/member", "--cache-max-gb", "20")
			require.Equal(t, 0, code, errs)
			assert.Contains(t, out, "INSTALL DISK-GUARD OK unit="+path+" written=true loaded=true")
			b, err := os.ReadFile(path)
			require.NoError(t, err)
			args, err := sprint.UnitArgs(goos, b)
			require.NoError(t, err)
			assert.Equal(t, []string{"/opt/nova/bin/nova-swarm", "disk-guard", "--root", "/srv/run/member", "--cache-max-gb", "20"}, args)
			if goos == "darwin" {
				assert.Contains(t, string(b), "<integer>900</integer>", "one pass every 15 minutes")
			} else {
				assert.Contains(t, string(b), "RestartSec=900")
			}
			states, err := sprint.CheckUnits(filepath.Dir(path), goos, []sprint.UnitKind{k})
			require.NoError(t, err)
			assert.Equal(t, sprint.UnitInstalled, states[0].State, states[0].Why)

			code, out, errs = run(uninstall, "disk-guard")
			require.Equal(t, 0, code, errs)
			assert.Contains(t, out, "UNINSTALL DISK-GUARD OK unit="+path+" removed=true")
			assert.NoFileExists(t, path)
			assert.Equal(t, []string{"load " + k.File(goos), "unload " + k.File(goos)}, calls)

			for _, tc := range []struct {
				args []string
				want string
			}{
				{[]string{"mirror-refresh"}, "nova-swarm has no mirror verb"},
				{[]string{"store"}, "run: nova-redis install store"},
				{nil, "install wants the unit's kind first"},
				{[]string{"disk-guard", "--every", "0s"}, "--every is the least time between two passes, above zero"},
			} {
				code, _, errs = run(install, tc.args...)
				assert.Equal(t, 2, code, tc.args)
				assert.Contains(t, errs, tc.want, tc.args)
			}
			assert.Len(t, calls, 2, "a refused install loads nothing")
		})
	}
}
