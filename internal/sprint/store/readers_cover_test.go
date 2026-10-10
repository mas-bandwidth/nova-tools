package store

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ForgetReaders (readers.go) deletes the beat and the hold of readers taken
// off the readers table (docs/SPEC-SPRINT.md section 6): both records under
// the deployment's prefix in one delete, so a reader added again under the
// name starts clean. A forget of a reader with no records left says no error,
// and a store that does not answer the delete is an error, never a silent ok.
// These tests reach the readers.go function no other store test reaches: each
// row pins the records the forget deletes and the ones a reader it does not
// name keeps, and the one refusal, the delete whose reply is lost.

// deleteCut is a store that does not answer a key delete: the lost reply the
// forget sees as the connection closing.
type deleteCut struct {
	Backend
	err error
}

func (d *deleteCut) DeleteKeys(context.Context, []string) (int, error) {
	return 0, d.err
}

func TestReadersCoverForgetReadersDeletesBeatAndHold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		beat      []string // a reader beat with no row on the readers table
		forget    []string
		gone      []string // the readers both records of which the forget deletes
		kept      []string // the readers whose beat a forget they are not named by keeps
		wantState string   // reader-a's state after the forget
	}{
		{"one reader", nil, []string{"reader-a"}, []string{"reader-a"}, []string{"reader-b", "reader-c"}, "down"},
		{"two readers", nil, []string{"reader-b", "reader-c"}, []string{"reader-b", "reader-c"}, []string{"reader-a"}, "away"},
		{"a reader no row", []string{"reader-z"}, []string{"reader-z"}, []string{"reader-z"}, []string{"reader-a", "reader-b", "reader-c"}, "away"},
		{"no readers", nil, nil, nil, []string{"reader-a", "reader-b", "reader-c"}, "away"},
		{"a reader with no records", nil, []string{"reader-nobody"}, nil, []string{"reader-a"}, "away"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-a", true, "coordinator"))
			if len(tc.beat) > 0 {
				require.NoError(t, h.st.beatReaders(h.ctx, "", tc.beat...))
			}
			got, err := h.st.ReaderStates(h.ctx, []string{"reader-a"}, h.st.now())
			require.NoError(t, err)
			assert.Equal(t, "away", got["reader-a"], "the hold is on before the forget")
			require.NoError(t, h.st.ForgetReaders(h.ctx, tc.forget))
			for _, r := range tc.gone {
				_, ok, err := h.m.GetKey(h.ctx, readerBeatKey(r))
				require.NoError(t, err)
				assert.False(t, ok, "reader %s: the beat is gone", r)
				_, ok, err = h.m.GetKey(h.ctx, readerAwayKey(r))
				require.NoError(t, err)
				assert.False(t, ok, "reader %s: the hold is gone", r)
			}
			for _, r := range tc.kept {
				_, ok, err := h.m.GetKey(h.ctx, readerBeatKey(r))
				require.NoError(t, err)
				assert.True(t, ok, "reader %s: its beat stays", r)
			}
			got, err = h.st.ReaderStates(h.ctx, []string{"reader-a"}, h.st.now())
			require.NoError(t, err)
			assert.Equal(t, tc.wantState, got["reader-a"], "the state the forget leaves reader-a in")
		})
	}
}

func TestReadersCoverForgetReadersRefusesWhenTheStoreDoesNotAnswer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cut  string
	}{
		{"the connection closes", "write tcp: connection reset by peer"},
		{"the store times out", "context deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-a", true, "coordinator"))
			st := *h.st
			st.B = &deleteCut{Backend: h.m, err: errors.New(tc.cut)}
			err := st.ForgetReaders(h.ctx, []string{"reader-a"})
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.cut)
			_, ok, err := h.m.GetKey(h.ctx, readerBeatKey("reader-a"))
			require.NoError(t, err)
			assert.True(t, ok, "a refused forget deletes no beat")
			_, ok, err = h.m.GetKey(h.ctx, readerAwayKey("reader-a"))
			require.NoError(t, err)
			assert.True(t, ok, "a refused forget deletes no hold")
		})
	}
}
