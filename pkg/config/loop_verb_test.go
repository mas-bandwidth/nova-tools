package config

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A loop whose verb is gone (loop.go): each nova program a loop's argv runs is
// asked whether it still has the verb; DeadLoops names a dead one with its
// remove for status, and CheckLoopVerb refuses one at add and set.

// fakeVerbs is a probe over the verbs each installed program has; a program it
// does not know is one not installed here, which judges nothing.
func fakeVerbs(have map[string][]string) VerbProbe {
	return func(_ context.Context, program, verb string) (bool, error) {
		verbs, ok := have[program[strings.LastIndex(program, "/")+1:]]
		if !ok {
			return false, errors.New(program + ": not installed here")
		}
		return !slices.Contains(verbs, verb), nil
	}
}

// the coordinator machine's loop of 2026-10-04: nova-sprint table under nova-loop and nova-secrets exec
var tableLive = `["/Users/u/.local/bin/nova-loop","sprint-table-live","nova-secrets","exec","--as","s","--only","K","--","/usr/local/bin/nova-sprint","table","--layout","live","--loop","1"]`

var installed = map[string][]string{"nova-sprint": {"where", "run", "friend", "inbox"}, "nova-secrets": {"exec"}, "nova-swarm": {"member", "disk-guard"}}

func TestALoopWhoseVerbIsGoneIsFlagged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	probe := fakeVerbs(installed)

	assert.Equal(t, []LoopCall{{"nova-secrets", "exec"}, {"/usr/local/bin/nova-sprint", "table"}}, LoopCalls(Argv(tableLive)),
		"the wrapper nova-loop is never asked; each nova program and its verb is")
	assert.Equal(t, []LoopCall{{"nova-swarm", "member"}}, LoopCalls([]string{"/usr/bin/env", "A=b", "nova-swarm", "member", "--as", "m1", "--harness", "/x/nova-friend"}),
		"a path after a program is no verb")
	assert.Empty(t, LoopCalls([]string{"nova-sprint", "--version"}))
	assert.Empty(t, LoopCalls([]string{"nova-sprint", "help", "where"}), "help is every tool's")

	r := newLoopRig(t, "m1")
	r.add(t, "sprint-table-live", map[string]string{"machine": "m1", "argv": tableLive, "keepalive": "true", "seat": "s", "keys": "K"})
	r.add(t, "member-m1", map[string]string{"machine": "m1", "argv": `["nova-swarm","member","--as","m1"]`, "keepalive": "true"})
	r.add(t, "elsewhere", map[string]string{"machine": "m1", "argv": `["nova-work","gone-verb"]`, "every": "60"})
	rows, err := r.st.List(r.ctx, KindLoop)
	require.NoError(t, err)

	dead := DeadLoops(ctx, rows, probe)
	assert.Equal(t, []string{
		"loop sprint-table-live runs nova-sprint table, and nova-sprint has no verb table: its unit exits at every start; run: nova-config loop remove sprint-table-live",
	}, dead, "status names the dead loop and its remove; a live one, and one whose program is not installed here, are not named")

	_, gone := DeadLoopVerb(ctx, Argv(tableLive), fakeVerbs(nil))
	assert.False(t, gone, "a probe that cannot answer judges nothing")
}

// add and set refuse an enabled loop whose verb is gone, the kind's own rules
// beside it; a disabled one passes, and so does a verb the program still has.
func TestALoopWhoseVerbIsGoneIsRefusedAtAdd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	probe := fakeVerbs(installed)
	k, _ := Lookup(KindLoop)
	row := func(name string, raw map[string]string) Row {
		r, err := k.NewRow(name, raw)
		require.NoError(t, err, "the kind's Check runs no program and passes %s", name)
		return r
	}

	err := CheckLoopVerb(ctx, row("table-live", map[string]string{"machine": "m1", "argv": tableLive, "keepalive": "true", "seat": "s", "keys": "K"}), probe)
	require.Error(t, err)
	assert.Equal(t, "loop table-live runs nova-sprint table, and nova-sprint has no verb table: its unit exits at every start; run nova-sprint help for its verbs, or --enabled false", err.Error())

	assert.NoError(t, CheckLoopVerb(ctx, row("off", map[string]string{"machine": "m1", "argv": tableLive, "keepalive": "true", "seat": "s", "keys": "K", "enabled": "false"}), probe),
		"a disabled loop starts no unit: --enabled false is a way out")
	assert.NoError(t, CheckLoopVerb(ctx, row("live", map[string]string{"machine": "m1", "argv": `["nova-sprint","where","--watch"]`, "keepalive": "true"}), probe))
	assert.NoError(t, CheckLoopVerb(ctx, row("seat-push", map[string]string{"machine": "m1", "argv": `["/usr/bin/env","NOVA_SPRINT_SERVER=127.0.0.1:6390","nova-sprint","inbox","--wait","--push","seat"]`, "keepalive": "true"}), probe))
	assert.NoError(t, CheckLoopVerb(ctx, row("table-live", map[string]string{"machine": "m1", "argv": tableLive, "keepalive": "true", "seat": "s", "keys": "K"}), fakeVerbs(nil)),
		"a probe that cannot answer refuses nothing")
}

// NovaTools is every program of cmd/: a new tool is asked like the others.
func TestNovaToolsIsEveryCommand(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir("../../cmd")
	require.NoError(t, err)
	var cmds []string
	for _, e := range entries {
		if e.IsDir() {
			cmds = append(cmds, e.Name())
		}
	}
	assert.ElementsMatch(t, cmds, NovaTools, "pkg/config/loop.go's NovaTools is not the set of cmd/ directories; add the new command there")
}
