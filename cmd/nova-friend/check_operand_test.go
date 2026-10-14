package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/require"
)

// The flag parser stops at the first friend name, so a flag after it arrives
// as an operand: the health check refuses it, named, at exit 2 before any
// friend is checked (docs/STANDARD.md §2, "It refuses to guess, and recovery
// takes one turn"). With the daemon down, status labels every counter it
// reads from the daemon's files last-known instead of printing it as live
// (docs/SPEC-FRIEND.md, "A friend's status, from evidence"), and the exit
// table says what the verb does: exit 1 only on STATUS NONE. The check's help
// never says "its" about a friend: their agent, the friend's own.
func TestCheckRefusesAFlagGivenAsAFriendNameAndStatusLabelsLastKnown(t *testing.T) {
	t.Parallel()

	// a flag where a friend name goes is refused at 2, named, with where the
	// flags go, and no friend is checked
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	cli.Do(t, "check", "--as", "ada", "bob", "--since", "1h").
		Exit(2).
		Err("CHECK REFUSED", `"--since"`, "flags go before the friend names", "nova-friend check [flags] <friend>...").
		NotOut("CHECK DAEMON", "CHECK VERDICT")

	// the daemon up: the counters keep their live names
	dir := t.TempDir()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Seat: "ada", LastPing: start.Add(-time.Minute), Connection: friend.Connected, Challenge: friend.Challenged, Nonce: "n1", LastDaemonPong: start.Add(-30 * time.Second), Pongs: 2, Beats: 7, Width: 4, Delivered: 2}))
	require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start.Add(-2 * time.Minute), Queue: 3, Working: 1, Width: 8}))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"a","state":"queued"},{"id":"b","state":"working"}]}`), 0o644))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out("STATUS OK daemon=up", "last_pong=2026-10-04T02:58:00Z", "pongs=2", "queue=1", "working=1", "width=8", "beats=7").
		NotOut("last_known")

	// the daemon down: the same counters, from the files it left, labelled
	// last-known, their values kept
	r.now = start.Add(friend.DaemonStale)
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out("STATUS OK daemon=down",
			"last_pong_last_known=2026-10-04T02:58:00Z", "pongs_last_known=2", "queue_last_known=1",
			"working_last_known=1", "width_last_known=8", "beats_last_known=7").
		NotOut("last_pong=", "pongs=", "queue=", "beats=")

	// check -h says their agent and the friend's own, and the exit table
	// tells the truth about status: exit 1 only on STATUS NONE
	help := cli.Do(t, "check", "-h").Exit(0).Stdout
	require.Contains(t, help, "their agent")
	require.Contains(t, help, "the friend's own")
	require.NotContains(t, help, "its agent")
	require.NotContains(t, help, "the friend itself")
	require.Contains(t, help, "status: no status file (STATUS NONE)")
	require.NotContains(t, help, "status: no daemon")
}
