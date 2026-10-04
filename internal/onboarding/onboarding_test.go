package onboarding

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The document this package parses is docs/TESTS.md, and the failure that put
// these tests here was not a parse error: it was a lookup that could not fail.
// docs/TESTS.md carried `## nova-work` twice; Section cut to the first match, so
// cmd/nova-work/firstrun_test.go executed the first section and the second one --
// which held a refusal sentence the binary had stopped printing and an events
// line that reached the real forge -- was read by no test at all for as long as
// it took two benches to find it by hand.

const twoSections = `# doc

## nova-alpha

### First run

` + "```text" + `
$ nova-alpha go
ALPHA OK n=1
` + "```" + `

### Refusals

` + "```text" + `
$ nova-alpha
nova-alpha: no verb given; run: nova-alpha help
` + "```" + `

## nova-beta

### First run

` + "```text" + `
$ nova-beta go
BETA OK n=1
` + "```" + `

## nova-alpha

### First run

` + "```text" + `
$ nova-alpha stale
ALPHA STALE
` + "```" + `
`

func TestSectionNamesKeepsOrderAndRepeats(t *testing.T) {
	t.Parallel()
	got := SectionNames(twoSections)
	want := []string{"nova-alpha", "nova-beta", "nova-alpha"}
	require.Equal(t, len(want), len(got))
	for i := range want {
		require.Equal(t, want[i], got[i])
	}
}

func TestRepeatedSectionsNamesTheOneWrittenTwice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		doc  string
		want []string
	}{
		{"document with repeats", twoSections, []string{"nova-alpha"}},
		{"healthy document", "## a\n\n## b\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RepeatedSections(tc.doc)
			require.Equal(t, tc.want, got)
		})
	}
}

// Repeated names are invisible to Section: it answers the first half. FirstRun
// of a repeated section also reads the first, so any later section unread becomes
// undocumented.
func TestSectionReadsOnlyTheFirstOfTwo(t *testing.T) {
	t.Parallel()
	body, ok := Section(twoSections, "nova-alpha")
	require.True(t, ok)
	lines, err := FirstRun(twoSections, "nova-alpha")
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Equal(t, "$ nova-alpha go", lines[0])
	require.NotContains(t, body, "ALPHA STALE")
}

func TestTranscriptReadsANamedSubsection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		doc  string
		tool string
		sub  string
		ok   bool
	}{
		{"valid subsection", twoSections, "nova-alpha", "Refusals", true},
		{"missing subsection", twoSections, "nova-beta", "Refusals", false},
		{"missing tool", twoSections, "nova-gamma", "First run", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines, err := Transcript(tc.doc, tc.tool, tc.sub)
			if tc.ok {
				require.NoError(t, err)
				require.Len(t, lines, 2)
				require.Equal(t, "$ nova-alpha", lines[0])
			} else {
				require.Error(t, err)
			}
		})
	}
}
