package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// friendSyncInstall is the install line docs/FRIENDS.md gives for the friend
// sync loop, its flags by name: the loop row nova-config loop add writes.
func friendSyncInstall(t *testing.T) (name string, raw map[string]string) {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "FRIENDS.md"))
	require.NoError(t, err)
	var line string
	for l := range strings.SplitSeq(string(doc), "\n") {
		if strings.HasPrefix(l, "nova-config loop add friend-sync ") {
			line = l
		}
	}
	require.NotEmpty(t, line, "docs/FRIENDS.md gives the friend sync loop's install line")
	name = strings.Fields(line)[3]
	raw = map[string]string{}
	argv := regexp.MustCompile(`--argv '([^']*)'`).FindStringSubmatch(line)
	require.Len(t, argv, 2, "the install line's --argv")
	raw["argv"] = argv[1]
	for _, flag := range []string{"machine", "seat", "keys", "keepalive", "every"} {
		if m := regexp.MustCompile(`--` + flag + ` (\S+)`).FindStringSubmatch(strings.Replace(line, argv[0], "", 1)); m != nil {
			raw[flag] = m[1]
		}
	}
	return name, raw
}

// Friend sync is an installed verb loop, never a shell's (the owner,
// 2026-10-04: "Golang nova-tools and nova-sprint verbs only"; "Make the ping
// loop mechanical!!!!"; docs/FRIENDS.md, "The friend sync loop";
// docs/SPEC-FRIEND.md, "The beat comes from the daemon"). The install line of
// docs/FRIENDS.md is a loop row nova-config accepts, kept alive, whose argv
// runs nova-sprint friend sync --every with no shell anywhere in it. Run as
// that argv runs it, with no actor, on a fake clock: each pass acts as the
// sprint's coordinator seat, reopens the store and syncs the config as it is
// then; it waits --every between passes; a pass that changed something says
// its line, a pass with nothing to do says nothing, a failing pass is said
// once on stderr and the loop goes on, its recovery said once; an interrupt
// ends it with 0. The friends' beats are their nova-friend daemons' alone: the
// help names no shell loop beating for a friend.
func TestFriendSyncRunsAsAnInstalledLoopWithNoShell(t *testing.T) {
	t.Parallel()

	name, raw := friendSyncInstall(t)
	assert.Equal(t, "friend-sync", name)
	assert.Equal(t, "true", raw["keepalive"], "the loop is kept alive: the verb is the loop")
	assert.Empty(t, raw["every"], "the period is the verb's --every, not the row's")
	var argv []string
	require.NoError(t, json.Unmarshal([]byte(raw["argv"]), &argv))
	for _, w := range argv {
		assert.NotContains(t, []string{"sh", "bash", "zsh", "-c"}, filepath.Base(w), "the loop's argv runs no shell: %q", argv)
		assert.NotContains(t, w, "$", "no shell expands a word of the argv: %q", w)
	}
	cfgRows := config.NewMem()
	ctx := context.Background()
	_, err := cfgRows.Insert(ctx, config.KindMachine, config.Row{Name: raw["machine"], Fields: map[string]string{"user": "u", "seat": raw["seat"], "slots": "4", "runners": "0"}}, "t")
	require.NoError(t, err)
	k, ok := config.Lookup(config.KindLoop)
	require.True(t, ok)
	row, err := k.NewRow(name, raw)
	require.NoError(t, err, "nova-config loop add takes the install line")
	_, err = cfgRows.Insert(ctx, config.KindLoop, row, "t")
	require.NoError(t, err)

	at := slices.Index(argv, "nova-sprint")
	require.Positive(t, at, "the argv runs nova-sprint under env, the password by the seat's key name")
	words := argv[at+1:]
	require.Equal(t, []string{"friend", "sync"}, words[:2])
	every := slices.Index(words, "--every")
	require.Positive(t, every, "the argv gives the loop's period")
	period, err := time.ParseDuration(words[every+1])
	require.NoError(t, err)

	ta, cfg := friendApp(t, "amy")
	prev := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "NOVA_SPRINT_ACTOR" {
			return "" // the loop row names no actor: each pass acts as the seat
		}
		return prev(k)
	}
	readable := ta.a.friends
	unreadable := func(context.Context, string) ([]config.Row, error) { return nil, errors.New("connection refused") }
	stopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	ta.a.notify = func(context.Context) (context.Context, context.CancelFunc) { return stopCtx, stop }
	var waits []time.Duration
	ta.a.after = func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		switch len(waits) {
		case 1:
			addFriendRow(t, cfg, "bob") // the next pass adds her
		case 2:
			ta.a.friends = unreadable // the next two passes fail
		case 4:
			ta.a.friends = readable // the next pass is ok again
		case 5:
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
	code := ta.a.run(words, &out, &errs)
	assert.Equal(t, 0, code, "an interrupt ends the loop: %s", errs.String())
	assert.Equal(t, []time.Duration{period, period, period, period, period}, waits, "it waits --every between passes")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 4, "pass 1 and 2 changed something, pass 5 recovered, then the stop: %q", out.String())
	assert.Contains(t, lines[0], "FRIEND-SYNC OK added=amy removed=- updated=- friends=1")
	assert.Contains(t, lines[1], "FRIEND-SYNC OK added=bob removed=- updated=- friends=2")
	assert.Contains(t, lines[2], "FRIEND-SYNC OK again")
	assert.Contains(t, lines[3], "FRIEND-SYNC STOP interrupted")
	assert.Equal(t, 1, strings.Count(errs.String(), "the config cannot be read"), "a failing pass is said once, not each pass: %s", errs.String())
	assert.Contains(t, errs.String(), "FRIEND-SYNC FAILING")
	assert.Equal(t, map[string]string{"amy": "down", "bob": "down"}, ta.friendStatus(), "the loop synced the config as it was each pass; no beat came from it")

	// a usage error ends the verb before any pass
	code, _, e := ta.do("friend sync --every -1s")
	assert.Equal(t, 2, code)
	assert.Contains(t, e, "--every")

	// its -h gives the flag and the loop's exit codes
	code, h, _ := ta.do("friend sync -h")
	assert.Equal(t, 0, code)
	assert.Contains(t, h, "-every")
	assert.Contains(t, h, "with --every: 0 interrupted")

	// the beat is the daemon's: the help names no shell loop beating for a friend
	for _, help := range []string{friendWords(), friendVerbWords("friend beat")} {
		assert.NotContains(t, help, "while :")
		assert.Contains(t, help, "nova-friend")
	}
}

// A friend sync loop failing past one minute is one judgment in the
// coordinator's inbox (sprint.NFriendSyncFailing), naming the refusal and its
// remedy, raised by the tick on the state the loop records; the first ok pass
// clears the state and the next tick closes the judgment. On 2026-10-08 the
// loop refused for 7h45m ("schema config is at version 35 and this binary
// carries 36; run: nova-config migrate") and told no one.
func TestFriendSyncFailingPastAMinuteIsOneJudgmentThatClosesWhenAPassIsOK(t *testing.T) {
	t.Parallel()

	ta, _ := friendApp(t, "amy")
	prev := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "NOVA_SPRINT_ACTOR" {
			return ""
		}
		return prev(k)
	}
	readable := ta.a.friends
	refusal := errors.New("schema config is at version 35 and this binary carries 36; run: nova-config migrate")
	ta.a.friends = func(context.Context, string) ([]config.Row, error) { return nil, refusal }
	stopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	ta.a.notify = func(context.Context) (context.Context, context.CancelFunc) { return stopCtx, stop }
	passes := 0
	ta.a.after = func(d time.Duration) <-chan time.Time {
		passes++
		if passes == 6 { // six failing passes, 75 s: past the minute
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
	code := ta.a.run([]string{"friend", "sync", "--every", "15s"}, &out, &errs)
	require.Equal(t, 0, code, errs.String())
	assert.Equal(t, 1, strings.Count(out.String(), "FRIEND-SYNC JUDGMENT recorded"), "recorded once, not every pass: %s", out.String())
	assert.Equal(t, 1, strings.Count(errs.String(), "FRIEND-SYNC FAILING"), errs.String())

	ta.ok("start --actor coordinator") // the machine RUNNING: its tick raises the judgment on the record
	ta.ok("tick")
	g := ta.group(sprint.NFriendSyncFailing, "")
	assert.Equal(t, 1, g.Count, "one judgment")
	assert.Contains(t, g.What, "schema config is at version 35 and this binary carries 36; run: nova-config migrate", "it names the refusal and its remedy")
	assert.Contains(t, g.What, "no friend card is delivered or collected")
	ta.ok("tick")
	n := 0
	for _, g := range ta.inboxGroups() {
		if g.Type == sprint.NFriendSyncFailing {
			n += g.Count
		}
	}
	assert.Equal(t, 1, n, "a later tick raises no second judgment while the failure stands")

	// a new run of the loop (the binary replaced, say) whose first pass is ok clears it
	ta.a.friends = readable
	stopCtx, stop = context.WithCancel(context.Background())
	ta.a.notify = func(context.Context) (context.Context, context.CancelFunc) { return stopCtx, stop }
	passes = 0
	ta.a.after = func(d time.Duration) <-chan time.Time {
		passes++
		stop()
		fired := make(chan time.Time, 1)
		fired <- ta.a.now()
		return fired
	}
	out.Reset()
	errs.Reset()
	code = ta.a.run([]string{"friend", "sync", "--every", "15s"}, &out, &errs)
	require.Equal(t, 0, code, errs.String())
	ta.ok("tick")
	for _, g := range ta.inboxGroups() {
		if g.Kind == sprint.Judgment {
			assert.NotEqual(t, sprint.NFriendSyncFailing, g.Type, "the judgment closed when a pass was ok: %+v", g)
		}
	}
	assert.Equal(t, 1, ta.group(sprint.NFriendSyncFailing, "").Count, "its answer stands in the inbox: closed by the tick, not the coordinator")
}
