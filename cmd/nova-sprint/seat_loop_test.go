package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// seatPushInstall is the install line docs/FRIENDS.md gives for the seat
// push loop, its flags by name: the loop row nova-config loop add writes.
func seatPushInstall(t *testing.T) (name string, raw map[string]string) {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "FRIENDS.md"))
	require.NoError(t, err)
	var line string
	for _, l := range strings.Split(string(doc), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "nova-config loop add seat-push ") {
			line = l
			break
		}
	}
	require.NotEmpty(t, line, "docs/FRIENDS.md gives the seat push loop's install line")
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

// The seat push runs as an installed loop row, never a shell's (the owner,
// 2026-10-04: "no bash scripts"; "anything you rely on that is a bespoke tool as
// coordinator, that has to, absolutely go"; docs/FRIENDS.md, "The coordinator's
// loops"; docs/SPEC-SPRINT.md, "Handing over the seat"). The install line of
// docs/FRIENDS.md is a loop row nova-config accepts, kept alive, whose argv
// runs nova-sprint inbox --wait --push seat with no shell anywhere in it.
// When run as that argv runs it, on a fake clock: each open judgment and note to
// the coordinator is pushed to the holder's inbox directory, the push follows
// the seat when it moves, and an interrupt ends the loop with 0.
func TestTheSeatPushRunsAsAnInstalledLoop(t *testing.T) {
	t.Parallel()

	name, raw := seatPushInstall(t)
	assert.Equal(t, "seat-push", name)
	assert.Equal(t, "true", raw["keepalive"], "the seat push loop is kept alive: the verb is the loop")
	assert.Empty(t, raw["every"], "the push loop is kept alive, not periodic")
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
	require.Positive(t, at, "the argv runs nova-sprint")
	words := argv[at+1:]
	require.Equal(t, []string{"inbox", "--wait", "--push", "seat"}, words)

	ta, held := heldAndWaiting(t)
	home := t.TempDir()
	ta.a.home = func() (string, error) { return home, nil }
	for _, who := range []string{"coordinator", "rowan"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, who+"-working", "inbox"), 0o755))
	}
	ta.ok("init --owner glenn")
	in := ta.interruptible()
	ta.atSleep(func(n int) {
		switch n {
		case 3:
			ta.ok("coordinator rowan --take --approved-by glenn --reason 'coordinator is asleep' --actor rowan")
			ta.ok("tick")
		case 7:
			in.now(t)
		}
	})

	var out, errs bytes.Buffer
	code := ta.a.run(words, &out, &errs)
	assert.Equal(t, 0, code, "an interrupt ends the push loop: %s", errs.String())

	old := filepath.Join(home, "coordinator-working", "inbox", "sprint-judgments")
	next := filepath.Join(home, "rowan-working", "inbox", "sprint-judgments")
	assert.FileExists(t, filepath.Join(old, held.Notes[0]+".md"), out.String())
	assert.FileExists(t, filepath.Join(next, held.Notes[0]+".md"), "the open judgment follows the seat:\n%s", out.String())
	assert.Contains(t, out.String(), "NOTE the seat is rowan's: pushing to "+next+"\n", out.String())

	var takenNote string
	for _, g := range ta.inboxGroups() {
		if g.Type == sprint.NSeatTaken {
			takenNote = g.Notes[0]
		}
	}
	require.NotEmpty(t, takenNote)
	assert.FileExists(t, filepath.Join(old, takenNote+".md"), "the taken note is the old holder's:\n%s", out.String())
	assert.NoFileExists(t, filepath.Join(next, takenNote+".md"), out.String())
}
