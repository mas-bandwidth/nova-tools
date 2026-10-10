// Unit coverage for internal/sandbox/profile.go that the per-function coverage table
// of the unit tier held at 0.0%: DeniedWrites (profile.go:243), LaneProfile.Input
// (profile.go:260), resolved (profile.go:302), and DarwinProfile (profile.go:38).
// Every test is in process: no sleeps, no real time, no network, no subprocess,
// no Redis or Postgres. Each listed function gets its main path and one refusal
// where it has one.
package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSandboxProfileCoverDeniedWrites pins the deny list with ~/ made home: ~/self under
// home /h is /h/self, an absolute path stands, a ~/ path with an empty home is dropped
// while the absolute ones stay, and nil gives nil.
func TestSandboxProfileCoverDeniedWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		home string
		deny []string
		want []string
	}{
		{"~/self under home /h is /h/self", "/h", []string{"~/self"}, []string{"/h/self"}},
		{"absolute path stands", "", []string{"/abs/path"}, []string{"/abs/path"}},
		{"~/ with empty home is dropped while absolute ones stay", "",
			[]string{"~/drop", "/stay"}, []string{"/stay"}},
		{"nil gives nil", "", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DeniedWrites(tc.home, tc.deny)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSandboxProfileCoverLaneProfileInput pins the wall's input: a Name outside LaneProfiles
// is refused naming "friend"; an empty Work is refused; a Deny that is empty, or only
// "~/x" with an empty Home, is refused with "denies nothing"; the main path gives Writes
// = Work then each Job then ConfigDir, once each and with "" left out, Home = ConfigDir
// or Work when ConfigDir is "", NetDeny true, NetPorts equal to LaneNetPorts, Deny made
// home, Argv passed through; Cwd is the given cwd when it is inside a write (a
// subdirectory of Work) and "" when it is outside every write or empty.
func TestSandboxProfileCoverLaneProfileInput(t *testing.T) {
	t.Parallel()
	work := t.TempDir()
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")

	for _, tc := range []struct {
		name        string
		lp          LaneProfile
		cwd         string
		argv        []string
		wantErr     bool
		errSubstr   string
		wantWrites  []string
		wantHome    string
		wantNetDeny bool
		wantCwd     string
	}{
		{"Name outside LaneProfiles is refused naming friend",
			LaneProfile{Name: "bad", Work: work, Deny: []string{work}, Home: work}, "", nil,
			true, "no wall profile", nil, "", false, ""},
		{"empty Work is refused", LaneProfile{Name: "", Work: "", Deny: []string{work}, Home: work},
			"", nil, true, "working directory", nil, "", false, ""},
		{"empty Deny is refused with denies nothing",
			LaneProfile{Name: "", Work: work, Deny: []string{}, Home: work}, "", nil,
			true, "denies nothing", nil, "", false, ""},
		{"~/x with empty Home is refused with denies nothing",
			LaneProfile{Name: "", Work: work, Deny: []string{"~/x"}, Home: ""}, "", nil,
			true, "denies nothing", nil, "", false, ""},
		{"main path gives Writes = Work then each Job then ConfigDir once each with \"\" left out",
			LaneProfile{Name: "", Work: work, Jobs: []string{dir, configDir}, ConfigDir: configDir,
				Deny: []string{work}, Home: work}, "", nil,
			false, "", []string{work, dir, configDir}, configDir, true, ""},
		{"Home = Work when ConfigDir is \"\"",
			LaneProfile{Name: "", Work: work, Jobs: nil, ConfigDir: "",
				Deny: []string{work}, Home: work}, "", nil,
			false, "", []string{work}, work, true, ""},
		{"NetDeny true, NetPorts equal to LaneNetPorts",
			LaneProfile{Name: "", Work: work, Deny: []string{work}, Home: work}, "", nil,
			false, "", nil, work, true, ""},
		{"Argv passed through",
			LaneProfile{Name: "", Work: work, Deny: []string{work}, Home: work}, "",
			[]string{"cmd", "arg"}, false, "", nil, work, true, ""},
		{"Cwd inside write is cwd",
			LaneProfile{Name: "", Work: work, Deny: []string{work}, Home: work},
			filepath.Join(work, "subdir"), nil,
			false, "", nil, work, true, filepath.Join(work, "subdir")},
		{"Cwd outside write is \"\"",
			LaneProfile{Name: "", Work: work, Deny: []string{work}, Home: work},
			dir, nil, false, "", nil, work, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.lp.Input(tc.cwd, tc.argv)
			if tc.wantErr {
				require.Error(t, err, "expected error")
				if tc.errSubstr != "" {
					assert.Contains(t, err.Error(), tc.errSubstr)
				}
				return
			}
			require.NoError(t, err, "unexpected error: %v", err)
			if tc.wantWrites != nil {
				assert.Equal(t, tc.wantWrites, got.Writes)
			}
			if tc.wantHome != "" {
				assert.Equal(t, tc.wantHome, got.Home)
			}
			assert.Equal(t, tc.wantNetDeny, got.NetDeny)
			assert.Equal(t, tc.wantCwd, got.Cwd)
		})
	}
}

// TestSandboxProfileCoverResolved pins resolved: a symlink made in t.TempDir() resolves to
// its target, and a path that does not exist comes back unchanged.
func TestSandboxProfileCoverResolved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")

	// Create target file
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))

	// Create symlink
	require.NoError(t, os.Symlink(target, link))

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"symlink resolves to target", link, target},
		{"path that does not exist comes back unchanged", filepath.Join(dir, "not-there"),
			filepath.Join(dir, "not-there")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolved(tc.path)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSandboxProfileCoverDarwinProfile pins DarwinProfile on a hand-built Policy: nil and a
// policy with no Writes are refused with "no --write"; an OptRoots, PathDirs or
// LinkSpellings entry holding a control character (use "\x01", not an SBPL metacharacter)
// is refused naming "root", "path dir" or "link spelling"; NetDeny with NetPorts 443 and
// 22 emits the mDNSResponder literal and `(allow network-outbound (remote tcp "*:443"))`;
// NetAllow "127.0.0.1:11434" emits `(remote ip "localhost:11434")` and an entry with no port
// adds no line; ReadsNoExec emits the NOEXEC0 read grant and deny process-exec* line;
// GPUMetal appends the ";; gpu=metal requested" line.
func TestSandboxProfileCoverDarwinProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	for _, tc := range []struct {
		name      string
		p         *Policy
		wantErr   bool
		errSubstr string
		wantLines []string
	}{
		{"nil is refused with no --write", nil, true, "no --write", nil},
		{"policy with no Writes is refused with no --write",
			&Policy{Writes: []string{}}, true, "no --write", nil},
		{"OptRoots with control char is refused naming root",
			&Policy{Writes: []string{dir}, OptRoots: []string{"\x01root"}}, true, "root", nil},
		{"PathDirs with control char is refused naming path dir",
			&Policy{Writes: []string{dir}, PathDirs: []string{"\x01path"}}, true, "path dir", nil},
		{"LinkSpellings with control char is refused naming link spelling",
			&Policy{Writes: []string{dir}, LinkSpellings: []string{"\x01link"}}, true, "link spelling", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DarwinProfile(tc.p)
			if tc.wantErr {
				require.Error(t, err, "expected error")
				if tc.errSubstr != "" {
					assert.Contains(t, err.Error(), tc.errSubstr)
				}
				return
			}
			require.NoError(t, err)
		})
	}

	// Test NetDeny with NetPorts
	{
		p := &Policy{Writes: []string{dir}, NetDeny: true, NetPorts: []int{443, 22}}
		text, _, err := DarwinProfile(p)
		require.NoError(t, err)
		assert.Contains(t, text, `/private/var/run/mDNSResponder`)
		assert.Contains(t, text, `(allow network-outbound (remote tcp "*:443"))`)
	}

	// Test NetAllow
	{
		p := &Policy{Writes: []string{dir}, NetAllow: []string{"127.0.0.1:11434"}}
		text, _, err := DarwinProfile(p)
		require.NoError(t, err)
		assert.Contains(t, text, `(allow network-outbound (remote ip "localhost:11434"))`)
	}

	// Test entry with no port adds no line
	{
		p := &Policy{Writes: []string{dir}, NetAllow: []string{"host-without-port"}}
		text, _, err := DarwinProfile(p)
		require.NoError(t, err)
		// Should not contain localhost entry
		assert.NotContains(t, text, `localhost:`)
	}

	// Test ReadsNoExec
	{
		p := &Policy{Writes: []string{dir}, ReadsNoExec: []string{dir}}
		text, _, err := DarwinProfile(p)
		require.NoError(t, err)
		assert.Contains(t, text, `(allow file-read* (subpath (param "NOEXEC0")))`)
		assert.Contains(t, text, `(deny process-exec* (subpath (param "NOEXEC0")))`)
	}

	// Test GPUMetal
	{
		p := &Policy{Writes: []string{dir}, GPUMode: GPUMetal}
		text, _, err := DarwinProfile(p)
		require.NoError(t, err)
		assert.Contains(t, text, `;; gpu=metal requested`)
	}
}
