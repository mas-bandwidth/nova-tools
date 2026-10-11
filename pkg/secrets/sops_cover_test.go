package secrets

// The unit cover for CheckAgeKeygenVersion, which the per-function coverage
// table held at 0.0%. The function's only seam is the age-keygen path it is
// handed, so the main-path rows run a fake tool written into the test's own
// temporary directory -- testbin.WriteExecutable holds the fork lock, so no
// parallel test can fork while the fake is open for writing -- and the
// refusal rows stop before any child is built.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// sopsCoverFakeAgeKeygen writes a fake age-keygen whose --version probe prints
// output, and returns the path to hand the probe.
func sopsCoverFakeAgeKeygen(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "age-keygen")
	require.NoError(t, testbin.WriteExecutable(path, []byte("#!/bin/sh\necho '"+output+"'\n"), 0o755))
	return path
}

// TestSopsCoverRemediesNameThePlatformsInstall pins the install or upgrade
// remedy each refusal carries against the platform the check runs on: Homebrew
// on darwin, and the command that platform actually has elsewhere (go install
// for sops, apt for age-keygen) -- never brew. It reaches the remedy through
// the platform-taking twins, so one run covers both platforms. The commands it
// pins must match internal/up/layout.go's tools table (nova-tools#5016).
func TestSopsCoverRemediesNameThePlatformsInstall(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell-script fakes are unavailable on Windows; the remedy is a string and the probe runs the binary at the path it is handed")
	}
	for _, tc := range []struct {
		name   string
		goos   string
		tool   string
		absent bool
		want   string
	}{
		{"darwin installs sops with brew", "darwin", "sops", true, "run: brew install sops"},
		{"darwin upgrades sops with brew", "darwin", "sops", false, "run: brew upgrade sops"},
		{"darwin installs age-keygen with brew", "darwin", "age-keygen", true, "run: brew install age"},
		{"darwin upgrades age-keygen with brew", "darwin", "age-keygen", false, "run: brew upgrade age"},
		{"linux installs sops with go install", "linux", "sops", true, "run: go install github.com/getsops/sops/v3/cmd/sops@latest"},
		{"linux upgrades sops with go install", "linux", "sops", false, "run: go install github.com/getsops/sops/v3/cmd/sops@latest"},
		{"linux installs age-keygen with apt", "linux", "age-keygen", true, "run: sudo apt-get install -y age"},
		{"linux upgrades age-keygen with apt", "linux", "age-keygen", false, "run: sudo apt-get install -y age"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var path string
			switch {
			case tc.absent:
				path = filepath.Join(t.TempDir(), "no-such-"+tc.tool)
			case tc.tool == "sops":
				path = filepath.Join(t.TempDir(), "sops")
				require.NoError(t, testbin.WriteExecutable(path, []byte("#!/bin/sh\necho 'sops 3.9.0'\n"), 0o755))
			default:
				path = sopsCoverFakeAgeKeygen(t, "1.2.9")
			}
			var err error
			if tc.tool == "sops" {
				_, err = checkSopsVersion(realExecCommand, path, tc.goos)
			} else {
				_, err = checkAgeKeygenVersion(realExecCommand, path, tc.goos)
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			if tc.goos == "linux" {
				assert.NotContains(t, err.Error(), "brew")
			}
		})
	}
}

// TestSopsCoverAgeKeygenVersionParsesProbeOutput pins the main path: the probe
// parses the version the binary prints, with and without the v prefix, returns
// it bare, and refuses an older binary with the upgrade remedy and output it
// cannot parse.
func TestSopsCoverAgeKeygenVersionParsesProbeOutput(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell-script fakes are unavailable on Windows; the probe runs the binary at the path it is handed")
	}
	ageUpgrade := "run: brew upgrade age"
	if runtime.GOOS == "linux" {
		ageUpgrade = "run: sudo apt-get install -y age"
	}
	for _, tc := range []struct {
		name    string
		output  string
		wantVer string
		wantErr string
	}{
		{"the minimum version is accepted", "1.3.2", "1.3.2", ""},
		{"a v-prefixed newer version is accepted", "v1.4.0", "1.4.0", ""},
		{"an older version is refused with the remedy", "1.2.9", "1.2.9", "too old; minimum required is 1.3.2; " + ageUpgrade},
		{"unparseable output is refused", "age-keygen fake", "", "unable to parse age-keygen version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ver, err := CheckAgeKeygenVersion(realExecCommand, sopsCoverFakeAgeKeygen(t, tc.output))
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
			assert.Equal(t, tc.wantVer, ver)
		})
	}
}

// TestSopsCoverAgeKeygenVersionRefusesABinaryItCannotExecute pins the refusal
// that fires before any child is built: a path that is absent, and a file that
// exists but carries no executable bit.
func TestSopsCoverAgeKeygenVersionRefusesABinaryItCannotExecute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rows := []struct {
		name string
		path string
	}{
		{"absent", filepath.Join(dir, "no-such-age-keygen")},
	}
	if runtime.GOOS != "windows" {
		dead := filepath.Join(dir, "age-keygen-dead")
		require.NoError(t, os.WriteFile(dead, []byte("#!/bin/sh\n"), 0o644))
		rows = append(rows, struct {
			name string
			path string
		}{"present but not executable", dead})
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := CheckAgeKeygenVersion(realExecCommand, tc.path)
			require.Error(t, err)
			ageInstall := "is absent or not executable; run: brew install age"
			if runtime.GOOS == "linux" {
				ageInstall = "is absent or not executable; run: sudo apt-get install -y age"
			}
			assert.Contains(t, err.Error(), ageInstall)
		})
	}
}
