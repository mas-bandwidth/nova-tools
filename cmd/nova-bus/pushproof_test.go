package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// Glenn, 2026-10-05: "nova-bus is useless if the friend using it is deaf and
// is not listening to messages sent back." A note to the coordinator sat 40
// minutes unread while neither side had a push into its session, and the
// bus took every message anyway. A name is on the bus only while something
// proven can hear it: send --as and recv --as refuse until the name has an
// inbox push its friend daemon proved (a SESSION CHECK carried in by the
// deliver adapter and answered by the session) younger than ten minutes;
// send --to a name whose proof is stale, down or missing is refused with
// `deaf: <x> has no proven push since <age>` and the remedy, writing
// nothing; names shows each name's push and its age.
func TestSendAndRecvRefuseUntilTheInboxPushIsProven(t *testing.T) {
	t.Parallel()
	r := deafRig("ada", "bob")
	cli := r.cli()
	send := []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}

	// no proof at all: the sender and the recipient are both deaf, named at once
	cli.Do(t, send...).Exit(2).Err("SEND REFUSED",
		"deaf: ada has no proven push since never: no daemon has recorded one",
		"deaf: bob has no proven push since never",
		"nova-friend install --as bob --harness <h> --dir <d>", "SESSION CHECK")
	cli.Do(t, append(send, "--dry-run")...).Exit(2).Err("deaf: ada has no proven push since never")
	cli.Do(t, "recv", "--as", "bob").Exit(2).Err("RECV REFUSED", "deaf: bob has no proven push since never")
	cli.Do(t, "recv", "--as", "bob", "--dry-run").Exit(2).Err("deaf: bob has no proven push since never")
	cli.Do(t, "recv", "--as", "bob", "--forever", "--exec", "deliver").Exit(2).Err("deaf: bob")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2 proven=0", "NAMES NAME name=ada push=none age=never", "NAMES NAME name=bob push=none age=never")
	assert.Equal(t, 0, r.store.Len(bus.LogKey), "a send to or from a deaf name writes nothing")

	// the sender proven, the recipient not: the sender never talks into a void
	r.prove(start, true, "ada")
	cli.Do(t, send...).Exit(2).Err("deaf: bob has no proven push since never").NotErr("deaf: ada")
	cli.Do(t, "send", "--as", "ada", "--to", "ada", "--cc", "bob", "--subject", "s", "--body", "x").Exit(2).Err("deaf: bob") // a cc is a recipient too
	assert.Equal(t, 0, r.store.Len(bus.LogKey))

	// both proven: the loop works
	r.prove(start, true, "bob")
	mid := id(t, cli.OK(t, send...).Stdout)
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK id=" + mid)
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2 proven=2", "NAMES NAME name=ada push=proven age=", "harness=claude")

	// a proof the daemon stopped renewing goes stale at ten minutes
	r.store.Advance(bus.PushFresh)
	cli.Do(t, send...).Exit(2).Err("deaf: ada has no proven push since 10m", "last renewed it", "deaf: bob has no proven push since 10m")
	cli.Do(t, "recv", "--as", "bob").Exit(2).Err("deaf: bob has no proven push since 10m")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2 proven=0", "NAMES NAME name=bob push=stale age=10m")

	// renewed, then the session stops answering its check: down at once
	now := start.Add(bus.PushFresh + time.Minute)
	r.store.Advance(time.Minute)
	r.prove(now, true, "ada")
	r.prove(now, false, "bob")
	cli.Do(t, send...).Exit(2).Err("deaf: bob has no proven push since", "holds no answered SESSION CHECK from the session (no session answer)").NotErr("deaf: ada")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2 proven=1", "NAMES NAME name=ada push=proven", "NAMES NAME name=bob push=down")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "only the message sent while both were heard")

	// a deaf name may still look: peek, ack and log are not gated
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=0")
	cli.Do(t, "ack", "--as", "bob", "--id", mid).Exit(0).Out("ACK OK acked=1")
	cli.Do(t, "log").Exit(0).Out("LOG OK total=1")
}

// The log's oldest window is one read of 10000 (bus.Log); --after <id>
// lists past an entry and --newest from the end, so the latest message is
// reachable on a log of any length; the two together are refused.
func TestLogReachesTheNewestMessage(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	var ids, entries []string
	for i := range 3 {
		ids = append(ids, id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", fmt.Sprint("s", i), "--body", "x").Stdout))
	}
	all := cli.Do(t, "log").Exit(0).Out("LOG OK total=3", "subject=\"s0\"\nLOG MESSAGE", "subject=\"s2\"").Stdout
	for _, f := range strings.Fields(all) {
		if v, ok := strings.CutPrefix(f, "entry="); ok {
			entries = append(entries, v)
		}
	}
	require.Len(t, entries, 3, "each line says its place on the log stream: %s", all)
	newest := cli.Do(t, "log", "--newest", "--max", "1").Exit(0).Out("LOG OK total=3", "LOG MESSAGE id="+ids[2], "LOG MORE").NotOut("id=" + ids[0]).Stdout
	assert.Less(t, strings.Index(newest, "id="+ids[2]), strings.Index(newest, "LOG MORE"))
	cli.Do(t, "log", "--newest").Exit(0).Out("subject=\"s2\"\nLOG MESSAGE", "subject=\"s0\"")
	cli.Do(t, "log", "--after", entries[1]).Exit(0).Out("LOG OK total=1", "LOG MESSAGE id="+ids[2]).NotOut("id=" + ids[1])
	cli.Do(t, "log", "--after", entries[2]).Exit(0).Out("LOG OK total=0")
	cli.Do(t, "log", "--after", ids[0]).Exit(2).Err("--after wants a log entry id, <ms>-<seq> as LOG MESSAGE prints entry=")
	cli.Do(t, "log", "--after", entries[0], "--newest").Exit(2).Err("give one or the other")
}
