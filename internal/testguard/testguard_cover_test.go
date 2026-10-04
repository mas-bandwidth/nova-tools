package testguard

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// coverNoProgram names a program no machine has on PATH: LookPath fails, so
// isFakeProgram says not-a-fake and an armed guard panics naming the command.
// The refusal rows reach the panic on every machine, without a subprocess.
const coverNoProgram = "nova-testguard-cover-no-such-program"

// TestTestguardCoverDefaultGuardSurface covers the package-level surface --
// Refusing, AllowHosts, RefuseHosts -- which every guarded seam in the tree
// calls and which delegates to the process-wide defaultGuard. The rows run
// sequentially (no t.Parallel below) because defaultGuard is shared state:
// each row arms and releases it, or injects its lookPath seam, inside one
// subtest, so no row can observe another's scope. The baseline row runs first,
// while nothing is forced.
func TestTestguardCoverDefaultGuardSurface(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"RefusingReportsTheEnvironmentWhenNothingIsForced", func(t *testing.T) {
			// The package-level accessor must equal the armed state Reload
			// read from the environment at start; the expectation is computed
			// from the same source so the row is green with or without the
			// variable set.
			assert.Equal(t, os.Getenv(EnvNoHost) == "1", Refusing(),
				"unforced, the package-level Refusing must report the environment's answer")
		}},
		{"RefusingIsTrueWhileTheDefaultGuardIsForced", func(t *testing.T) {
			disarm := defaultGuard.Arm()
			defer disarm()
			assert.True(t, Refusing(),
				"Arm on the default guard must make the package-level Refusing report armed whatever the environment says")
		}},
		{"RefuseHostsPanicsNamingTheCommandAndTheRemedy", func(t *testing.T) {
			disarm := defaultGuard.Arm()
			defer disarm()
			var r any
			func() {
				defer func() { r = recover() }()
				RefuseHosts(coverNoProgram, "host.invalid", "uptime")
			}()
			msg, ok := r.(string)
			require.True(t, ok, "the armed default guard must refuse the package-level seam with a string panic; got %v", r)
			for _, want := range []string{EnvNoHost, `"` + coverNoProgram + `"`, `"host.invalid"`, "testguard.AllowHosts"} {
				assert.Contains(t, msg, want, "the refusal must carry %s; got %q", want, msg)
			}
		}},
		{"RefuseHostsLetsAFakeUnderATempRootRun", func(t *testing.T) {
			old := defaultGuard.lookPath
			defer func() { defaultGuard.lookPath = old }()
			dir := t.TempDir()
			fake := filepath.Join(dir, "ssh")
			if runtime.GOOS == "windows" {
				fake += ".bat"
			}
			require.NoError(t, testbin.WriteExecutable(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755))
			defaultGuard.lookPath = func(program string) (string, error) {
				if program == "ssh" {
					return fake, nil
				}
				return old(program)
			}
			disarm := defaultGuard.Arm()
			defer disarm()
			assert.NotPanics(t, func() { RefuseHosts("ssh", "host.invalid", "uptime") },
				"a fake that resolves inside a temp directory is not a host, seen through the package-level seam")
		}},
		{"AllowHostsPassesTheSeamInsideItsScopeAndRefusesAgainAfter", func(t *testing.T) {
			disarm := defaultGuard.Arm()
			defer disarm()
			allow := AllowHosts()
			assert.NotPanics(t, func() { RefuseHosts(coverNoProgram, "host.invalid") },
				"inside an AllowHosts scope the package-level seam must run")
			allow()
			assert.Panics(t, func() { RefuseHosts(coverNoProgram, "host.invalid") },
				"once the returned function closes the scope the guard must refuse again")
		}},
		{"AllowHostsNestsAndClosingTwiceIsNoSecondDecrement", func(t *testing.T) {
			disarm := defaultGuard.Arm()
			defer disarm()
			outer := AllowHosts()
			inner := AllowHosts()
			inner()
			inner() // a second close must not undo the outer scope
			assert.NotPanics(t, func() { RefuseHosts(coverNoProgram, "host.invalid") },
				"the outer scope must still stand after the inner one is closed twice")
			outer()
			assert.Panics(t, func() { RefuseHosts(coverNoProgram, "host.invalid") },
				"the guard must refuse again once every scope has closed")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
