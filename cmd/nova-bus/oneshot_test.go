package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// A one-shot friend (nova-config friend --session one-shot: no live session between
// cards) is not on the psychic link, so a send naming her is refused at once, exit 2
// with the one_shot reason and the remedy, alone or beside a hooked friend, and
// nothing is admitted for anyone: her work and context travel in her next card's
// brief (the v1.3 comms spec, guarantee 19; tla/FriendLane.tla, OneShot). A
// dry run is refused the same way.
func TestASendToAOneShotIsRefusedWithTheBriefRemedy(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob", "fay")
	r.oneShot = []string{"fay"}
	cli := r.cli()
	send := []string{"send", "--as", "ada", "--subject", "work", "--body", "hello"}

	cli.Do(t, append(send, "--to", "fay")...).Exit(2).Err("fay is a one-shot: put it in the next card's brief")
	cli.Do(t, append(send, "--to", "bob", "--cc", "fay")...).Exit(2).Err("fay is a one-shot: put it in the next card's brief")
	cli.Do(t, append(send, "--to", "fay", "--dry-run")...).Exit(2).Err("fay is a one-shot")
	assert.Zero(t, r.store.Len(bus.StreamOf("fay")), "nothing admitted for the one-shot")
	assert.Zero(t, r.store.Len(bus.StreamOf("bob")), "a send naming a one-shot admits nothing for the other recipient either")

	res := cli.Do(t, append(send, "--to", "fay", "--json")...).Exit(2)
	assert.Contains(t, res.Stdout+res.Stderr, `"one_shot"`, "the refusal carries the one_shot reason code")

	cli.Do(t, append(send, "--to", "bob")...).Exit(0).Out("SEND OK id=")
	assert.Equal(t, 1, r.store.Len(bus.StreamOf("bob")), "a send naming no one-shot is admitted as before")
}
