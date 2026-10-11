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

// sopsCoverFakeSops writes a fake sops whose --version probe prints output, and
// returns the path to hand the probe.
func sopsCoverFakeSops(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sops")
	require.NoError(t, testbin.WriteExecutable(path, []byte("#!/bin/sh\necho '"+output+"'\n"), 0o755))
	return path
}

// TestSopsCoverRemedyNamesThePlatformsInstall pins the one command each refusal
// names on darwin and linux: Homebrew on darwin, the go module for sops and the
// apt package for age-keygen on linux. It reaches the remedy through the
// unexported twins so each row chooses its platform; the exported checks pass
// runtime.GOOS.
func TestSopsCoverRemedyNamesThePlatformsInstall(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell-script fakes are unavailable on Windows; the probe runs the binary at the path it is handed")
	}
	const (
		brewInstallSops = "brew install sops"
		brewUpgradeSops = "brew upgrade sops"
		brewInstallAge  = "brew install age"
		brewUpgradeAge  = "brew upgrade age"
		goInstallSops   = "go install github.com/getsops/sops/v3/cmd/sops@latest"
		aptInstallAge   = "sudo apt-get install -y age"
	)
	dir := t.TempDir()
	oldSops := sopsCoverFakeSops(t, "sops 3.9.0")
	oldAge := sopsCoverFakeAgeKeygen(t, "1.2.9")
	for _, tc := range []struct {
		name string
		tool string
		goos string
		path string
		want string
	}{
		{"sops absent on darwin", "sops", "darwin", filepath.Join(dir, "no-sops"), brewInstallSops},
		{"sops absent on linux", "sops", "linux", filepath.Join(dir, "no-sops"), goInstallSops},
		{"sops old on darwin", "sops", "darwin", oldSops, brewUpgradeSops},
		{"sops old on linux", "sops", "linux", oldSops, goInstallSops},
		{"age-keygen absent on darwin", "age", "darwin", filepath.Join(dir, "no-age-keygen"), brewInstallAge},
		{"age-keygen absent on linux", "age", "linux", filepath.Join(dir, "no-age-keygen"), aptInstallAge},
		{"age-keygen old on darwin", "age", "darwin", oldAge, brewUpgradeAge},
		{"age-keygen old on linux", "age", "linux", oldAge, aptInstallAge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			if tc.tool == "sops" {
				_, err = checkSopsVersion(realExecCommand, tc.path, tc.goos)
			} else {
				_, err = checkAgeKeygenVersion(realExecCommand, tc.path, tc.goos)
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
	oldRemedy := "sudo apt-get install -y age"
	if runtime.GOOS == "darwin" {
		oldRemedy = "brew upgrade age"
	}
	for _, tc := range []struct {
		name    string
		output  string
		wantVer string
		wantErr string
	}{
		{"the minimum version is accepted", "1.3.2", "1.3.2", ""},
		{"a v-prefixed newer version is accepted", "v1.4.0", "1.4.0", ""},
		{"an older version is refused with the remedy", "1.2.9", "1.2.9", "too old; minimum required is 1.3.2; run: " + oldRemedy},
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
	absentRemedy := "sudo apt-get install -y age"
	if runtime.GOOS == "darwin" {
		absentRemedy = "brew install age"
	}
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
			assert.Contains(t, err.Error(), "is absent or not executable; run: "+absentRemedy)
		})
	}
}
