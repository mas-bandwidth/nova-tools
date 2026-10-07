package swarm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The card template carries the REPO: and BASE: lines nova-sprint land reads
// (ReadCardBase, the header only) with a line saying what they are for, so a
// card written from it names where its work lands; filled in, they are the
// repository and the base land and staging read.
func TestTheCardTemplateCarriesRepoAndBase(t *testing.T) {
	t.Parallel()
	body, err := Template("card")
	require.NoError(t, err)
	cb := ReadCardBase([]byte(body))
	assert.Equal(t, "<owner>/<name>", cb.Named, "REPO: is a header line of the template")
	assert.Equal(t, "<branch>", cb.Ref, "BASE: is a header line of the template")
	assert.Contains(t, body, "nova-sprint land merges the card's head onto BASE: (land --base stands in for a card naming no BASE:")

	filled := strings.NewReplacer("<owner>/<name>", "owner/name", "<branch>", "main").Replace(body)
	cb = ReadCardBase([]byte(filled))
	assert.Equal(t, "owner/name", cb.Named)
	assert.NotEmpty(t, cb.Repo)
	assert.Equal(t, CardRepoURL("owner/name"), cb.Repo)
	assert.Equal(t, "main", cb.Ref)
}

// The card template's STEP 4 gate names whose file failed, so an unchanged file
// that fails a test is checked against BASE rather than assumed to be the child's own.
func TestTheCardTemplateCarriesGateNamesWhoseFile(t *testing.T) {
	t.Parallel()
	body, err := Template("card")
	require.NoError(t, err)
	assert.Contains(t, body, GateNamesWhoseFile)
}

// The card template's STEP 1 carries the one GOCACHE sentence: the machine's shared,
// warm build cache is already set, so the child keeps it rather than exporting a cache
// of its own. cardgen.Render shares swarm.GoCacheLine with it.
func TestTheCardTemplateCarriesTheGoCacheLine(t *testing.T) {
	t.Parallel()
	body, err := Template("card")
	require.NoError(t, err)
	assert.Contains(t, body, GoCacheLine)
	assert.NotContains(t, body, "private GOCACHE")
	assert.NotContains(t, body, "GOCACHE=")
}

// The card template's Deadline line says the judgment of a card that runs past it is the coordinator's.
func TestTheCardTemplateCarriesDeadlineJudgmentLine(t *testing.T) {
	t.Parallel()
	body, err := Template("card")
	require.NoError(t, err)
	assert.Contains(t, body, "Deadline: finish within <n> minutes; the judgment of a card that runs past it is the coordinator's, so report what you have with the verdict not-done rather than push past it.")
}
