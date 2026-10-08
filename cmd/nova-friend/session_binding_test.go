package main

import (
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"testing"
)

func TestPongCannotClaimAnotherRuntimeConversation(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.env["CODEX_THREAD_ID"] = "conversation-a"
	r.cli().Do(t, "pong", "--as", "bob", "--nonce", "n1", "--to", "ada", "--session", "conversation-b").Exit(2).Err("--session disagrees with the harness runtime session id")
	assert.NoFileExists(t, filepath.Join(r.home, ".nova-friend", "bob", "pong.json"))
}
