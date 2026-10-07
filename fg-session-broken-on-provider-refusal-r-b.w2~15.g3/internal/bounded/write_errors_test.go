package bounded

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A writer that fails a scripted call has not shown anything: the listing counts
// the offered item in Total and not in Shown, and only the writes that reached
// the stream are in the output. The scripted writer answers each write by its
// scripted entry -- a sentinel fails the call -- so a table row names exactly
// which call failed and what reached the stream.
var (
	errItemOne   = errors.New("the first item write failed")
	errItemTwo   = errors.New("the second item write failed")
	errMoreWrite = errors.New("the MORE write failed")
)

type scriptedWriter struct {
	script []error
	got    []byte
	calls  int
}

func (w *scriptedWriter) Write(p []byte) (int, error) {
	n := w.calls
	w.calls++
	if n < len(w.script) && w.script[n] != nil {
		return 0, w.script[n]
	}
	w.got = append(w.got, p...)
	return len(p), nil
}

func TestListingCountsAndDeliversAroundFailedWrites(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		max     int
		script  []error
		shown   int
		total   int
		calls   int
		wantOut string
	}{
		{
			name:    "a failed MORE write is not shown and the item is",
			max:     1,
			script:  []error{nil, errMoreWrite},
			shown:   1,
			total:   2,
			calls:   2,
			wantOut: "WAKE REPORT path=a\n",
		},
		{
			name:    "two failed item writes show nothing and count both",
			max:     0,
			script:  []error{errItemOne, errItemTwo, nil},
			shown:   0,
			total:   2,
			calls:   3,
			wantOut: "WAKE MORE kind=report shown=0 total=2 --max-lines 0 prints them all\n",
		},
		{
			// A write that failed has not shown anything, so the next line is
			// still under the ceiling and is offered: the script answers it,
			// delivered, before the MORE line fails.
			name:    "a failed item write stands even when the MORE write fails after it",
			max:     1,
			script:  []error{errItemOne, nil, errMoreWrite},
			shown:   1,
			total:   2,
			calls:   3,
			wantOut: "WAKE REPORT path=b\n",
		},
		{
			name:   "a delivered item and a delivered MORE keep the counts and bytes exact",
			max:    1,
			script: []error{nil, nil},
			shown:  1,
			total:  2,
			calls:  2,
			wantOut: "WAKE REPORT path=a\n" +
				"WAKE MORE kind=report shown=1 total=2 --max-lines 0 prints them all\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &scriptedWriter{script: tc.script}
			l := Capped(w, tc.max, "WAKE", "report", "--max-lines 0 prints them all")
			l.Line("WAKE REPORT path=a")
			l.Line("WAKE REPORT path=b")
			l.More()
			assert.Equal(t, tc.shown, l.Shown(), "Shown() counts only writes that reached the stream")
			assert.Equal(t, tc.total, l.Total(), "Total() counts every offered item")
			assert.Equal(t, tc.calls, w.calls, "the writer answered %d calls; a capped item offers no write", w.calls)
			assert.Equal(t, tc.wantOut, string(w.got), "the successful writes the stream received")
		})
	}
}
