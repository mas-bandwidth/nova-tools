package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// Every verb that writes takes --dry-run that writes nothing (docs/STANDARD.md, the
// onboarding standard): send checks the message as send does and adds no entry; recv
// says what waits and moves nothing to pending; ack says which ids are pending and acks
// none.
func TestTheWritingVerbsDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()

	cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--dry-run").Exit(0).
		Out("SEND OK", "to=bob", "bytes=1", "dry_run=true")
	assert.Equal(t, 0, r.store.Len(bus.LogKey), "a dry send adds no entry to the log")
	assert.Equal(t, 0, r.store.Len(bus.StreamOf("bob")), "nor to the recipient's stream")
	cli.Do(t, "send", "--as", "ada", "--to", "zed", "--subject", "s", "--body", "x", "--dry-run").Exit(2).Err("zed is no known name")

	mid := id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x").Stdout)
	cli.Do(t, "recv", "--as", "bob", "--dry-run").Exit(0).Out("RECV OK pending=0 new=1 next_new="+mid, "dry_run=true")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=0 new=1")

	require.Contains(t, cli.OK(t, "recv", "--as", "bob").Stdout, "RECV OK id="+mid)
	cli.Do(t, "ack", "--as", "bob", "--id", mid+",NOPE", "--dry-run").Exit(0).
		Out("ACK OK acked=1 asked=2", "dry_run=true", "ACK ID id="+mid+" acked=true", "ACK ID id=NOPE acked=false")
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=1 new=0")
}
