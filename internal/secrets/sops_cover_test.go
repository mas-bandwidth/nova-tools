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

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// sopsCoverFakeAgeKeygen writes a fake age-keygen whose --version probe prints
// output, and returns the path to hand the probe.
func sopsCoverFakeAgeKeygen(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "age-keygen")
	require.NoError(t, testbin.WriteExecutable(path, []byte("#!/bin/sh\necho '"+output+"'\n"), 0o755))
	return path
}

// TestAgeUpgradeCmdForOS returns the GOOS-specific age upgrade commands.
func TestAgeUpgradeCmdForOS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		goos string
		want string
	}{
		{"darwin uses brew", "darwin", "brew upgrade age"},
		{"linux uses apt-get, dnf, or curl", "linux", "sudo apt-get install --only-upgrade age || sudo dnf upgrade age || curl -L https://github.com/" + "FiloSottile/age/releases/download/v" + MinAgeKeygenVersion + "/age-" + MinAgeKeygenVersion + "-linux-amd64.tar.gz | tar xz && sudo mv age/age /usr/local/bin/ && sudo mv age/age-keygen /usr/local/bin/"},
		{"windows defaults to brew", "windows", "brew upgrade age"},
		{"unknown OS defaults to brew", "unknown", "brew upgrade age"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ageUpgradeCmdForOS(tc.goos)
			assert.Equal(t, tc.want, got)
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
	for _, tc := range []struct {
		name    string
		output  string
		wantVer string
		wantErr string
	}{
		{"the minimum version is accepted", "1.3.2", "1.3.2", ""},
		{"a v-prefixed newer version is accepted", "v1.4.0", "1.4.0", ""},
		{"an older version is refused with the remedy", "1.2.9", "1.2.9", "too old; minimum required is 1.3.2; run: " + ageUpgradeCmd()},
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
			assert.Contains(t, err.Error(), "is absent or not executable; run: "+ageUpgradeCmd())
		})
	}
}
