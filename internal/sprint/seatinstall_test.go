package sprint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
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
			"<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>NOVA_BUS_REDIS</key>\n\t\t<string>127.0.0.1:6390</string>",
		}},
		{"linux", SeatService, []string{
			`ExecStart="/opt/nova/bin/nova-sprint" "inbox" "--wait" "--push" "seat" "--redis" "127.0.0.1:6381"`,
			"Restart=always", "WantedBy=default.target", `Environment="NOVA_BUS_REDIS=127.0.0.1:6390"`,
		}},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			var calls []string
			in := SeatInstaller{Dir: dir,
				Load:   func(p string) error { calls = append(calls, "load "+p); return nil },
				Unload: func(p string) error { calls = append(calls, "unload "+p); return nil }}
			u := SeatUnit{OS: tc.goos, Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Bus: "127.0.0.1:6390", Log: filepath.Join(dir, "push.log")}
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
		ok := SeatUnit{OS: "darwin", Exe: "/opt/nova/bin/nova-sprint", Redis: "127.0.0.1:6381", Bus: "127.0.0.1:6390"}
		for name, u := range map[string]SeatUnit{
			"windows":  {OS: "windows", Exe: ok.Exe, Redis: ok.Redis, Bus: ok.Bus},
			"relative": {OS: ok.OS, Exe: "nova-sprint", Redis: ok.Redis, Bus: ok.Bus},
			"no store": {OS: ok.OS, Exe: ok.Exe, Bus: ok.Bus},
			"no bus":   {OS: ok.OS, Exe: ok.Exe, Redis: ok.Redis},
			"twin":     {OS: ok.OS, Exe: ok.Exe, Redis: "mem:sprint.twin", Bus: ok.Bus},
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
	// the loop's push reaches the seat over nova-bus: a pushed group is one message
	// from the holder to whose inbox its file went, on a fake bus store (no socket)
	t.Run("the push reaches the bus", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		b := &bus.Bus{Store: bus.NewFake(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), "rowan", "stella")}
		g := Group{ID: "n-12", Kind: Judgment, Type: NWorkFailed, Stream: "s1"}
		text := "JUDGMENT n-12 work-failed s1\n  tests red\nclock: 2030-01-02T03:04:05Z\n"
		file := "/home/rowan/rowan-working/inbox/sprint-judgments/n-12.md"
		p := SeatPushOf("rowan", "stella", g, []string{"n-12", "n-13"}, file, text)
		assert.Equal(t, SeatPush{From: "rowan", To: "stella", Subject: "sprint judgment n-12 in s1: work came back failed (2 new)",
			Body: "notes: n-12,n-13\nfile: " + file + "\n\n" + text}, p)
		_, err := b.Send(ctx, bus.Message{From: p.From, To: []string{p.To}, Subject: p.Subject, Body: p.Body})
		require.NoError(t, err)
		e, ok, err := b.Recv(ctx, "stella", 0)
		require.NoError(t, err)
		require.True(t, ok, "the inbox's owner has the message on her stream")
		m := e.Message()
		assert.Equal(t, []string{"stella"}, m.To)
		assert.Equal(t, "rowan", m.From)
		assert.Equal(t, p.Subject, m.Subject)
		assert.Equal(t, p.Body, m.Body)
		_, ok, err = b.Recv(ctx, "rowan", 0)
		require.NoError(t, err)
		assert.False(t, ok, "the holder is not sent another's note")

		// a group too long for one message is cut, and says where the whole is
		long := SeatPushOf("rowan", "rowan", g, []string{"n-12"}, file, strings.Repeat("x", 2*SeatPushMaxBody))
		assert.LessOrEqual(t, len(long.Body), SeatPushMaxBody+200)
		assert.True(t, strings.HasSuffix(long.Body, "\n... cut at "+strconv.Itoa(SeatPushMaxBody)+" bytes; the whole is "+file+"\n"), long.Body[len(long.Body)-120:])
		_, err = b.Send(ctx, bus.Message{From: long.From, To: []string{long.To}, Subject: long.Subject, Body: long.Body})
		require.NoError(t, err)
	})
}
