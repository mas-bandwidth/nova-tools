package main

import (
	"context"
	"encoding/json"
	"io"
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

// TestSendDryRunHandlesCall pins the successful JSON simulation and an early
// store refusal after send acknowledges Call.DryRun.
func TestSendDryRunHandlesCall(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()

	got := cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "preview", "--token", "dry-token", "--dry-run", "--json").Exit(0)
	var receipt struct {
		Result struct {
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts map[string]any `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.Stdout), &receipt))
	assert.Equal(t, "ok", receipt.Result.Status)
	assert.Equal(t, 0, receipt.Result.Exit)
	assert.Equal(t, true, receipt.Facts["dry_run"])
	assert.Equal(t, "bob", receipt.Facts["to"])
	assert.Equal(t, float64(len("preview")), receipt.Facts["bytes"])
	assert.Equal(t, 0, r.store.Len(bus.LogKey), "the simulated send adds no log entry")
	assert.Equal(t, 0, r.store.Len(bus.StreamOf("bob")), "the simulated send adds no recipient entry")
	_, recorded, err := r.store.Sent(context.Background(), bus.SentOf("ada", "dry-token"))
	require.NoError(t, err)
	assert.False(t, recorded, "the simulated send must not reserve its deduplication token")

	r.openErr = io.ErrUnexpectedEOF
	refused := cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "preview", "--dry-run", "--json").Exit(2)
	var refusal struct {
		Result struct {
			Status string   `json:"status"`
			Exit   int      `json:"exit"`
			Why    []string `json:"why"`
		} `json:"result"`
		Notes []string `json:"notes"`
	}
	require.NoError(t, json.Unmarshal([]byte(refused.Stdout), &refusal))
	assert.Equal(t, "refused", refusal.Result.Status)
	assert.Equal(t, 2, refusal.Result.Exit)
	require.NotEmpty(t, refusal.Result.Why)
	assert.Contains(t, refusal.Result.Why[0], io.ErrUnexpectedEOF.Error())
	assert.NotContains(t, refusal.Result.Why[0], "Call.DryRun", "the safety guard must not replace the store refusal")
	assert.Empty(t, refusal.Notes, "send reads Call.DryRun before setup, so the safety fallback is unnecessary")
}
