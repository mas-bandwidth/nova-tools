package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamOf is the entries on name's stream.
func streamOf(t *testing.T, r *rig, name string) []map[string]string {
	t.Helper()
	es, err := r.store.Range(context.Background(), bus.StreamOf(name), "-", "+", 0)
	require.NoError(t, err)
	out := make([]map[string]string, len(es))
	for i, e := range es {
		out[i] = e.Fields
	}
	return out
}

// TestWritesTableFormSend pins C30's send writer: after the switch every
// entry send writes carries the typed envelope in the bus's native binary
// field (S3), and the dual reader reads it strictly as the binary form.
func TestWritesTableFormSend(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	id := id(t, r.cli().OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "switched", "--kind", "report", "--card", "c1", "--head", strings.Repeat("ab", 20), "--body", "the body\n").Stdout)
	fields := streamOf(t, r, "bob")
	require.Len(t, fields, 1)
	require.Contains(t, fields[0], bus.EnvelopeField, "the entry carries the native binary field")
	reader := bus.NewEnvelopeReader()
	m, form, err := reader.ReadEntry(fields[0])
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormBinary, form)
	assert.Equal(t, []any{id, "ada", []string{"bob"}, "switched", bus.KindReport, "c1", "the body\n"}, []any{m.ID, m.From, m.To, m.Subject, m.Kind, m.Card, m.Body})
	assert.Equal(t, uint64(1), reader.Reads(schemaread.FormBinary))
	assert.Zero(t, reader.Reads(schemaread.FormLegacy))
}

// TestRefusesNotFallsBackSend pins that a send past one of the envelope's
// bounds refuses (exit 2, one SEND REFUSED line) and writes nothing, never
// falling back to a legacy entry without the native field.
func TestRefusesNotFallsBackSend(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	for _, args := range [][]string{
		{"--subject", strings.Repeat("s", 201)},
		{"--subject", "s", "--card", strings.Repeat("c", 65)},
		{"--subject", "s", "--head", "nothex"},
	} {
		res := r.cli().Do(t, append([]string{"send", "--as", "ada", "--to", "bob", "--body", "b\n"}, args...)...).Exit(2).Err("SEND REFUSED")
		assert.Equal(t, 0, strings.Count(strings.TrimSuffix(res.Stderr, "\n"), "\n"), "%v: one line: %q", args, res.Stderr)
	}
	assert.Empty(t, streamOf(t, r, "bob"), "a refused send writes nothing")
	assert.Zero(t, r.store.Len(bus.LogKey))
}

// TestWriterSwitchOldReadersStillReadSend pins the readers-first proof (S4)
// for send: a reader that has not learned the native field (bus.Parse, the
// legacy fields) reads the switched writer's entry as before, field for
// field, beside the dual reader's binary read of the same entry.
func TestWriterSwitchOldReadersStillReadSend(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.cli().OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "both readers", "--body", "read alike\n")
	fields := streamOf(t, r, "bob")
	require.Len(t, fields, 1)
	old := bus.Parse(fields[0])
	m, form, err := bus.NewEnvelopeReader().ReadEntry(fields[0])
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormBinary, form)
	assert.Equal(t, m.ID, old.ID)
	assert.Equal(t, m.From, old.From)
	assert.Equal(t, m.To, old.To)
	assert.Equal(t, m.Subject, old.Subject)
	assert.Equal(t, m.Kind, old.Kind)
	assert.Equal(t, m.Body, old.Body)
	assert.True(t, m.At.Equal(old.At))
}
