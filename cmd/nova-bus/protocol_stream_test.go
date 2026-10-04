package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
)

// TestTheProgressRegistryMatchesTheLineThisProgramWrites keeps the registry in
// internal/bus honest about THIS program: the walk's two shapes are both
// progress by the shared answer, so a consumer that asks internal/bus drops
// both without having heard of either.
func TestTheProgressRegistryMatchesTheLineThisProgramWrites(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"INBOX WALK commits=1/1 notes=0 elapsed=3ms",
		`INBOX WALK bounded commits=500 remedy="raise --max-commits or close --before <instant>"`,
	} {
		assert.Truef(t, bus.IsProgress(line), "internal/bus does not know this is progress, so no consumer does:\n%s", line)
		assert.Falsef(t, bus.IsProtocol(line), "a progress line is also claimed as protocol, which is the two halves of the rule contradicting each other:\n%s", line)
	}
	// The complement: the lines consumers DO parse are protocol and are never
	// dropped as progress.
	for _, line := range []string{
		"INBOX NOTE id=bo-abcdef012345 from=Bo addr=to at=2026-09-07T00:01:00Z path=from-bo/x.md: A question",
		"INBOX OPEN carrying=2 heard=1 large=false remedy=-",
		"INBOX OK as=Ada carrying=2 open=0 notes=1 receipts=0 heard=1 unaddressed=0 unreadable=0",
		"INBOX HEARD id=bo-111111111111 from=Bo addr=to at=2026-09-07T00:02:00Z path=from-bo/y.md: Heard",
		"RECEIPT RECORD note=bo-abcdef012345 lane=from-ada",
	} {
		assert.Falsef(t, bus.IsProgress(line), "a protocol line is dropped as progress, which is the false quiet arriving by the other road:\n%s", line)
		assert.Truef(t, bus.IsProtocol(line), "a documented protocol line is not in internal/bus.ProtocolPrefixes:\n%s", line)
	}
	// A prefix match on token boundaries and not on substrings: a future
	// `INBOX WALKER` is not this progress line.
	assert.False(t, bus.IsProgress("INBOX WALKER id=x"), "the progress match is a substring match, so a future token that merely starts with one would be dropped unread")
}
