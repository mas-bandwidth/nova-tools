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

func TestSprintFriendSyncUnitCoverFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		goos string
		want string
	}{
		{"darwin", FriendSyncLabel + ".plist"},
		{"linux", FriendSyncService},
		{"windows", ""},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, FriendSyncUnitFile(tc.goos))
		})
	}
}

func TestSprintFriendSyncUnitCoverArgs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		unit FriendSyncUnit
		want []string
	}{
		{
			name: "with PG and Root",
			unit: FriendSyncUnit{Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", PG: "postgres://u:p@h/d", Root: "/data/friends", Every: 15 * time.Second},
			want: []string{"/opt/nova/bin/nova-sprint", "friend", "sync", "--every", "15s", "--redis", "127.0.0.1:6381", "--pg", "postgres://u:p@h/d", "--root", "/data/friends"},
		},
		{
			name: "without PG and Root",
			unit: FriendSyncUnit{Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second},
			want: []string{"/opt/nova/bin/nova-sprint", "friend", "sync", "--every", "15s", "--redis", "127.0.0.1:6381"},
		},
		{
			name: "only PG",
			unit: FriendSyncUnit{Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", PG: "postgres://u:p@h/d", Every: 15 * time.Second},
			want: []string{"/opt/nova/bin/nova-sprint", "friend", "sync", "--every", "15s", "--redis", "127.0.0.1:6381", "--pg", "postgres://u:p@h/d"},
		},
		{
			name: "only Root",
			unit: FriendSyncUnit{Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Root: "/data/friends", Every: 15 * time.Second},
			want: []string{"/opt/nova/bin/nova-sprint", "friend", "sync", "--every", "15s", "--redis", "127.0.0.1:6381", "--root", "/data/friends"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.unit.Args())
		})
	}
}

func TestSprintFriendSyncUnitCoverRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		unit        FriendSyncUnit
		wantRefusal bool
	}{
		{
			name: "unknown OS",
			unit: FriendSyncUnit{OS: "windows", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second},
		},
		{
			name: "Every 0",
			unit: FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 0},
		},
		{
			name: "relative Exe",
			unit: FriendSyncUnit{OS: "darwin", Exe: "nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second},
		},
		{
			name: "empty Redis",
			unit: FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Every: 15 * time.Second},
		},
		{
			name: "mem: Redis",
			unit: FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "mem:sprint.twin", Every: 15 * time.Second},
		},
		{
			name: "relative Root",
			unit: FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Root: "friends", Every: 15 * time.Second},
		},
		{
			name: "invalid _ENV value",
			unit: FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second, Env: [][2]string{{"NOVA_PG_PASSWORD_ENV", "not-a-var-name"}}},
		},
		{
			name:        "non-ENV pair passes",
			unit:        FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second, Env: [][2]string{{"REDIS_PASS", "secret123"}}},
			wantRefusal: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			why := tc.unit.refusal()
			if tc.wantRefusal {
				assert.NotEmpty(t, why, "%s should refuse", tc.name)
			}
		})
	}
}

func TestSprintFriendSyncUnitCoverTextOS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		unit        FriendSyncUnit
		wantRefusal bool
	}{
		{
			name:        "unknown OS refused",
			unit:        FriendSyncUnit{OS: "windows", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second},
			wantRefusal: true,
		},
		{
			name:        "Every 0 refused",
			unit:        FriendSyncUnit{OS: "linux", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 0},
			wantRefusal: true,
		},
		{
			name:        "relative Exe refused",
			unit:        FriendSyncUnit{OS: "darwin", Exe: "nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second},
			wantRefusal: true,
		},
		{
			name:        "empty Redis refused",
			unit:        FriendSyncUnit{OS: "linux", Exe: "/opt/nova/bin/nova-sprint", Every: 15 * time.Second},
			wantRefusal: true,
		},
		{
			name:        "mem: Redis refused",
			unit:        FriendSyncUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "mem:sprint.twin", Every: 15 * time.Second},
			wantRefusal: true,
		},
		{
			name:        "relative Root refused",
			unit:        FriendSyncUnit{OS: "linux", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Root: "friends", Every: 15 * time.Second},
			wantRefusal: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.unit.Text()
			if tc.wantRefusal {
				assert.Error(t, err, "%s should refuse", tc.name)
			}
		})
	}
}

func TestSprintFriendSyncUnitCoverTextLinux(t *testing.T) {
	t.Parallel()
	u := FriendSyncUnit{
		OS:    "linux",
		Exe:   "/opt/nova/bin/nova-sprint",
		Redis: "127.0.0.1:6381",
		PG:    "postgres://u:p@h/d",
		Root:  "/data/friends",
		Every: 15 * time.Second,
		Env:   [][2]string{{"NOVA_PG_PASSWORD_ENV", "PG_PWD"}},
	}
	text, err := u.Text()
	require.NoError(t, err)
	assert.Contains(t, text, "[Unit]")
	assert.Contains(t, text, "ExecStart="+`"/opt/nova/bin/nova-sprint" "friend" "sync" "--every" "15s" "--redis" "127.0.0.1:6381" "--pg" "postgres://u:p@h/d" "--root" "/data/friends"`)
	assert.Contains(t, text, "Environment="+`"NOVA_PG_PASSWORD_ENV=PG_PWD"`)
}

func TestSprintFriendSyncUnitCoverTextDarwin(t *testing.T) {
	t.Parallel()
	u := FriendSyncUnit{
		OS:    "darwin",
		Exe:   "/opt/nova/bin/nova-sprint",
		Redis: "127.0.0.1:6381",
		Log:   filepath.Join("/tmp", "friend-sync.log"),
		Every: 15 * time.Second,
		Env:   [][2]string{{"NOVA_PG_PASSWORD_ENV", "PG_PWD"}},
	}
	text, err := u.Text()
	require.NoError(t, err)
	assert.Contains(t, text, `<key>Label</key>`)
	assert.Contains(t, text, `<string>`+FriendSyncLabel+`</string>`)
	assert.Contains(t, text, `<key>StandardOutPath</key>`)
	assert.Contains(t, text, u.Log)
}

func TestSprintFriendSyncUnitCoverInstallRefusal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	loaded := false
	in := SeatInstaller{
		Dir:    dir,
		Load:   func(string) error { loaded = true; return nil },
		Unload: func(string) error { return nil },
	}
	for name, u := range map[string]FriendSyncUnit{
		"windows":  {OS: "windows", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second},
		"relative": {OS: "darwin", Exe: "nova-sprint", Redis: "127.0.0.1:6381", Every: 15 * time.Second},
		"no redis": {OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Every: 15 * time.Second},
		"mem twin": {OS: "linux", Exe: "/opt/nova/bin/nova-sprint", Redis: "mem:sprint.twin", Every: 15 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := in.InstallFriendSync(u)
			assert.Error(t, err, name)
		})
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused install writes nothing")
	assert.False(t, loaded, "a refused install loads nothing")
}

func TestSprintFriendSyncUnitCoverInstallWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var loadCalls []string
	in := SeatInstaller{
		Dir:    dir,
		Load:   func(p string) error { loadCalls = append(loadCalls, p); return nil },
		Unload: func(string) error { return nil },
	}
	u := FriendSyncUnit{
		OS:    "linux",
		Exe:   "/opt/nova/bin/nova-sprint",
		Redis: "127.0.0.1:6381",
		Every: 15 * time.Second,
	}
	r, err := in.InstallFriendSync(u)
	require.NoError(t, err)
	assert.True(t, r.Changed, "first install should write")
	path := filepath.Join(dir, FriendSyncService)
	assert.Equal(t, path, r.Path)
	assert.FileExists(t, path)
	assert.Equal(t, []string{path}, loadCalls, "install loads the unit it wrote")

	// same text again
	loadCalls = nil
	r, err = in.InstallFriendSync(u)
	require.NoError(t, err)
	assert.False(t, r.Changed, "same text should not change")
	assert.Equal(t, []string{path}, loadCalls, "install loads the unit even when unchanged")
}

func TestSprintFriendSyncUnitCoverInstallLoadError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := SeatInstaller{
		Dir:    dir,
		Load:   func(string) error { return errors.New("unit did not load") },
		Unload: func(string) error { return nil },
	}
	u := FriendSyncUnit{
		OS:    "darwin",
		Exe:   "/opt/nova/bin/nova-sprint",
		Redis: "127.0.0.1:6381",
		Every: 15 * time.Second,
	}
	_, err := in.InstallFriendSync(u)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unit did not load")
	assert.FileExists(t, filepath.Join(dir, FriendSyncLabel+".plist"), "unit stays on load error")
}

func TestSprintFriendSyncUnitCoverUninstallUnknownOS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := SeatInstaller{Dir: dir, Load: func(string) error { return nil }, Unload: func(string) error { return nil }}
	_, err := in.UninstallFriendSync("windows")
	assert.Error(t, err)
}

func TestSprintFriendSyncUnitCoverUninstallNoFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var unloadCalls []string
	in := SeatInstaller{
		Dir:    dir,
		Load:   func(string) error { return nil },
		Unload: func(p string) error { unloadCalls = append(unloadCalls, p); return nil },
	}
	r, err := in.UninstallFriendSync("linux")
	require.NoError(t, err)
	assert.False(t, r.Changed, "no file should not change")
	assert.Empty(t, unloadCalls, "unload not called when no file")
}

func TestSprintFriendSyncUnitCoverUninstallWithFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, FriendSyncService)
	require.NoError(t, os.WriteFile(path, []byte("unit"), 0o644))
	var unloadCalls []string
	in := SeatInstaller{
		Dir:    dir,
		Load:   func(string) error { return nil },
		Unload: func(p string) error { unloadCalls = append(unloadCalls, p); return nil },
	}
	r, err := in.UninstallFriendSync("linux")
	require.NoError(t, err)
	assert.True(t, r.Changed, "file removal should change")
	assert.Equal(t, []string{path}, unloadCalls, "unload called before removal")
	assert.NoFileExists(t, path)
}

func TestSprintFriendSyncUnitCoverUninstallLoadError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, FriendSyncService)
	require.NoError(t, os.WriteFile(path, []byte("unit"), 0o644))
	in := SeatInstaller{
		Dir:    dir,
		Load:   func(string) error { return nil },
		Unload: func(string) error { return errors.New("unit did not unload") },
	}
	_, err := in.UninstallFriendSync("linux")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unit did not unload")
	assert.FileExists(t, path, "file kept on unload error")
}
