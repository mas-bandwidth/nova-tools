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
// a bug." So hearing is the seat's liveness rule and a sender's advice, never
// a gate: send lands and recv reads for every roster name, and a name that
// has not received lately (no daemon proof, no recv or ack under ten minutes)
// is one NOTE beside the result. A friend who works by hand on the bus, with
// no daemon at all, is heard by receiving: no failure-sounding NOTE follows
// a message she is reading and answering (2026-10-13, issue 4).
func TestAFriendWhoWorksByHandIsHeardByReceivingAndNeverNotedWhileShe(t *testing.T) {
	t.Parallel()
	r := deafRig("ada", "bob")
	cli := r.cli()
	send := []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}
	reply := []string{"send", "--as", "bob", "--to", "ada", "--subject", "r", "--body", "y"}

	// nobody has received yet: the message lands, and each is one NOTE saying so, in words
	// that do not read as a failure and name no push state
	first := id(t, cli.Do(t, send...).Exit(0).Out("SEND OK id=",
		"SEND NOTE ada has never received on the bus (last sent 0s ago): the message is queued on ada's stream and arrives when its receiver runs",
		"SEND NOTE bob has never received on the bus (never sent): the message is queued on bob's stream and arrives when its receiver runs",
		"nova-hook status --as bob",
		"nova-hook hook --as bob --role <comms|friend> under the session's Monitor",
		"Monitor nova-bus recv --as bob --forever --exec '<deliver-into-session>'").NotOut("push=", "no proven push").Stdout)
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "a send to an unheard name lands")
	cli.Do(t, append(send, "--dry-run")...).Exit(0).Out("SEND OK ", "SEND NOTE bob has never received").NotOut("id=0")
	cli.Do(t, "recv", "--as", "bob", "--dry-run").Exit(0).Out("RECV OK pending=0 new=1").NotOut("RECV NOTE")

	// bob reads by hand: no daemon, no proof, and no NOTE on a first recv
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK id=" + first).NotOut("RECV NOTE")
	cli.Do(t, "recv", "--as", "bob").Exit(1).Err("RECV NONE: nothing for bob").NotOut("RECV NOTE")
	// bob answers; ada has not received, so she is the only name noted
	cli.Do(t, reply...).Exit(0).Out("SEND OK id=", "SEND NOTE ada has never received on the bus (last sent ").NotOut("SEND NOTE bob")
	// ada reads by hand too: from now on the conversation carries no NOTE at all
	cli.Do(t, "recv", "--as", "ada").Exit(0).Out("RECV OK").NotOut("RECV NOTE")
	cli.Do(t, send...).Exit(0).Out("SEND OK id=").NotOut("SEND NOTE")
	cli.Do(t, reply...).Exit(0).Out("SEND OK id=").NotOut("SEND NOTE")
	assert.Equal(t, 4, r.store.Len(bus.LogKey), "every send landed")

	// names shows what each did: the old fields in place, the new ones after harness
	cli.Do(t, "names", "--as", "ada").Exit(0).Out("NAMES OK count=2 proven=0 heard=2 deaf=0",
		"NAMES NAME name=ada push=none age=never harness=- recv=", "sent=", "heard=true deaf=false",
		"NAMES NAME name=bob push=none age=never harness=- recv=").NotOut("recv=never")

	// ada stops receiving and goes on sending: she is deaf, bob (who also stopped, and sent
	// before it) is not, and a sender is told so, then bob's receiver is told it was silent
	r.store.Advance(bus.PushFresh)
	cli.Do(t, send...).Exit(0).Out("SEND OK id=",
		"SEND NOTE ada last received 10m", "(last sent 0s ago)",
		"SEND NOTE bob last received 10m", "the message is queued on bob's stream and arrives when its receiver runs").NotOut("push=")
	cli.Do(t, "names", "--as", "ada").Exit(0).Out("NAMES OK count=2 proven=0 heard=0 deaf=1",
		"NAMES NAME name=ada push=none age=never harness=- recv=10m", "heard=false deaf=true",
		"NAMES NAME name=bob push=none age=never harness=- recv=10m", "heard=false deaf=false")
	out := cli.Do(t, "recv", "--as", "bob", "--all").Exit(0).Out("RECV NOTE bob's receiver was not running: it last received 10m",
		"Monitor nova-bus recv --as bob --forever --exec '<deliver-into-session>'").Stdout
	assert.Equal(t, 1, strings.Count(out, "RECV NOTE"), "once per recv, not per message:\n%s", out)
	cli.Do(t, "recv", "--as", "bob").Exit(1).Err("RECV NONE: nothing for bob").NotOut("RECV NOTE")
	cli.Do(t, "names", "--as", "ada").Exit(0).Out("heard=1 deaf=1", "NAMES NAME name=bob push=none age=never harness=- recv=", "heard=true deaf=false")
	assert.Equal(t, 5, r.store.Len(bus.LogKey))
}

// An ack by id is the session reading too: a name that only acks is heard.
func TestAnAckByIdMakesTheSessionHeard(t *testing.T) {
	t.Parallel()
	r := deafRig("ada", "bob")
	cli := r.cli()
	mid := id(t, cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x").Exit(0).Stdout)
	cli.Do(t, "names", "--as", "ada").Exit(0).Out("NAMES NAME name=bob push=none age=never harness=- recv=never", "heard=false")
	cli.Do(t, "ack", "--as", "bob", "--id", mid).Exit(0).Out("ACK OK")
	cli.Do(t, "names", "--as", "ada").Exit(0).Out("NAMES OK count=2 proven=0 heard=1 deaf=1", "NAMES NAME name=ada push=none age=never harness=- recv=never sent=", "heard=false deaf=true", "NAMES NAME name=bob push=none age=never harness=- recv=", "heard=true deaf=false")
	cli.Do(t, "ack", "--as", "bob", "--id", mid, "--dry-run").Exit(0).Out("ACK OK")
}

// The daemon's proof still counts, and its word that the session is down still
// outweighs a recent recv (its own recv loop reads for a session that is not
// there): a proven name is heard whatever it has received, a name a fresh down
// proof names is noted even when it received a moment ago, and a stale proof
// says nothing of receiving.
func TestTheDaemonsProofStillMakesANameHeardAndItsDownWordOutweighsARecv(t *testing.T) {
	t.Parallel()
	r := deafRig("ada", "bob")
	cli := r.cli()
	send := []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}

	// both proven, neither has received: no NOTE at all
	r.prove(start, true, "ada", "bob")
	cli.Do(t, send...).Exit(0).Out("SEND OK id=").NotOut("SEND NOTE")
	cli.Do(t, "names", "--as", "ada").Exit(0).Out("NAMES OK count=2 proven=2 heard=2 deaf=0", "NAMES NAME name=ada push=proven age=", "harness=claude recv=never")

	// the proof goes stale at ten minutes and nobody received: advice, in the new words
	r.store.Advance(bus.PushFresh)
	cli.Do(t, send...).Exit(0).Out("SEND NOTE ada has never received on the bus", "SEND NOTE bob has never received on the bus").NotOut("push=")
	cli.Do(t, "names", "--as", "ada").Exit(0).Out("NAMES OK count=2 proven=0", "NAMES NAME name=bob push=stale age=10m")

	// bob receives: heard again with a stale proof
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK")
	cli.Do(t, send...).Exit(0).NotOut("SEND NOTE bob")

	// the daemon renews ada's and says bob's session did not answer: bob's recv a moment ago
	// does not outweigh the daemon's word
	now := start.Add(bus.PushFresh + time.Minute)
	r.store.Advance(time.Minute)
	r.prove(now, true, "ada")
	r.prove(now, false, "bob")
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK")
	cli.Do(t, send...).Exit(0).Out("SEND NOTE bob last received", "and its friend daemon says the session did not answer its SESSION CHECK (no session answer)").NotOut("for ada", "push=")
	cli.Do(t, "names", "--as", "ada").Exit(0).Out("NAMES NAME name=bob push=down age=", "heard=false")
}

// The owner, 2026-10-07: adopt wide ASAP. The gate is advisory: a send between
// two unheard names goes, with a NOTE for each, and recv takes its message the
// same way; --require-push (or NOVA_BUS_REQUIRE_PUSH=1) alone refuses a send
// with the deaf line, writing nothing, --dry-run alike. A gated recv is itself a
// receive, so it is refused only by a daemon's fresh word that the session is
// down; it never locks a name that has not received out of its first recv.
func TestTheHeardGateIsAdvisoryUntilRequired(t *testing.T) {
	t.Parallel()
	r := deafRig("ada", "bob")
	cli := r.cli()
	send := []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x"}

	mid := id(t, cli.Do(t, send...).Exit(0).Out("SEND OK id=", "SEND NOTE ada has never received", "SEND NOTE bob has never received").Stdout)
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK id=" + mid).NotOut("RECV NOTE")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "the advisory send was written")

	cli.Do(t, append(send, "--require-push")...).Exit(2).Err("SEND REFUSED",
		"deaf: ada has never received on the bus (last sent ", "the remedy:", "nova-hook hook --as ada --role <comms|friend>")
	cli.Do(t, append(send, "--require-push", "--dry-run")...).Exit(2).Err("SEND REFUSED", "deaf: ada has never received")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "a required gate writes nothing")

	// a gated recv is a receive: it passes for a name that has never received, dry-run alike
	cli.Do(t, "recv", "--as", "bob", "--require-push", "--dry-run").Exit(0).Out("RECV OK pending=1 new=0")
	cli.Do(t, "recv", "--as", "bob", "--require-push").Exit(1).Err("RECV NONE: nothing for bob")
	// but not past a daemon's fresh word that the session did not answer
	r.prove(start, false, "bob")
	cli.Do(t, "recv", "--as", "bob", "--require-push").Exit(2).Err("RECV REFUSED", "deaf: bob", "its friend daemon says the session did not answer its SESSION CHECK")
	cli.Do(t, "recv", "--as", "bob", "--require-push", "--dry-run").Exit(2).Err("RECV REFUSED", "deaf: bob")
	cli.Do(t, "recv", "--as", "bob").Exit(1).Err("RECV NONE: nothing for bob")

	// the variable is the flag; both names proven, the required gate passes and nothing is noted
	r.env[RequirePushEnv] = "1"
	cli = r.cli()
	r.prove(start, false, "ada")
	cli.Do(t, send...).Exit(2).Err("SEND REFUSED", "deaf: ada")
	r.prove(start, true, "ada")
	r.prove(start, true, "bob")
	cli.Do(t, send...).Exit(0).Out("SEND OK id=").NotOut("SEND NOTE")
	cli.Do(t, "recv", "--as", "bob").Exit(0).Out("RECV OK id=").NotOut("RECV NOTE")
	assert.Equal(t, 2, r.store.Len(bus.LogKey), "the heard send was written")
}
