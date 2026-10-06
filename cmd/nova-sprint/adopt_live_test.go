package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// livePlist is a launchd plist with these ProgramArguments.
func livePlist(args ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>ProgramArguments</key><array>`)
	for _, a := range args {
		fmt.Fprintf(&b, "<string>%s</string>", a)
	}
	b.WriteString(`</array><key>KeepAlive</key><true/></dict></plist>`)
	return b.String()
}

func liveInode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	require.NoError(t, err)
	return uint64(fi.Sys().(*syscall.Stat_t).Ino)
}

// TestLiveShowsWhatIsInstalled: live reads a fake host (a home with three
// agents, a bin directory, a dashboard link, and the commands it runs
// answered by a fake) and says the half moves: a server whose process runs a
// binary the install replaced, a friend daemon whose plist names a copy, a
// store library that is not the build's, a dashboard link to another binary;
// an agent that runs no nova tool is listed and never stale.
func TestLiveShowsWhatIsInstalled(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	agents := filepath.Join(home, "Library", "LaunchAgents")
	copyDir := filepath.Join(home, "copy")
	for _, d := range []string{bin, agents, copyDir, filepath.Join(home, "dash")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	for _, f := range []string{filepath.Join(bin, "nova-sprint"), filepath.Join(bin, "nova-friend"), filepath.Join(bin, "nova-redis"), filepath.Join(copyDir, "nova-friend")} {
		require.NoError(t, os.WriteFile(f, []byte(f), 0o755))
	}
	server := []string{filepath.Join(bin, "nova-sprint"), "run", "--listen", ":6390", "--tick-deadline", "60s"}
	plists := map[string]string{
		"com.nova.loop.sprint-server-a": livePlist(append([]string{filepath.Join(bin, "nova-secrets"), "exec", "--as", "seat-a", "--only", "K", "--", "/usr/bin/env", "A=B"}, server...)...),
		"com.nova.friend-a":             livePlist(filepath.Join(copyDir, "nova-friend"), "run", "--as", "friend-a", "--harness", "opencode", "--dir", "/d", "--width", "2"),
		"com.nova.redis":                livePlist("/opt/homebrew/bin/redis-server", "/x/nova-redis.conf"),
	}
	for label, body := range plists {
		require.NoError(t, os.WriteFile(filepath.Join(agents, label+".plist"), []byte(body), 0o644))
	}
	// a backup beside them is not an agent
	require.NoError(t, os.WriteFile(filepath.Join(agents, "com.nova.redis.plist.1~"), []byte(plists["com.nova.redis"]), 0o644))
	link := filepath.Join(home, "dash", "nova-sprint-int")
	require.NoError(t, os.Symlink(filepath.Join(home, "old", "nova-sprint"), link))

	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	uid := os.Getuid()
	friendIno := liveInode(t, filepath.Join(copyDir, "nova-friend"))
	answers := map[string]string{
		bin + "/nova-sprint version": "nova-sprint v1.2.0-dev.abcdef1 darwin/arm64 go1.27.1",
		fmt.Sprintf("launchctl print gui/%d/com.nova.loop.sprint-server-a", uid): "state = running\n\tpid = 11\n",
		fmt.Sprintf("launchctl print gui/%d/com.nova.friend-a", uid):             "\tpid = 12\n",
		"ps -o args= -p 11":                                strings.Join(server, " "),
		"lsof -a -p 11 -d txt -F i":                        "p11\nftxt\ni1\n",
		"ps -o args= -p 12":                                filepath.Join(copyDir, "nova-friend") + " run --as friend-a --harness opencode --dir /d --width 2",
		"lsof -a -p 12 -d txt -F i":                        fmt.Sprintf("p12\nftxt\ni%d\n", friendIno),
		bin + "/nova-friend status --as friend-a --dir /d": "STATUS OK daemon=up last_beat=2026-10-06T14:59:30Z",
	}
	var ran []string
	fake := adoptRunner(func(_ context.Context, name string, args ...string) (string, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		ran = append(ran, line)
		if strings.HasPrefix(line, bin+"/nova-redis fn check") {
			return "STALE nova_sprint sha=bbb loaded=aaa want=bbb store=s:1", errors.New("exit status 1")
		}
		if out, ok := answers[line]; ok {
			return out, nil
		}
		return "", errors.New("exit status 113")
	})
	env := map[string]string{"HOME": home, "NOVA_SPRINT_REDIS": "s:1", "NOVA_SPRINT_REDIS_USER": "coordinator", "NOVA_SPRINT_REDIS_PASSWORD_ENV": "PW"}
	a := newApp(func(k string) string { return env[k] })
	a.now = func() time.Time { return now }
	liveRunnerOf.Store(a, fake)
	t.Cleanup(func() { liveRunnerOf.Delete(a) })

	var out, errs bytes.Buffer
	require.Equal(t, 0, a.run([]string{"live", "--json", "--dashboard", link}, &out, &errs), errs.String())
	var m liveManifest
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	assert.Contains(t, ran, bin+"/nova-redis fn check --addr s:1 --user coordinator --password-env PW", "the library is read by the installed nova-redis, as the seat logs in")

	assert.Equal(t, filepath.Join(bin, "nova-sprint"), m.Server.Path)
	assert.Equal(t, liveInode(t, filepath.Join(bin, "nova-sprint")), m.Server.Inode)
	assert.Equal(t, "v1.2.0-dev.abcdef1", m.Server.Version)
	assert.Equal(t, "abcdef1", m.Server.Revision)
	assert.Equal(t, liveLibrary{State: "STALE", Loaded: "aaa", Want: "bbb"}, m.Library)
	require.Len(t, m.Dashboard, 1)
	assert.False(t, m.Dashboard[0].Current, "the link names another binary")

	require.Len(t, m.Agents, 3, "the three plists, never the backup")
	byLabel := map[string]liveAgent{}
	for _, ag := range m.Agents {
		byLabel[ag.Label] = ag
	}
	srv := byLabel["com.nova.loop.sprint-server-a"]
	assert.Equal(t, "server", srv.Role)
	assert.Equal(t, 11, srv.PID)
	assert.Equal(t, []string{"127.0.0.1:6390"}, srv.Listen)
	assert.True(t, srv.Installed)
	assert.True(t, srv.ArgsTook, "ps's arguments are the plist's from the tool on")
	assert.False(t, srv.Fresh, "the process runs inode 1, not the installed binary's")
	assert.True(t, srv.Stale)
	assert.Equal(t, fmt.Sprintf("gui/%d/com.nova.loop.sprint-server-a", uid), srv.Target)

	fa := byLabel["com.nova.friend-a"]
	assert.Equal(t, "friend", fa.Role)
	assert.Equal(t, "friend-a", fa.Friend)
	assert.True(t, fa.Fresh, "it runs the bytes its plist names")
	assert.False(t, fa.Installed, "but they are a copy, not the bin directory's tool")
	assert.True(t, fa.Stale)
	assert.Equal(t, 30, fa.BeatAge)
	assert.Equal(t, []string{"install", "--as", "friend-a", "--harness", "opencode", "--dir", "/d", "--width", "2"}, fa.Install)

	redis := byLabel["com.nova.redis"]
	assert.Empty(t, redis.Tool, "a .conf named nova-* is no nova tool")
	assert.False(t, redis.Stale)

	out.Reset()
	require.Equal(t, 0, a.run([]string{"live", "--dashboard", link}, &out, &errs), errs.String())
	text := out.String()
	assert.Contains(t, text, "LIVE SERVER binary="+filepath.Join(bin, "nova-sprint"))
	assert.Contains(t, text, "LIVE LIBRARY state=STALE loaded=aaa want=bbb match=false")
	assert.Contains(t, text, "LIVE SERVER label=com.nova.loop.sprint-server-a pid=11 stale=true fresh=false installed=true args_took=true")
	assert.Contains(t, text, "LIVE FRIEND label=com.nova.friend-a pid=12 stale=true fresh=true installed=false")
	assert.Contains(t, text, "LIVE AGENT label=com.nova.redis pid=0 program=")
}
