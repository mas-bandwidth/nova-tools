package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWritesTableForm pins that the pong writer writes the table form (the
// binary wire) after the switch, never the legacy line: a pong the pong verb
// sends reads strictly as the binary form with the pong's own friend and send
// time, and the reader's per-form counter shows the binary read.
func TestWritesTableForm(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.cli().Do(t, "pong", "--as", "bob", "--nonce", "abc123", "--to", "ada", "--queue", "2", "--working", "1", "--width", "4").Exit(0).Out("PONG OK nonce=abc123 to=ada id=")
	es, err := r.store.Range(context.Background(), bus.LogKey, "-", "+", 0)
	require.NoError(t, err)
	var m bus.Message
	for _, e := range es {
		if e.Message().From == "bob" && friend.IsPong(e.Message()) {
			m = e.Message()
			break
		}
	}
	require.NotZero(t, m.ID, "a pong from bob lands on the bus")
	pongs := friend.NewPongReader()
	p, form, err := pongs.Message(m)
	require.NoError(t, err, "a table pong reads strictly")
	assert.Equal(t, schemaread.FormBinary, form, "the pong body is the binary table form, not legacy")
	assert.Equal(t, "bob", p.Friend)
	assert.Equal(t, "abc123", p.Nonce)
	assert.Equal(t, 2, p.Queue)
	assert.Equal(t, 1, p.Working)
	assert.Equal(t, 4, p.Width)
	assert.Equal(t, uint64(1), pongs.Counts().Snapshot()["binary"])
	assert.Equal(t, uint64(0), pongs.Counts().Snapshot()["legacy"])
}

// TestRefusesNotFallsBack pins that a writer validation failure refuses (exit
// 2, one REFUSED line) and writes nothing, never falling back to the legacy
// line: a nonce past the schema's bound is refused by the writer.
func TestRefusesNotFallsBack(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	before := r.store.Len(bus.LogKey)
	r.cli().Do(t, "pong", "--as", "bob", "--nonce", strings.Repeat("z", 33), "--to", "ada").Exit(2).Err("PONG REFUSED")
	assert.Equal(t, before, r.store.Len(bus.LogKey), "a refused pong writes nothing")
}

// TestWriterSwitchOldReadersStillRead pins the readers-first proof: the reader
// in place before the switch (PongReader) still reads what the switched writer
// writes. The switched pong verb writes the table form, and the reader reads
// it strictly, round-tripping the pong's counts with the binary read counted.
func TestWriterSwitchOldReadersStillRead(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.cli().Do(t, "pong", "--as", "bob", "--nonce", "n1", "--to", "ada", "--queue", "7", "--working", "3", "--width", "9").Exit(0)
	es, err := r.store.Range(context.Background(), bus.LogKey, "-", "+", 0)
	require.NoError(t, err)
	var m bus.Message
	for _, e := range es {
		if e.Message().From == "bob" && friend.IsPong(e.Message()) {
			m = e.Message()
			break
		}
	}
	require.NotZero(t, m.ID)
	pongs := friend.NewPongReader()
	p, form, err := pongs.Message(m)
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormBinary, form)
	assert.Equal(t, "n1", p.Nonce)
	assert.Equal(t, 7, p.Queue)
	assert.Equal(t, 3, p.Working)
	assert.Equal(t, 9, p.Width)
	assert.Equal(t, uint64(1), pongs.Counts().Snapshot()["binary"])
}
