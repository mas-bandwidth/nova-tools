package main

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
)

// send --kind ask --deadline <d>: the deadline is the store's, and the two go
// together or not at all; never wait for ever.
func TestSendAnAskWantsADeadlineAndOnlyAnAskTakesOne(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "q", "--body", "?", "--kind", "ask").Exit(2).
		Err("SEND REFUSED", "an ask wants --deadline <duration>")
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "q", "--body", "?", "--deadline", "5m").Exit(2).
		Err("SEND REFUSED", "--deadline is for --kind ask")
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "q", "--body", "?", "--kind", "ask", "--deadline", "-1s").Exit(2).
		Err("SEND REFUSED", "--deadline wants a duration above zero")
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "q", "--body", "?", "--kind", "timeout").Exit(2).
		Err("SEND REFUSED", "the bus's own")
	assert.Equal(t, 0, r.store.Len(bus.LogKey), "a refused send writes nothing")
	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "q", "--body", "?", "--kind", "ask", "--deadline", "5m").Exit(0).
		Out("SEND OK", "kind=ask", "deadline=2026-10-03T12:05:01Z")
}

// asks lists what I asked and where each stands; overdue, the alarm the
// coordinator's loop runs, times an open ask out once and wakes the sender.
func TestAsksAndTheTimeoutTheOverdueAlarmDelivers(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob", "cy")
	cli := r.cli()
	a1 := id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "answered one", "--body", "?", "--kind", "ask", "--deadline", "5m").Stdout)
	a2 := id(t, cli.OK(t, "send", "--as", "ada", "--to", "cy", "--subject", "silent one", "--body", "?", "--kind", "ask", "--deadline", "5m").Stdout)
	cli.OK(t, "send", "--as", "bob", "--to", "ada", "--re", a1, "--subject", "re", "--body", "yes")

	cli.Do(t, "asks", "--as", "ada").Exit(0).Out("ASKS OK count=2 open=1 answered=1 timedout=0",
		"ASKS ASK id="+a2+" state=open to=cy", "ASKS ASK id="+a1+" state=answered to=bob", "answered_by=bob")
	cli.Do(t, "asks", "--as", "ada", "--open").Exit(0).Out("ASKS OK count=1", "id="+a2).NotOut("id=" + a1)

	r.advance(6 * time.Minute)
	cli.Do(t, "asks", "--as", "ada", "--timedout").Exit(0).Out("ASKS OK count=1", "id="+a2+" state=timedout", "timeout=-")
	got := cli.Do(t, "overdue", "--as", "ada").Stdout
	assert.Contains(t, got, "OVERDUE TIMEOUT ask="+a2+" from=ada to=cy")
	cli.Do(t, "overdue", "--as", "ada").NotOut("OVERDUE TIMEOUT")

	cli.Do(t, "recv", "--as", "ada", "--kind", "timeout").Exit(0).Out("RECV OK", "from="+bus.TimeoutFrom, "re="+a2, "kind=timeout", "subject=\"ASK TIMEOUT "+a2+" cy after 6m")
	cli.Do(t, "asks", "--as", "ada", "--timedout").Exit(0).Out("id=" + a2 + " state=timedout").NotOut("timeout=-")
	cli.Do(t, "asks", "--as", "ada", "--max", "1").Exit(0).Out("ASKS OK count=2", "MORE")
	cli.Do(t, "asks", "--as", "ada", "--open", "--all").Exit(2).Err("one of --open, --timedout or --all")
}

// asks --liveness is each friend's word from the asks to it, by the store's clock.
func TestAsksLiveness(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob", "cy")
	r.store.Friends = []string{"ada", "bob", "cy"}
	cli := r.cli()
	a1 := id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "q", "--body", "?", "--kind", "ask", "--deadline", "5m").Stdout)
	cli.OK(t, "send", "--as", "ada", "--to", "cy", "--subject", "q", "--body", "?", "--kind", "ask", "--deadline", "5m")
	cli.OK(t, "recv", "--as", "bob")
	cli.OK(t, "send", "--as", "bob", "--to", "ada", "--re", a1, "--subject", "re", "--body", "yes")
	r.advance(6 * time.Minute)
	cli.Do(t, "asks", "--as", "ada", "--liveness").Exit(0).Out("ASKS OK count=3 window=24h0m0s",
		"ASKS LIVE name=ada state=down", "ASKS LIVE name=bob state=up ask="+a1, "ASKS LIVE name=cy state=unreached")
}
