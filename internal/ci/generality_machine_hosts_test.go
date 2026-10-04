package ci

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGeneralityCommonWordMachineWitness pins that machine names that are also
// common English words (such as 'space') are matched only in host positions
// (<name>.local, @<name>, ssh <name>, <name>: as a host:path, --machine <name>,
// a hostname column, /Users/<name>, ~<name>), and pass as regular English words.
func TestGeneralityCommonWordMachineWitness(t *testing.T) {
	t.Parallel()

	// The English word passes
	assert.Empty(t, extractGeneralityTokens("the space between"), "English word 'space' must pass")
	assert.Empty(t, extractGeneralityTokens("white-space: nowrap"), "CSS 'white-space' must pass")
	assert.Empty(t, extractGeneralityTokens("space := true"), "identifier 'space' must pass")

	// Host positions are refused
	assert.Contains(t, extractGeneralityTokens("ssh space"), "space", "ssh <name> must be refused")
	assert.Contains(t, extractGeneralityTokens("host space.local"), "space", "<name>.local must be refused")
	assert.Contains(t, extractGeneralityTokens("ping user@space"), "space", "@<name> must be refused")
	assert.Contains(t, extractGeneralityTokens("rsync space:/var/log /tmp"), "space", "<name>:path must be refused")
	assert.Contains(t, extractGeneralityTokens("redis space:6380"), "space", "<name>:port must be refused")
	assert.Contains(t, extractGeneralityTokens("nova-pulse --machine space"), "space", "--machine <name> must be refused")
	assert.Contains(t, extractGeneralityTokens("nova-pulse --machine=space"), "space", "--machine=<name> must be refused")
	assert.Contains(t, extractGeneralityTokens("space\tbench\tlinux"), "space", "hostname column must be refused")
	assert.Contains(t, extractGeneralityTokens("col1\tspace\tcol3"), "space", "hostname column must be refused")
	assert.Contains(t, extractGeneralityTokens("/Users/space/jobs"), "space", "/Users/<name> must be refused")
	assert.Contains(t, extractGeneralityTokens("/home/space/jobs"), "space", "/home/<name> must be refused")
	assert.Contains(t, extractGeneralityTokens("cd ~space/repo"), "space", "~<name> must be refused")
}
