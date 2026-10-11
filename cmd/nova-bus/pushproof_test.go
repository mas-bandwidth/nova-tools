package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
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

// The owner, 2026-10-07: adopt wide ASAP; the seat's push proof is card
// the-seats-pushes-are-proven-before-the-sprint-moves-b. The gate is advisory: a
// send between two unproven names goes, with a NOTE for each, and recv takes its
// message the same way; --require-push (or NOVA_BUS_REQUIRE_PUSH=1) alone refuses
// it with the deaf line, writing and reading nothing, --dry-run alike.
func TestThePushGateIsAdvisoryUntilRequired(t *testing.T) {
	t.Parallel()
	r := deafRig("ada", "bob")
	cli := r.cli()
	send := []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}

	mid := id(t, cli.Do(t, send...).Exit(0).Out("SEND OK id=", "SEND NOTE push=none for ada", "SEND NOTE push=none for bob").Stdout)
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK id="+mid, "RECV NOTE push=none for bob")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "the advisory send was written")

	cli.Do(t, append(send, "--require-push")...).Exit(2).Err("SEND REFUSED",
		"deaf: ada has no proven push since never: no daemon has recorded one", "deaf: bob has no proven push since never")
	cli.Do(t, append(send, "--require-push", "--dry-run")...).Exit(2).Err("SEND REFUSED", "deaf: bob has no proven push since never")
	cli.Do(t, "recv", "--as", "bob", "--require-push").Exit(2).Err("RECV REFUSED", "deaf: bob has no proven push since never")
	cli.Do(t, "recv", "--as", "bob", "--require-push", "--dry-run").Exit(2).Err("RECV REFUSED", "deaf: bob has no proven push since never")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "a required gate writes nothing")

	// the variable is the flag; both names proven, the required gate passes and nothing is noted
	r.env[RequirePushEnv] = "1"
	cli = r.cli()
	cli.Do(t, send...).Exit(2).Err("SEND REFUSED", "deaf: ada has no proven push since never")
	r.prove(start, true, "ada")
	r.prove(start, true, "bob")
	cli.Do(t, send...).Exit(0).Out("SEND OK id=").NotOut("SEND NOTE")
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK id=").NotOut("RECV NOTE")
	assert.Equal(t, 2, r.store.Len(bus.LogKey), "the proven send was written")
}
