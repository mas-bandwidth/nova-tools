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
// is not listening to messages sent back." And Glenn, 2026-10-08 (issue
// #5450, measured on a new fleet machine where send and recv were refused
// for every name without a daemon proof, a claude friend never writing one):
// "it is important that we can talk to friends, if you can't that's totally
// a bug." So the push proof is the seat's liveness rule and a sender's
// advice, never a gate: send lands and recv reads for every roster name, and
// a name whose friend daemon has not proven its inbox push under ten minutes
// is one NOTE push=<state> for <name> beside the result; names shows each
// name's push, its age and what it rests on.
func TestSendAndRecvNoteAnUnprovenPushAndNeverRefuseOnIt(t *testing.T) {
	t.Parallel()
	r := deafRig("ada", "bob")
	cli := r.cli()
	send := []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}

	// no proof at all: the message lands, and the sender and the recipient are each a NOTE
	first := id(t, cli.Do(t, send...).Exit(0).Out("SEND OK id=",
		"SEND NOTE push=none for ada: no proven push since never: no daemon has recorded one",
		"SEND NOTE push=none for bob: no proven push since never",
		"waits on its stream until something reads it (nova-bus recv --as bob)",
		"nova-friend install --as bob --harness <h> --dir <d>").Stdout)
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "a send to an unheard name lands")
	cli.Do(t, append(send, "--dry-run")...).Exit(0).Out("SEND OK ", "SEND NOTE push=none for ada").NotOut("id=0")
	cli.Do(t, "recv", "--as", "bob", "--dry-run").Exit(0).Out("RECV OK pending=0 new=1", "RECV NOTE push=none for bob")
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK id="+first, "RECV NOTE push=none for bob: no proven push since never")
	cli.Do(t, "recv", "--as", "bob").Exit(1).Err("RECV NONE: nothing for bob", "RECV NOTE push=none for bob")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2 proven=0", "NAMES NAME name=ada push=none age=never harness=-", "NAMES NAME name=bob push=none age=never")

	// a batch and a loop say it once, on the first result; a cc is a recipient too
	cli.OK(t, send...)
	cli.Do(t, "send", "--as", "ada", "--to", "ada", "--cc", "bob", "--subject", "s", "--body", "x").Exit(0).Out("SEND NOTE push=none for ada", "SEND NOTE push=none for bob")
	cli.OK(t, send...)
	out := cli.Do(t, "recv", "--as", "bob", "--all").Exit(0).Out("RECV NOTE push=none for bob").Stdout
	assert.Equal(t, 1, strings.Count(out, "RECV NOTE"), "once per recv, not per message:\n%s", out)
	assert.Equal(t, 3, strings.Count(out, "RECV OK id="), out)

	// the sender proven, the recipient not: only the recipient is noted
	r.prove(start, true, "ada")
	cli.Do(t, send...).Exit(0).Out("SEND NOTE push=none for bob").NotOut("for ada")
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK", "RECV NOTE push=none for bob")

	// both proven: no NOTE at all
	r.prove(start, true, "bob")
	mid := id(t, cli.Do(t, send...).Exit(0).NotOut("SEND NOTE").Stdout)
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK id=" + mid).NotOut("RECV NOTE")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2 proven=2", "NAMES NAME name=ada push=proven age=", "harness=claude")

	// a proof the daemon stopped renewing goes stale at ten minutes: still advice
	r.store.Advance(bus.PushFresh)
	cli.Do(t, send...).Exit(0).Out("SEND NOTE push=stale for ada: no proven push since 10m", "last renewed it", "SEND NOTE push=stale for bob: no proven push since 10m")
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK", "RECV NOTE push=stale for bob: no proven push since 10m")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2 proven=0", "NAMES NAME name=bob push=stale age=10m")

	// renewed, then the session stops answering its check: down at once
	now := start.Add(bus.PushFresh + time.Minute)
	r.store.Advance(time.Minute)
	r.prove(now, true, "ada")
	r.prove(now, false, "bob")
	cli.Do(t, send...).Exit(0).Out("SEND NOTE push=down for bob: no proven push since", "holds no answered SESSION CHECK from the session (no session answer)").NotOut("for ada")
	cli.Do(t, "names").Exit(0).Out("NAMES OK count=2 proven=1", "NAMES NAME name=ada push=proven", "NAMES NAME name=bob push=down age=", "harness=claude")
	assert.Equal(t, 8, r.store.Len(bus.LogKey), "every send landed")
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
