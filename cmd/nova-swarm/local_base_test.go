package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// TestAMemberPassesTheLocalBaseToNative: a packet naming a local route's endpoint hands
// native --local-base with that URL; a packet with no endpoint adds nothing new.
func TestAMemberPassesTheLocalBaseToNative(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "override/model", "999", 9*time.Second)
	// a packet naming an endpoint gives native --local-base with that URL
	args, _ := launched(t, r, member.Packet{Card: "l1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/l1",
		Model: "local-g1/gemma4-32k", Tokens: "100000", Endpoint: "http://127.0.0.1:11434/v1"})
	assert.Equal(t, "http://127.0.0.1:11434/v1", args["--local-base"], "the packet's endpoint is native's --local-base")
	// a packet with no endpoint adds none
	args2, _ := launched(t, r, member.Packet{Card: "n1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/n1",
		Model: "openrouter/x-ai/grok-4.7", Tokens: "100000"})
	_, has := args2["--local-base"]
	assert.False(t, has, "a packet with no endpoint launches with no --local-base")
}

// TestNativeTakesALocalBaseOnlyWithALocalProvider: a local base is refused at exit 2
// before any launch unless the model is local-<host>/<tag> and the base's host may serve,
// and none of the refusals starts the harness.
func TestNativeTakesALocalBaseOnlyWithALocalProvider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, model, localBase, want string
	}{
		{"a non-local provider refuses --local-base", "openrouter/x-ai/grok-4.7", "http://127.0.0.1:11434/v1", "--local-base is for a local provider"},
		{"a local provider without --local-base is refused", "local-g1/gemma4-32k", "", "wants --local-base"},
		{"a public-address base is refused", "local-g1/gemma4-32k", "http://example.com:11434/v1", "does not resolve here"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, slot := aSlot(t)
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: aRunnableBinary(t), model: c.model, label: "lbl",
				card: []byte("a card\n"), slotDir: slot, root: root, localBase: c.localBase,
			}, &errOut)
			assertOneRefused(t, &errOut, code, c.want)
			require.NotContains(t, errOut.String(), "harness", "a refused local base launches no harness")
		})
	}
}
