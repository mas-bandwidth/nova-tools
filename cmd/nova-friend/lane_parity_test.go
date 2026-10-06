package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
)

// A provider failure writes the pause marker; only a person's resume clears it, and says what it held.
func TestResumeClearsTheLanesPauseAPersonBringsUp(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	cli.Do(t, "resume", "--as", "bob").Exit(0).Out("cleared=none")
	require.NoError(t, friend.WritePause(state, "402 Payment Required: insufficient balance", start))
	cli.Do(t, "resume", "--as", "bob", "--dry-run").Exit(0).Out("cleared=would", `402\x20Payment\x20Required:\x20insufficient\x20balance`)
	assert.NotEmpty(t, friend.ReadPause(state), "a dry run clears nothing")
	cli.Do(t, "resume", "--as", "bob").Exit(0).Out("cleared=yes", `402\x20Payment\x20Required:\x20insufficient\x20balance`)
	assert.Empty(t, friend.ReadPause(state))
}

// The shims run this binary as go or gofmt: the refuse-go verb says no, exit 2, with the next step.
func TestRefuseGoRefusesWithTheWayToABench(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	for _, name := range friend.GoShimNames {
		cli.Do(t, "refuse-go", "--name", name).Exit(2).Err(name+" is refused: no go command runs on this machine", "; run: rsync the job's clone to a bench")
	}
}
