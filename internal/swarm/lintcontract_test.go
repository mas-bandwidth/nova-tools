package swarm

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CONTRACT LINE IS ONE LINE, AND THREE READERS HAVE TO AGREE ON IT (issue #1741).
//
// `cut` renders line 1, `lint --card` checks it on the bench, and `gather` compares the
// `RESULT.md`'s line 1 against the card's. They did not agree: `cut` writes and accepts
// `RESULT <label> sha=<sha12>` (the deleted internal/pulse cut), while the lint's `result-first`
// wanted `RESULT: ` with a colon (WORKER-CARDS.md practice 1, now in the nova-work-old repository). Every card `cut`
// writes therefore drew a `result-first` drift, which is how six cards written this
// session each drew one.
//
// This is a CLASS test, not a card's: it reads the other two readers where they live
// rather than restating them, so the day one of them changes form this goes red. It
// cannot import internal/pulse -- that package imports THIS one -- so it reads its
// source as text, which is the same thing a class test over a document does.

// cardCutDocPath holds the card contract line as a card's line 1 carries it.
const (
	cardCutDocPath = "testdata/card-contract-line.txt"
)

func readOr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "this class test reads %s: %v", path, err)
	return string(b)
}

// The card contract line in testdata is a line the lint accepts.
func TestTheDocumentsContractLineIsOneTheLintAccepts(t *testing.T) {
	t.Parallel()

	doc := readOr(t, cardCutDocPath)
	want := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "RESULT ") || strings.HasPrefix(line, "RESULT: ") {
			want = line
			break
		}
	}
	require.NotEmpty(t, want, "%s no longer shows a RESULT line 1; this test reads that example", cardCutDocPath)
	require.True(t, IsCardContractLine(want), "the lint refuses the very line 1 the document says `cut` writes: %q\nthe lint accepts %v", want, CardContractPrefixes)
}

// BOTH FORMS ARE ACCEPTED, AND THAT IS A STOPGAP. `cut` writes one form and
// WORKER-CARDS.md practice 1 names the other; until #1741 settles one of them the
// lint accepts both, because refusing either one refuses real cards. When #1741 closes,
// one of these two lines goes.
func TestBothContractFormsAreAcceptedUntilTheIssueSettlesIt(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"RESULT card-1 sha=0123456789ab",
		"RESULT: CARD-0000 do the thing",
	} {
		assert.True(t, IsCardContractLine(line), "the lint refuses %q, which a tool on dev writes today", line)
	}
	for _, line := range []string{
		"# fixed: row 5 writer_bound_count on go",
		"RESULTS card-1 sha=0123456789ab",
		"RESULT",
		" RESULT card-1 sha=0123456789ab",
	} {
		assert.False(t, IsCardContractLine(line), "%q is not a contract line and the lint takes it for one", line)
	}
}

// THE RULING IS SAID IN ONE PLACE, AND IT SAYS ONE FORM (SPEC-TOOLWORK.md §5 rule 7).
//
// Before the ruling the remedy named the two forms side by side -- "as `cut` writes it, or
// ... as practice 1 writes it" -- and a card writer reading it could not tell which to
// type. Five managers on 2026-09-19 each decided for themselves, and three of them decided
// wrong. The colon form won; the colon-less one is a stopgap the rule-7 class test retires.
// This test holds the message to that, so the accept-both stopgap can never quietly become
// a second rule.
func TestTheContractRemedyNamesOneFormAndCallsTheOtherAStopgap(t *testing.T) {
	t.Parallel()

	require.Equal(t, "RESULT: ", CardContractPrefixes[0], "the colon form is the rule, so it is first: %v", CardContractPrefixes)
	require.Contains(t, CardContractWanted, "`RESULT: <label> sha=<sha12>`", "the remedy names the colon form as the one to write: %q", CardContractWanted)
	require.Contains(t, CardContractWanted, "stopgap", "the remedy calls the colon-less form a stopgap, not a second rule: %q", CardContractWanted)
	// A remedy that offers two forms with an `or` between them is the wording the shift
	// could not act on.
	require.NotContains(t, CardContractWanted, "sha=<sha12>` as `cut` writes it, or ", "the remedy no longer offers a choice of two: %q", CardContractWanted)
	// Both forms are still READ, because the plain `cut` template still renders one.
	for _, line := range []string{"RESULT: c-1 sha=0123456789ab", "RESULT c-1 sha=0123456789ab"} {
		require.True(t, IsCardContractLine(line), "until rule 7's renderer card lands both forms are read: %q", line)
	}
}
