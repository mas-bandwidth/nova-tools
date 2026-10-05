package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A reader nova-swarm builds carries the script read (docs/SPEC-SPRINT.md, the script
// read): memberConfig sets Config.ScriptVerify for a reader, so a script card's head is
// read by the program that made it, and leaves it nil for a worker, whose reads are its
// models' alone.
func TestAReaderBuiltByNovaSwarmHasScriptVerify(t *testing.T) {
	t.Parallel()
	reader := memberConfig("r1", 0, true, nil, nil, t.TempDir(), false)
	assert.NotNil(t, reader.ScriptVerify, "a reader reads a script card by its program")
	worker := memberConfig("m1", 0, false, nil, nil, t.TempDir(), false)
	assert.Nil(t, worker.ScriptVerify, "a worker does not read script cards")
}
