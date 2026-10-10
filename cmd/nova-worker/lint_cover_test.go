package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The coverage card for lint.go's fleet heredoc scanner (cover-cmd-nova-worker-lint.w1).
// fleetHeredocAt sat at 0.0% in the unit tier before these tests: no launcher script in
// the tree opens a heredoc, so the scanner that hands heredoc.open's delimiter back to
// fleetLineScan never ran. The function is pure -- bytes in, a delimiter and a byte length
// out, no store, no clock, no child -- so every case drives it direct, the way the
// diskguard cover tests drive their helpers. A heredoc's body is shell data and is not
// linted by this pass: the one thing that matters is whether the opener names a delimiter
// at all, which is what fleetHeredocAt answers.

// TestLintCoverHeredocAt pins the fleet heredoc opener: for every spelling the shell
// accepts it names the delimiter and the full byte length of the opener, and for the
// shapes that are not one -- a herestring, a bare `<<`, a `<<` with only spaces, or a
// quoted delimiter that never closes -- it returns no delimiter and no length, so
// fleetLineScan leaves the `<` as ordinary code.
func TestLintCoverHeredocAt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		line      string
		i         int
		wantDelim string
		wantN     int
	}{
		// main path: a `<<` that names a delimiter hands back the delimiter and
		// the whole opener's length so fleetLineScan can skip its body as data.
		{name: "plain delimiter", line: "cat <<EOF", i: 4, wantDelim: "EOF", wantN: 5},
		{name: "delimiter straight after the opener", line: "<<EOF", i: 0, wantDelim: "EOF", wantN: 5},
		{name: "single-quoted delimiter", line: "<<'EOF'", i: 0, wantDelim: "EOF", wantN: 7},
		{name: "double-quoted delimiter", line: "<<\"EOF\"", i: 0, wantDelim: "EOF", wantN: 7},
		{name: "dash strips leading tabs", line: "<<-EOF", i: 0, wantDelim: "EOF", wantN: 6},
		{name: "space before the delimiter", line: "<< EOF", i: 0, wantDelim: "EOF", wantN: 6},
		{name: "space and quote before the delimiter", line: "<< 'EOF'", i: 0, wantDelim: "EOF", wantN: 8},
		{name: "trailing text after the opener is not its concern", line: "<<EOF extra", i: 0, wantDelim: "EOF", wantN: 5},

		// refusals: not a heredoc, the opener is reported as the empty string and
		// zero length so fleetLineScan treats the `<` as code instead of a heredoc.
		{name: "herestring is not a heredoc", line: "<<<", i: 0, wantDelim: "", wantN: 0},
		{name: "bare opener ends the line", line: "<<", i: 0, wantDelim: "", wantN: 0},
		{name: "only spaces, no delimiter word", line: "<< ", i: 0, wantDelim: "", wantN: 0},
		{name: "a quoted delimiter that never closes refuses the opener", line: "<<'EOF", i: 0, wantDelim: "", wantN: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDelim, gotN := fleetHeredocAt(tc.line, tc.i)
			assert.Equal(t, tc.wantN, gotN, "the opener's byte length")
			assert.Equal(t, tc.wantDelim, gotDelim, "the delimiter fleetLineScan holds against the body")
		})
	}

	// Through the seam the caller gates on: fleetLineScan names the heredoc and
	// treats its body as data, so a `mapfile` in a heredoc body is not flagged while
	// one on a real code line is.
	t.Run("heredoc delimiter flows back to fleetLineScan", func(t *testing.T) {
		t.Parallel()
		// the opener's delimiter is returned as the third value of fleetLineScan;
		// only the `<<EOF` opener is consumed, the `cat ` before it stays as code.
		bare, unquoted, delim := fleetLineScan("cat <<EOF")
		assert.Equal(t, "cat ", bare, "text before the heredoc opener is ordinary code")
		assert.False(t, unquoted, "heredoc openers carry no expansion to flag")
		assert.Equal(t, "EOF", delim, "the opener's delimiter is what fleetLineScan holds")
	})
}
