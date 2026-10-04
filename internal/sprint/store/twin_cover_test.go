package store

// The tick's twin (twin.go): a store reads and writes through the twin it is
// given (ShareTwin), and a table whose change stream does not account for
// every revision between is a GapError, whose line names the table, the
// revisions and the stream's why (the caller reads that table whole). These
// rows reach both from values alone: no store, no clock, no socket.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTwinCoverGapErrorNamesTheGap pins GapError.Error: the line names the
// table, the revision range it could not account for and the stream's own
// why; a zero gap still formats; and a wrapped gap is still a *GapError, the
// refusal catchUp reads the table whole on (errors.As).
func TestTwinCoverGapErrorNamesTheGap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		gap  *GapError
		want string
	}{
		{
			name: "a named gap names the table, the revisions and the why",
			gap:  &GapError{Table: "work", From: 7, To: 12, Why: "another epoch wrote between"},
			want: "table work read whole: its change stream does not account for revisions 7 to 12: another epoch wrote between",
		},
		{
			name: "a zero gap still formats",
			gap:  &GapError{},
			want: "table  read whole: its change stream does not account for revisions 0 to 0: ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.gap.Error())
			wrapped := fmt.Errorf("the change stream of %q: %w", tc.gap.Table, tc.gap)
			var got *GapError
			require.ErrorAs(t, wrapped, &got, "a wrapped gap is still a *GapError")
			assert.Same(t, tc.gap, got)
		})
	}
}

// TestTwinCoverShareTwinSharesAndClearsTheStoresTwin pins Store.ShareTwin:
// the store's steps read and write through the twin it is given, and nil
// leaves the store with none, so the next use makes an empty one. Neither row
// dials a backend.
func TestTwinCoverShareTwinSharesAndClearsTheStoresTwin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		share *Twin
	}{
		{name: "a store shares the twin it is given", share: NewTwin()},
		{name: "nil leaves the store with no twin", share: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &Store{}
			st.ShareTwin(tc.share)
			if tc.share != nil {
				assert.Same(t, tc.share, st.tw, "the store keeps the twin it was given")
				assert.Same(t, tc.share, st.twin(), "twin() gives the shared twin")
				return
			}
			assert.Nil(t, st.tw, "nil clears the store's twin")
			made := st.twin()
			require.NotNil(t, made, "the store makes an empty twin on first use")
			assert.Same(t, made, st.tw, "the made twin is the store's")
		})
	}
}
