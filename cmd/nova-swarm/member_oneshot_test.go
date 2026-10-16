package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// A one-shot friend runs as a member (nova-swarm member --as friend.<name> --prompt
// <file>): the identity prompt file is handed to every launch, work and read alike,
// beside the commit identity, and the launch prepends it to the prompt the harness is
// handed (nativePrompt) while the card file stays byte for byte (the v1.3 friend lane
// spec, guarantee 12).
func TestAOneShotMemberHandsItsIdentityPromptToEveryLaunch(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "override/model", "999", 9*time.Second)
	r.prompt = "/friends/fay/IDENTITY.md"
	r.identity = "mas-bandwidth,Fay,fay@example.com"
	work, _ := launched(t, r, member.Packet{Card: "c1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c1"})
	assert.Equal(t, "/friends/fay/IDENTITY.md", work["--prompt"])
	assert.Equal(t, "mas-bandwidth,Fay,fay@example.com", work["--identity"])
	read, _ := launched(t, r, member.Packet{Card: "c1.r1.friend.fay", Kind: "read", Attempt: 1, Head: "abc", Brief: "c1: do it (s1) tier: pro\n\nThe task."})
	assert.Equal(t, "/friends/fay/IDENTITY.md", read["--prompt"], "a read carries the identity too")

	plain := argsRunner(t, "override/model", "999", 9*time.Second)
	none, _ := launched(t, plain, member.Packet{Card: "c1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c1"})
	_, given := none["--prompt"]
	assert.False(t, given, "a member with no identity prompt hands none")
}

// The prompt the harness is handed starts with the identity prompt; the card follows as
// it was, and a launch with none hands the card alone.
func TestTheHarnessIsHandedTheIdentityPromptFirst(t *testing.T) {
	t.Parallel()
	card := []byte("RESULT: c1 sha=<head>\n\nThe task.\n")
	with := nativePrompt(nativeRunConfig{card: card, prompt: "You are Fay (he/him)."})
	assert.Equal(t, "You are Fay (he/him).\n\n"+nativePrompt(nativeRunConfig{card: card}), with)
	assert.Contains(t, nativePrompt(nativeRunConfig{card: card}), "RESULT: c1 sha=<head>")
}
