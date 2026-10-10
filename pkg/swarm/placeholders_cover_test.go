package swarm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Coverage for placeholders.go: UnfilledTemplateLines is pure parsing over a card and the
// card template (templates.go), so the unit tier reaches it with no repository, store or
// subprocess. The tests are the tool ledger W12 pins named on the function's own comment:
// the template's unfilled <...> lines are named, everything else — a filled-in line, a
// card's own angle text, a blank — is not.

// templateUnfilledLine is the template's one line opening with the prefix, trimmed: the
// test cards are cut from the printed template, so a reworded template moves these
// findings with it instead of silently missing the line.
func templateUnfilledLine(t *testing.T, prefix string) string {
	t.Helper()
	var got []string
	for _, l := range strings.Split(templateCard, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, prefix) {
			got = append(got, l)
		}
	}
	require.Len(t, got, 1, "the template holds exactly one line opening with %q", prefix)
	require.Contains(t, got[0], "<", "the line opening with %q is a fill-in", prefix)
	return got[0]
}

func TestPlaceholdersCoverUnfilledTemplateLines(t *testing.T) {
	t.Parallel()

	require.Equal(t, "placeholder", PlaceholderCheck)
	resultLine := templateUnfilledLine(t, "RESULT: ")
	repoLine := templateUnfilledLine(t, "REPO: ")
	baseLine := templateUnfilledLine(t, "BASE: ")

	for _, tc := range []struct {
		name string
		card string
		want []CardHeaderFinding
	}{
		{
			name: "unfilledHeaderLinesNamedInOrder",
			card: strings.Join([]string{resultLine, repoLine, baseLine}, "\n"),
			want: []CardHeaderFinding{
				{Check: PlaceholderCheck, Line: 1, Excerpt: resultLine},
				{Check: PlaceholderCheck, Line: 2, Excerpt: repoLine},
				{Check: PlaceholderCheck, Line: 3, Excerpt: baseLine},
			},
		},
		{
			name: "indentedLineMatchesTrimmedAndKeepsItsOwnExcerpt",
			card: "THE TASK. filled in.\n\t" + repoLine,
			want: []CardHeaderFinding{
				{Check: PlaceholderCheck, Line: 2, Excerpt: "\t" + repoLine},
			},
		},
		{
			name: "sameUnfilledLineTwiceNamedTwice",
			card: repoLine + "\n" + repoLine,
			want: []CardHeaderFinding{
				{Check: PlaceholderCheck, Line: 1, Excerpt: repoLine},
				{Check: PlaceholderCheck, Line: 2, Excerpt: repoLine},
			},
		},
		{
			name: "blankLineIsSkippedButStillCounted",
			card: repoLine + "\n   \n" + repoLine,
			want: []CardHeaderFinding{
				{Check: PlaceholderCheck, Line: 1, Excerpt: repoLine},
				{Check: PlaceholderCheck, Line: 3, Excerpt: repoLine},
			},
		},
		{
			name: "filledInCardNamesNothing",
			card: "RESULT: cover-internal-swarm-placeholders sha=b9f9e0b100ca tier: flash\n" +
				"REPO: mas-bandwidth/nova-tools\n" +
				"BASE: sprint/mechanical-2026-10-02",
			want: nil,
		},
		{
			name: "cardOwnAngleTextNotFromTemplateNamesNothing",
			card: "THE TASK. Run go test --count <n> once.\nLibraries considered: the Go standard library.",
			want: nil,
		},
		{
			name: "emptyCardNamesNothing",
			card: "",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, UnfilledTemplateLines(tc.card))
		})
	}
}

func TestPlaceholdersCoverUnfilledTemplateLinesOnTheWholeTemplate(t *testing.T) {
	t.Parallel()

	// The template printed unfilled is the card the ledger names (W12): every line still
	// holding its fill-ins is found, in line order, none of the template's own plain
	// lines among them.
	findings := UnfilledTemplateLines(templateCard)
	require.NotEmpty(t, findings)
	assert.Equal(t, CardHeaderFinding{Check: PlaceholderCheck, Line: 1, Excerpt: templateUnfilledLine(t, "RESULT: ")}, findings[0])
	prev := 0
	for _, f := range findings {
		assert.Equal(t, PlaceholderCheck, f.Check)
		assert.Greater(t, f.Line, prev, "findings rise with the card's lines")
		prev = f.Line
		assert.Contains(t, f.Excerpt, "<", "a named line is one of the template's fill-ins")
	}
}
