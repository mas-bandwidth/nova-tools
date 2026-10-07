package onboarding

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

	got := RepeatedSections(twoSections)
	require.Equal(t, []string{"nova-alpha"}, got)
	none := RepeatedSections("## a\n\n## b\n")
	require.Empty(t, none)
}

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

	lines, err := Transcript(twoSections, "nova-alpha", "Refusals")
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Equal(t, "$ nova-alpha", lines[0])
	_, err = Transcript(twoSections, "nova-beta", "Refusals")
	require.Error(t, err)
	_, err = Transcript(twoSections, "nova-gamma", "First run")
	require.Error(t, err)
}
