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
