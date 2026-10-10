package sprint

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSprintFriendSyncUnitCoverFile tests FriendSyncUnitFile.
func TestSprintFriendSyncUnitCoverFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		goos string
		want string
	}{
		{"darwin", FriendSyncLabel + ".plist"},
		{"linux", FriendSyncService},
		{"windows", ""},
		{"freebsd", ""},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, FriendSyncUnitFile(tc.goos))
		})
	}
}

// TestSprintFriendSyncUnitCoverArgs tests Args.
func TestSprintFriendSyncUnitCoverArgs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		u    FriendSyncUnit
		want []string
	}{
		{"without PG and Root", FriendSyncUnit{Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second},
			[]string{"/opt/nova/bin/nova-sprint", "friend", "sync", "--every", "15s", "--redis", "127.0.0.1:6381"}},
		{"with PG", FriendSyncUnit{Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", PG: "user=test dbname=test", Every: 15 * time.Second},
			[]string{"/opt/nova/bin/nova-sprint", "friend", "sync", "--every", "15s", "--redis", "127.0.0.1:6381", "--pg", "user=test dbname=test"}},
		{"with Root", FriendSyncUnit{Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Root: "/home/test/friends", Every: 15 * time.Second},
			[]string{"/opt/nova/bin/nova-sprint", "friend", "sync", "--every", "15s", "--redis", "127.0.0.1:6381", "--root", "/home/test/friends"}},
		{"with PG and Root", FriendSyncUnit{Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", PG: "user=test dbname=test", Root: "/home/test/friends", Every: 15 * time.Second},
			[]string{"/opt/nova/bin/nova-sprint", "friend", "sync", "--every", "15s", "--redis", "127.0.0.1:6381", "--pg", "user=test dbname=test", "--root", "/home/test/friends"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.u.Args())
		})
	}
}

// TestSprintFriendSyncUnitCoverRefusal tests refusal.
func TestSprintFriendSyncUnitCoverRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		u    FriendSyncUnit
	}{
		{"windows", FriendSyncUnit{OS: "windows", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381"}},
		{"freebsd", FriendSyncUnit{OS: "freebsd", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381"}},
		{"every zero", FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 0}},
		{"every negative", FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: -1 * time.Second}},
		{"relative exe", FriendSyncUnit{OS: "darwin", Exe: "nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second}},
		{"empty redis", FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Every: 15 * time.Second}},
		{"mem redis", FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "mem:sprint.twin", Every: 15 * time.Second}},
		{"relative root", FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Root: "friends", Every: 15 * time.Second}},
		{"env pair with invalid", FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second, Env: [][2]string{{"NOVA_PG_PASSWORD_ENV", "123"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.NotEmpty(t, tc.u.refusal(), "refusal should be set for %s", tc.name)
		})
	}
	// Non-_ENV pairs pass
	u := FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second,
		Env: [][2]string{{"SOME_KEY", "any value"}}}
	assert.Empty(t, u.refusal())
}

// TestSprintFriendSyncUnitCoverText tests Text.
func TestSprintFriendSyncUnitCoverText(t *testing.T) {
	t.Parallel()
	// Linux
	uLinux := FriendSyncUnit{OS: "linux", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second}
	text, err := uLinux.Text()
	require.NoError(t, err)
	assert.Contains(t, text, "ExecStart")
	assert.Contains(t, text, "--every")
	assert.Contains(t, text, "--redis")

	// Darwin
	uDarwin := FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Log: filepath.Join(os.TempDir(), "test.log"), Every: 15 * time.Second}
	text, err = uDarwin.Text()
	require.NoError(t, err)
	assert.Contains(t, text, FriendSyncLabel)
	assert.Contains(t, text, "--redis")
	assert.Contains(t, text, uDarwin.Log)
}

// TestSprintFriendSyncUnitCoverInstallFriendSync tests InstallFriendSync.
func TestSprintFriendSyncUnitCoverInstallFriendSync(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) (dir string, in SeatInstaller, u FriendSyncUnit)
	}{
		{
			"refused",
			func(t *testing.T) (string, SeatInstaller, FriendSyncUnit) {
				dir := t.TempDir()
				in := SeatInstaller{Dir: dir}
				u := FriendSyncUnit{OS: "windows"}
				return dir, in, u
			},
		},
		{
			"writing",
			func(t *testing.T) (string, SeatInstaller, FriendSyncUnit) {
				dir := t.TempDir()
				in := SeatInstaller{Dir: dir, Load: func(string) error { return nil }}
				u := FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second}
				return dir, in, u
			},
		},
		{
			"same text",
			func(t *testing.T) (string, SeatInstaller, FriendSyncUnit) {
				dir := t.TempDir()
				callCount := 0
				in := SeatInstaller{Dir: dir, Load: func(p string) error { callCount++; return nil }}
				u := FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second}
				// First install
				_, err := in.InstallFriendSync(u)
				require.NoError(t, err)
				return dir, in, u
			},
		},
		{
			"load error",
			func(t *testing.T) (string, SeatInstaller, FriendSyncUnit) {
				dir := t.TempDir()
				in := SeatInstaller{Dir: dir, Load: func(string) error { return errors.New("unit did not load") }}
				u := FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second}
				return dir, in, u
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, in, u := tc.setup(t)

			r, err := in.InstallFriendSync(u)

			switch tc.name {
			case "refused":
				assert.Error(t, err)
				entries, _ := os.ReadDir(dir)
				assert.Empty(t, entries)
			case "writing":
				require.NoError(t, err)
				assert.True(t, r.Changed)
				assert.FileExists(t, r.Path)
			case "same text":
				require.NoError(t, err)
				assert.False(t, r.Changed)
			case "load error":
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unit did not load")
			}
		})
	}
}

// TestSprintFriendSyncUnitCoverUninstallFriendSync tests UninstallFriendSync.
func TestSprintFriendSyncUnitCoverUninstallFriendSync(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) (dir string, in SeatInstaller, goos string)
	}{
		{
			"unknown os",
			func(t *testing.T) (string, SeatInstaller, string) {
				dir := t.TempDir()
				return dir, SeatInstaller{Dir: dir}, "windows"
			},
		},
		{
			"no file",
			func(t *testing.T) (string, SeatInstaller, string) {
				dir := t.TempDir()
				return dir, SeatInstaller{Dir: dir, Unload: func(string) error { return nil }}, "darwin"
			},
		},
		{
			"with file",
			func(t *testing.T) (string, SeatInstaller, string) {
				dir := t.TempDir()
				path := filepath.Join(dir, FriendSyncUnitFile("darwin"))
				require.NoError(t, os.WriteFile(path, []byte("test"), 0644))
				return dir, SeatInstaller{Dir: dir, Unload: func(string) error { return nil }}, "darwin"
			},
		},
		{
			"unload error",
			func(t *testing.T) (string, SeatInstaller, string) {
				dir := t.TempDir()
				path := filepath.Join(dir, FriendSyncUnitFile("darwin"))
				require.NoError(t, os.WriteFile(path, []byte("test"), 0644))
				return dir, SeatInstaller{Dir: dir, Unload: func(string) error { return errors.New("unload failed") }}, "darwin"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, in, goos := tc.setup(t)

			r, err := in.UninstallFriendSync(goos)

			switch tc.name {
			case "unknown os":
				assert.Error(t, err)
			case "no file":
				require.NoError(t, err)
				assert.False(t, r.Changed)
				assert.NoFileExists(t, filepath.Join(dir, FriendSyncUnitFile(goos)))
			case "with file":
				require.NoError(t, err)
				assert.True(t, r.Changed)
				assert.NoFileExists(t, r.Path)
			case "unload error":
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unload failed")
				assert.FileExists(t, r.Path)
			}
		})
	}
}
