package onboarding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the three functions of transcript.go the unit tier left at
// 0.0%: Elide, Problem.String and Problem.Error. Elide is the escape hatch for
// a normalisation the package has no constructor for, so a reader of a failing
// first-run test learns what was not compared even when the norm came from
// outside the named set; Problem's two methods are how the harness's complaint
// reaches a terminal, as a value and as an error. Each test names what it pins.

// Elide's main path: the name, the pattern and the replacement it is handed
// become a working Norm, and the norm it returns elides exactly what it says.
func TestTranscriptCoverElideReturnsANormThatElidesItsPattern(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc    string
		pattern string
		as      string
		in      string
		want    string
	}{
		{
			desc:    "a run id shaped as hex",
			pattern: `id=[0-9a-f]+`,
			as:      "id=<run>",
			in:      "PUT OK name=gate id=0f1e2d3c",
			want:    "PUT OK name=gate id=<run>",
		},
		{
			desc:    "every run of digits on the line",
			pattern: `[0-9]+`,
			as:      "<n>",
			in:      "LIST OK n=1 page=2 of 3",
			want:    "LIST OK n=<n> page=<n> of <n>",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()

			norm, err := Elide("the run's number", c.pattern, c.as)
			require.NoError(t, err, "Elide refused its own well-formed pattern %q: %v", c.pattern, err)
			assert.Equal(t, "the run's number", norm.Name, "Elide lost the name a reader is told is not compared")
			assert.Equal(t, c.want, Normalize(c.in, []Norm{norm}), "the norm Elide returned does not elide as declared")
		})
	}
}

// Elide's refusal: a pattern that is not a regexp is refused rather than
// quietly compared literally, which would pass a transcript whose value drifts
// in a way the reader was promised was not compared.
func TestTranscriptCoverElideRefusesAPatternThatIsNotARegexp(t *testing.T) {
	t.Parallel()

	norm, err := Elide("the run id", `id=(`, "id=<run>")
	require.Error(t, err, "Elide accepted the unterminated group %q; a broken norm is an unchecked transcript", `id=(`)
	assert.Equal(t, Norm{}, norm, "Elide returned a non-zero Norm alongside its error: %#v", norm)
}

// String and Error speak Problem.Message whole: the complaint the harness
// formats is the sentence a reader sees, and the zero Problem -- no message --
// says nothing rather than panicking.
func TestTranscriptCoverProblemSpeaksItsMessageAsValueAndError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc string
		p    Problem
		want string
	}{
		{
			desc: "the whole complaint, formatted by the harness",
			p:    Problem{Message: "line 2 of the run is not line 2 of the document"},
			want: "line 2 of the run is not line 2 of the document",
		},
		{
			desc: "the zero value says nothing",
			p:    Problem{},
			want: "",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, c.want, c.p.String(), "Problem.String() did not speak the message")
			assert.Equal(t, c.want, c.p.Error(), "Problem.Error() did not speak the message")
		})
	}
}

// The methods are exercised on a Problem Compare actually produced, so the
// coverage is of the sentence a real failing transcript prints, not only of a
// hand-built struct.
func TestTranscriptCoverProblemFromCompareReadsAsItsComplaint(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1"}}
	problems := Compare(step, Result{Stdout: "LIST OK n=2\n"}, nil)
	require.Len(t, problems, 1, "Compare found %d problems, want 1: %v", len(problems), problems)
	assert.Equal(t, problems[0].Message, problems[0].Error(), "a Problem from Compare speaks a different sentence as an error than as its message")
	assert.Contains(t, problems[0].String(), "LIST OK", "the complaint does not quote the lines that disagree: %s", problems[0].Message)
}
