package bus

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBusEnvelopeRefusesLegacy pins the retired legacy reader: an entry with
// no native envelope field is the legacy form, refused by that name before any
// field is decoded, and counted as a legacy read. Every bus send writes the
// native field, so no current writer makes such an entry.
func TestBusEnvelopeRefusesLegacy(t *testing.T) {
	t.Parallel()
	t.Run("a legacy entry is refused by name, never read", func(t *testing.T) {
		t.Parallel()
		r := NewEnvelopeReader()
		legacy := msg("ada", "bob").Fields()
		got, form, err := r.ReadEntry(legacy)
		require.Error(t, err, "an entry with no envelope field is refused")
		assert.Equal(t, Message{}, got, "a refused read answers no message")
		assert.Equal(t, schemaread.FormLegacy, form)
		var ref *schemaread.Refusal
		require.True(t, errors.As(err, &ref), "the refusal is a *schemaread.Refusal: %v", err)
		assert.Equal(t, "legacy", ref.Reason)
		assert.Equal(t, EnvelopeField, ref.Field)
		assert.EqualError(t, err, "ENVELOPE REFUSED: legacy (envelope); run: "+EnvelopeHelp)
		assert.Equal(t, uint64(1), r.Reads(schemaread.FormLegacy), "the legacy read is counted")
	})
	t.Run("an adopt cycle reads every kind with no legacy read", func(t *testing.T) {
		t.Parallel()
		b, f := rig(t, "ada", "bob", "cy")
		r := NewEnvelopeReader()
		ms := everyKind()
		for _, m := range ms {
			_, err := b.Send(context.Background(), m)
			require.NoError(t, err)
		}
		es, err := f.Range(context.Background(), LogKey, "-", "+", 100)
		require.NoError(t, err)
		require.Len(t, es, len(ms))
		for _, e := range es {
			_, form, err := r.ReadEntry(e.Fields)
			require.NoError(t, err)
			assert.Equal(t, schemaread.FormBinary, form)
		}
		assert.Zero(t, r.Reads(schemaread.FormLegacy), "one adopt cycle reads no legacy form")
		assert.Equal(t, uint64(len(ms)), r.Reads(schemaread.FormBinary))
	})
	t.Run("every send writes the native field", func(t *testing.T) {
		t.Parallel()
		b, f := rig(t, "ada", "bob")
		sent, err := b.Send(context.Background(), msg("ada", "bob"))
		require.NoError(t, err)
		es, err := f.Range(context.Background(), LogKey, "-", "+", 10)
		require.NoError(t, err)
		require.Len(t, es, 1)
		require.Contains(t, es[0].Fields, EnvelopeField, "a send carries the typed envelope whatever the Native switch says")
		m, form, err := NewEnvelopeReader().ReadEntry(es[0].Fields)
		require.NoError(t, err)
		assert.Equal(t, schemaread.FormBinary, form)
		assert.Equal(t, sent.ID, m.ID)
	})
}
