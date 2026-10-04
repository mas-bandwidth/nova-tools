package sprint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seat install writes the push loop's unit, the loop being inbox --wait --push
// seat on the sprint's store, loads it, and uninstall unloads and removes it
// (docs/SPEC-SPRINT.md, "Handing over the seat"). The unit goes only into the
// directory the installer is given, and the loader is the test's: nothing is
// loaded on the machine running the test.
func TestSeatInstallInstallsThePushLoop(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		goos, file string
		want       []string
	}{
		{"darwin", SeatLabel + ".plist", []string{
			"<key>Label</key>\n\t<string>" + SeatLabel + "</string>",
			"<string>/opt/nova/bin/nova-sprint</string>\n\t\t<string>inbox</string>\n\t\t<string>--wait</string>\n\t\t<string>--push</string>\n\t\t<string>seat</string>\n\t\t<string>--redis</string>\n\t\t<string>127.0.0.1:6381</string>",
			"<key>KeepAlive</key>\n\t<true/>", "<key>RunAtLoad</key>\n\t<true/>",
		}},
		{"linux", SeatService, []string{
			`ExecStart="/opt/nova/bin/nova-sprint" "inbox" "--wait" "--push" "seat" "--redis" "127.0.0.1:6381"`,
			"Restart=always", "WantedBy=default.target",
		}},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			var calls []string
			in := SeatInstaller{Dir: dir,
				Load:   func(p string) error { calls = append(calls, "load "+p); return nil },
				Unload: func(p string) error { calls = append(calls, "unload "+p); return nil }}
			u := SeatUnit{OS: tc.goos, Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Log: filepath.Join(dir, "push.log")}
			path := filepath.Join(dir, tc.file)

			r, err := in.Install(u)
			require.NoError(t, err)
			assert.Equal(t, SeatResult{Path: path, Changed: true}, r)
			b, err := os.ReadFile(path)
			require.NoError(t, err)
			for _, w := range tc.want {
				assert.Contains(t, string(b), w, "the unit lacks %q:\n%s", w, b)
			}
			assert.Equal(t, []string{"load " + path}, calls, "install loads the unit it wrote")

			// again: the same unit is kept, and loaded again (a unit booted out by hand comes back)
			r, err = in.Install(u)
			require.NoError(t, err)
			assert.Equal(t, SeatResult{Path: path}, r)
			assert.Equal(t, []string{"load " + path, "load " + path}, calls)

			// another store: the unit is rewritten
			u.Redis = ""
			u.Server = "127.0.0.1:7480"
			r, err = in.Install(u)
			require.NoError(t, err)
			assert.True(t, r.Changed, "a unit for another store is rewritten")
			b, err = os.ReadFile(path)
			require.NoError(t, err)
			assert.NotContains(t, string(b), "--redis")
			assert.Contains(t, string(b), "NOVA_SPRINT_SERVER")
			assert.Contains(t, string(b), "127.0.0.1:7480")

			calls = nil
			r, err = in.Uninstall(tc.goos)
			require.NoError(t, err)
			assert.Equal(t, SeatResult{Path: path, Changed: true}, r)
			assert.NoFileExists(t, path)
			assert.Equal(t, []string{"unload " + path}, calls, "uninstall unloads the unit before it removes it")

			r, err = in.Uninstall(tc.goos)
			require.NoError(t, err)
			assert.Equal(t, SeatResult{Path: path}, r, "a second uninstall finds nothing and does nothing")
			assert.Equal(t, []string{"unload " + path}, calls)
		})
	}
	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		loaded := false
		in := SeatInstaller{Dir: dir, Load: func(string) error { loaded = true; return nil }, Unload: func(string) error { return nil }}
		ok := SeatUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381"}
		for name, u := range map[string]SeatUnit{
			"windows":  {OS: "windows", Exe: ok.Exe, Redis: ok.Redis},
			"relative": {OS: ok.OS, Exe: "nova-sprint", Redis: ok.Redis},
			"no store": {OS: ok.OS, Exe: ok.Exe},
			"twin":     {OS: ok.OS, Exe: ok.Exe, Redis: "mem:sprint.twin"},
		} {
			_, err := in.Install(u)
			assert.Error(t, err, name)
		}
		_, err := in.Uninstall("windows")
		assert.Error(t, err)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries, "a refused install writes nothing")
		assert.False(t, loaded, "a refused install loads nothing")

		// a load that fails is said, and the unit stays for the next install
		in.Load = func(string) error { return errors.New("bootstrap failed") }
		_, err = in.Install(ok)
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "bootstrap failed"), err.Error())
		assert.FileExists(t, filepath.Join(dir, SeatLabel+".plist"))
	})
}
