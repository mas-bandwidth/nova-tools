package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// friend sync --every is the coordinator's hand-typed zsh loop made the verb's own
// (the owner, 2026-10-05: "We need to get away from these one shot shell scripts"):
// that loop read the seat with where --json | jq .coordinator each 15 s, ran friend
// sync --actor <seat>, and printed a line only when it changed between failing and
// ok. Here, on the twin and an injected clock: three passes with the seat moved
// between them, each pass acting as the seat it finds (an actor that is not the seat
// is refused a coordinator's verb, so a pass that did not follow the seat fails);
// then fail, fail, ok, said as one FAILING line and one OK again line. A loop pinned
// to --actor does not follow, and its pass after the seat moves is refused. friend
// sync install --every writes the loop's unit naming this binary, the verb with
// --every and the store it was typed with, and no actor; uninstall removes it.
func TestFriendSyncEveryFollowsTheSeatAndSaysEachChangeOnce(t *testing.T) {
	t.Parallel()

	ta, cfg := friendApp(t, "amy")
	prev := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "NOVA_SPRINT_ACTOR" {
			return "" // as the unit runs it: no actor, each pass acts as the seat
		}
		return prev(k)
	}
	readable := ta.a.friends
	unreadable := func(context.Context, string) ([]config.Row, error) { return nil, errors.New("connection refused") }
	var seats []string // the seat each pass found, read where the pass reads its config
	ta.a.friends = func(ctx context.Context, pg string) ([]config.Row, error) {
		seats = append(seats, ta.seatState().Holder)
		return readable(ctx, pg)
	}
	following := ta.a.friends
	stopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	ta.a.notify = func(context.Context) (context.Context, context.CancelFunc) { return stopCtx, stop }
	var waits []time.Duration
	ta.a.after = func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		switch len(waits) {
		case 1:
			ta.ok("coordinator rowan --reason handover --actor coordinator")
			addFriendRow(t, cfg, "bob")
		case 2:
			ta.ok("coordinator stella --reason handover --actor rowan")
			addFriendRow(t, cfg, "cat")
		case 3:
			ta.a.friends = unreadable // the next two passes fail
		case 5:
			ta.a.friends = following // the next pass is ok again
			addFriendRow(t, cfg, "dan")
		case 6:
			stop()
		}
		ta.mu.Lock()
		ta.now = ta.now.Add(d)
		ta.mu.Unlock()
		fired := make(chan time.Time, 1)
		fired <- ta.a.now()
		return fired
	}
	var out, errs bytes.Buffer
	code := ta.a.run([]string{"friend", "sync", "--every", "15s", "--root", t.TempDir()}, &out, &errs)
	require.Equal(t, 0, code, "an interrupt ends the loop: %s", errs.String())
	assert.Equal(t, []time.Duration{15 * time.Second, 15 * time.Second, 15 * time.Second, 15 * time.Second, 15 * time.Second, 15 * time.Second}, waits, "it waits --every between passes, on the injected clock")
	assert.Equal(t, []string{"coordinator", "rowan", "stella", "stella"}, seats, "each pass that read its config acted as the seat then")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 6, "three passes that followed the seat, the pass ok again, the stop: %q", out.String())
	assert.Contains(t, lines[0], "FRIEND-SYNC OK added=amy")
	assert.Contains(t, lines[1], "FRIEND-SYNC OK added=bob", "the pass after the seat moved to rowan acted as rowan")
	assert.Contains(t, lines[2], "FRIEND-SYNC OK added=cat", "the pass after the seat moved to stella acted as stella")
	assert.Contains(t, lines[3], "FRIEND-SYNC OK added=dan")
	assert.Contains(t, lines[4], "FRIEND-SYNC OK again after 2 failing passes", "the recovery is said once")
	assert.Contains(t, lines[5], "FRIEND-SYNC STOP interrupted")
	assert.Equal(t, 1, strings.Count(errs.String(), "FRIEND-SYNC FAILING"), "fail, fail is one line, not one each pass: %s", errs.String())
	assert.Contains(t, errs.String(), "the config cannot be read")

	// a loop pinned to an actor does not follow the seat: its pass after the move is refused
	ta.ok("coordinator coordinator --reason back --actor stella")
	stopCtx, stop = context.WithCancel(context.Background())
	defer stop()
	waits = nil
	ta.a.after = func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		if len(waits) == 1 {
			ta.ok("coordinator rowan --reason handover --actor coordinator")
		} else {
			stop()
		}
		fired := make(chan time.Time, 1)
		fired <- ta.a.now()
		return fired
	}
	out.Reset()
	errs.Reset()
	code = ta.a.run([]string{"friend", "sync", "--every", "15s", "--actor", "coordinator", "--root", t.TempDir()}, &out, &errs)
	require.Equal(t, 0, code, errs.String())
	assert.Contains(t, errs.String(), "FRIEND-SYNC FAILING")
	assert.Contains(t, errs.String(), "the coordinator's alone: rowan, not coordinator")

	// install writes the loop's unit: this binary, the verb with --every, the stores
	// it was typed with and the variables that name their passwords, and no actor
	home := t.TempDir()
	ta.a.goos = "linux"
	ta.a.home = func() (string, error) { return home, nil }
	ta.a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
	env := map[string]string{"XDG_CONFIG_HOME": filepath.Join(home, ".config"), "NOVA_SPRINT_REDIS_USER": "coordinator", "NOVA_SPRINT_REDIS_PASSWORD_ENV": "NOVA_REDIS_COORDINATOR_PASSWORD", "NOVA_REDIS_COORDINATOR_PASSWORD": "s3cret"}
	before := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return before(k)
	}
	var calls []string
	ta.a.seatLoad = func(goos, op, path string) error { calls = append(calls, goos+" "+op+" "+path); return nil }
	unit := filepath.Join(home, ".config", "systemd", "user", sprint.FriendSyncService)
	pg := "postgres://nova_config@127.0.0.1:5432/nova"

	code, o, e := ta.do("friend sync install --every 15s --redis 127.0.0.1:6380 --pg " + pg + " --dry-run")
	require.Equal(t, 0, code, e)
	assert.Contains(t, o, "FRIEND-SYNC INSTALL DRY-RUN unit="+unit)
	assert.NoFileExists(t, unit, "a dry run writes nothing")
	assert.Empty(t, calls, "a dry run loads nothing")

	code, o, e = ta.do("friend sync install --every 15s --redis 127.0.0.1:6380 --pg " + pg)
	require.Equal(t, 0, code, e)
	assert.Contains(t, o, "FRIEND-SYNC INSTALL OK unit="+unit+" written=true loaded=true")
	assert.Contains(t, o, "runs: /opt/nova/bin/nova-sprint friend sync --every 15s --redis 127.0.0.1:6380 --pg "+pg)
	b, err := os.ReadFile(unit)
	require.NoError(t, err)
	text := string(b)
	assert.Contains(t, text, `ExecStart="/opt/nova/bin/nova-sprint" "friend" "sync" "--every" "15s" "--redis" "127.0.0.1:6380" "--pg" "`+pg+`"`)
	assert.Contains(t, text, `Environment="NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_COORDINATOR_PASSWORD"`)
	assert.Contains(t, text, `Environment="NOVA_SPRINT_REDIS_USER=coordinator"`)
	assert.NotContains(t, text, "--actor", "the loop acts as the seat each pass")
	assert.NotContains(t, text, "s3cret", "the unit carries no secret")
	assert.Equal(t, []string{"linux load " + unit}, calls)

	code, o, e = ta.do("friend sync install --every 15s --redis 127.0.0.1:6380 --pg " + pg)
	require.Equal(t, 0, code, e)
	assert.Contains(t, o, "written=false loaded=true", "the same unit is kept and loaded again")

	// macOS: a launchd agent, its lines to the user's Logs
	ta.a.goos = "darwin"
	agents := t.TempDir()
	code, _, e = ta.do("friend sync install --every 30s --redis 127.0.0.1:6380 --pg " + pg + " --dir " + agents)
	require.Equal(t, 0, code, e)
	b, err = os.ReadFile(filepath.Join(agents, sprint.FriendSyncLabel+".plist"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "<string>friend</string>\n\t\t<string>sync</string>\n\t\t<string>--every</string>\n\t\t<string>30s</string>")
	assert.Contains(t, string(b), filepath.Join(home, "Library", "Logs", "nova-sprint-friend-sync.log"))
	ta.a.goos = "linux"

	// refused, nothing written: no --every, an --actor, the twin, a --pg with its password
	for line, want := range map[string]string{
		"friend sync install --redis 127.0.0.1:6380 --pg " + pg:                                     "--every",
		"friend sync install --every 15s --redis 127.0.0.1:6380 --pg " + pg + " --actor rowan":      "leave out --actor",
		"friend sync install --every 15s --redis mem:0 --pg " + pg:                                  "in-memory twin",
		"friend sync install --every 15s --redis 127.0.0.1:6380 --pg postgres://u:hunter2@h:5432/n": "--pg carries a password",
	} {
		code, _, e := ta.do(line + " --dir " + filepath.Join(home, "refused"))
		assert.Equal(t, 2, code, line)
		assert.Contains(t, e, want, line)
		assert.NotContains(t, e, "hunter2", line)
		assert.NoDirExists(t, filepath.Join(home, "refused"), line)
	}

	// uninstall unloads it and removes it; again there is nothing to remove
	code, o, e = ta.do("friend sync uninstall")
	require.Equal(t, 0, code, e)
	assert.Contains(t, o, "FRIEND-SYNC UNINSTALL OK unit="+unit+" removed=true")
	assert.NoFileExists(t, unit)
	assert.Equal(t, []string{"linux load " + unit, "linux load " + unit, "darwin load " + filepath.Join(agents, sprint.FriendSyncLabel+".plist"), "linux unload " + unit}, calls)
	code, o, e = ta.do("friend sync uninstall")
	require.Equal(t, 0, code, e)
	assert.Contains(t, o, "removed=false")

	// each verb's -h gives its flags, its exit codes and its effect
	code, h, _ := ta.do("friend sync install -h")
	assert.Equal(t, 0, code)
	assert.Contains(t, h, "-every")
	assert.Contains(t, h, "effect: local write")
	code, h, _ = ta.do("friend sync -h")
	assert.Equal(t, 0, code)
	assert.Contains(t, h, "friend sync install --every")
}
