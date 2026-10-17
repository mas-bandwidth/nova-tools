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

// sentPong is the one pong from on the rig's bus log.
func sentPong(t *testing.T, r *rig, from string) bus.Message {
	t.Helper()
	es, err := r.store.Range(context.Background(), bus.LogKey, "-", "+", 0)
	require.NoError(t, err)
	var out []bus.Message
	for _, e := range es {
		if m := e.Message(); m.From == from && friend.IsPong(m) {
			out = append(out, m)
		}
	}
	require.Len(t, out, 1, "one pong from %s lands on the bus", from)
	return out[0]
}

// TestWritesTableForm pins C30's pong writer: after the switch the pong verb
// writes the table form, never the legacy line. The body is the padded
// standard base64 of the pong table's binary wire (S2: a bus body is a text
// transport), it reads strictly as base64 with the pong's own friend and
// counts, and the reader's per-form counter shows the read under base64.
func TestWritesTableForm(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.cli().Do(t, "pong", "--as", "bob", "--nonce", "abc123", "--to", "ada", "--queue", "2", "--working", "1", "--width", "4").Exit(0).Out("PONG OK nonce=abc123 to=ada id=")
	m := sentPong(t, r, "bob")
	assert.False(t, strings.HasPrefix(m.Body, "PONG "), "the body is not the legacy line: %q", m.Body)
	pongs := friend.NewPongReader()
	p, form, err := pongs.Message(m)
	require.NoError(t, err, "a table pong reads strictly")
	assert.Equal(t, schemaread.FormBase64, form, "the pong body is the table form as base64")
	assert.Equal(t, friend.Pong{Friend: "bob", Nonce: "abc123", Queue: 2, Working: 1, Width: 4, Sent: p.Sent}, p)
	assert.False(t, p.Sent.IsZero(), "the table pong carries its own send time")
	snap := pongs.Counts().Snapshot()
	assert.Equal(t, uint64(1), snap["base64"])
	assert.Zero(t, snap["legacy"])

	dry := r.cli().Do(t, "pong", "--as", "bob", "--nonce", "abc124", "--to", "ada", "--dry-run").Exit(0)
	assert.Contains(t, dry.Stdout, "form=base64", "the dry run names the form it would send")
	sentPong(t, r, "bob") // the dry run sent nothing
}

// TestRefusesNotFallsBack pins that a pong past the table's bounds refuses
// (exit 2, one PONG REFUSED line on stderr) and writes nothing, never falling
// back to the legacy line: a nonce over the table's 32 bytes, and a count past
// its range, are each refused by the writer.
func TestRefusesNotFallsBack(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	before := r.store.Len(bus.LogKey)
	for _, args := range [][]string{
		{"--nonce", strings.Repeat("z", 33)},
		{"--nonce", "n1", "--queue", "-1"},
		{"--nonce", "n1", "--width", "-1"},
	} {
		res := r.cli().Do(t, append([]string{"pong", "--as", "bob", "--to", "ada"}, args...)...).Exit(2).Err("PONG REFUSED")
		assert.Equal(t, 0, strings.Count(strings.TrimSuffix(res.Stderr, "\n"), "\n"), "%v: one line: %q", args, res.Stderr)
		assert.Empty(t, res.Stdout, "%v", args)
	}
	assert.Equal(t, before, r.store.Len(bus.LogKey), "a refused pong writes nothing")
}

// TestWriterSwitchOldReadersStillRead pins the readers-first proof (S4): the
// pong reader every seat already runs (PongReader, which the friend machine
// and the delivery check use) reads what the switched writer writes, and it
// refuses a value in the retired legacy form by name.
func TestWriterSwitchOldReadersStillRead(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob", "cy")
	r.cli().Do(t, "pong", "--as", "bob", "--nonce", "n1", "--to", "ada", "--queue", "7", "--working", "3", "--width", "9").Exit(0)
	_, err := (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{
		From: "cy", To: []string{"ada"}, Subject: friend.PongSubject, Body: friend.PongLine("n2", 1, 0, 2) + "\n",
	})
	require.NoError(t, err, "an old writer's legacy pong")
	pongs := friend.NewPongReader()
	p, form, err := pongs.Message(sentPong(t, r, "bob"))
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormBase64, form)
	assert.Equal(t, []any{"bob", "n1", 7, 3, 9}, []any{p.Friend, p.Nonce, p.Queue, p.Working, p.Width})
	_, form, err = pongs.Message(sentPong(t, r, "cy"))
	require.Error(t, err)
	assert.Equal(t, schemaread.FormLegacy, form)
	assert.Equal(t, "PONG REFUSED: legacy; run: "+friend.PongHelp, err.Error())
	snap := pongs.Counts().Snapshot()
	assert.Equal(t, []uint64{1, 1, 1}, []uint64{snap["base64"], snap["legacy"], snap["refused_legacy"]})
}
