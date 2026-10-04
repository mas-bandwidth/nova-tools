package bounded

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Err promises the first write error this listing hit, across the item lines and
// the MORE line alike: a caller that records what it delivered records the write
// that failed first, whatever came after it. Two ways to break that promise are
// pinned here through a writer scripted one sentinel per call -- a MORE line
// failing alone, and a second failed item write overwriting the first -- beside
// the rows that keep the counts and the delivered bytes exact.
//
// Sentinels are distinct per scripted call so errors.Is names which write's
// error a listing kept.
var (
	errItemOne   = errors.New("the first item write failed")
	errItemTwo   = errors.New("the second item write failed")
	errMoreWrite = errors.New("the MORE write failed")
)

// scriptedWriter answers each write by its scripted entry: nil delivers the
// bytes into the collected successful writes, a sentinel fails the call, so a
// table row names exactly which call failed and what reached the stream.
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

func TestListingKeepsFirstDeliveryError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		max     int
		script  []error
		want    error
		shown   int
		total   int
		calls   int
		wantOut string
	}{
		{
			name:    "a MORE line that fails alone is the delivery error",
			max:     1,
			script:  []error{nil, errMoreWrite},
			want:    errMoreWrite,
			shown:   1,
			total:   2,
			calls:   2,
			wantOut: "WAKE REPORT path=a\n",
		},
		{
			name:    "the second failed item write does not replace the first",
			max:     0,
			script:  []error{errItemOne, errItemTwo, nil},
			want:    errItemOne,
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
			want:    errItemOne,
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
			assert.ErrorIs(t, l.Err(), tc.want, "Err() = %v, want the first write error the listing hit", l.Err())
			assert.Equal(t, tc.shown, l.Shown(), "Shown() counts only writes that reached the stream")
			assert.Equal(t, tc.total, l.Total(), "Total() counts every offered item")
			assert.Equal(t, tc.calls, w.calls, "the writer answered %d calls; a capped item offers no write", w.calls)
			assert.Equal(t, tc.wantOut, string(w.got), "the successful writes the stream received")
		})
	}
}
