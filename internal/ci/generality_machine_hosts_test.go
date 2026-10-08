package ci

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGeneralityCommonWordMachineWitness pins the host reading of a machine name that
// is also a common English word: every host position is a finding —
// `<name>.local`, `@<name>`, `ssh <name>`, `<name>:` as host:path or host:port,
// `--machine <name>`, a hostname column (tab or markdown pipe separated),
// `/Users/<name>`, `/home/<name>` and `~<name>` — and the word in an English phrase
// passes as the word it is (docs/SPEC-CI.md#generality).
func TestGeneralityCommonWordMachineWitness(t *testing.T) {
	t.Parallel()

	// The word in an English phrase passes.
	for _, tc := range []struct{ name, line string }{
		{"phrase-before", "the space between the columns"},
		{"phrase-disk", "no disk space left on the runner"},
		{"phrase-leave", "leave space for the footer"},
		{"phrase-delimited", "a space-separated list of verbs"},
	} {
		t.Run("passes/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, extractGeneralityTokens(tc.line), "English phrase must pass: %q", tc.line)
		})
	}

	// Every host position is refused.
	for _, tc := range []struct{ name, line string }{
		{"dot-local", "host space.local"},
		{"at-name", "ping user@space"},
		{"ssh-name", "ssh space"},
		{"ssh-user-at-name", "ssh admin@space"},
		{"host-path", "rsync space:/var/log /tmp"},
		{"host-port", "redis space:6380"},
		{"machine-flag", "nova-pulse --machine space"},
		{"machine-flag-equals", "nova-pulse --machine=space"},
		{"column-tab", "space\tbench\tlinux"},
		{"column-tab-mid", "col1\tspace\tcol3"},
		{"column-pipe", "| space | bench | linux |"},
		{"users-path", "/Users/space/jobs"},
		{"home-path", "/home/space/jobs"},
		{"tilde-path", "cd ~space/repo"},
	} {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, extractGeneralityTokens(tc.line), "space", "host position must be refused: %q", tc.line)
		})
	}
}
