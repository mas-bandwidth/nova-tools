package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A harness is known by the name of its program, wherever it lives and whatever its
// extension; any other program is opencode, launched through the providers table.
func TestAHarnessIsKnownByItsProgramName(t *testing.T) {
	t.Parallel()
	for bin, want := range map[string]string{
		"claude":                       Claude,
		"/Users/g/.local/bin/codex":    Codex,
		"/Users/g/.grok/bin/grok":      Grok,
		"grok.exe":                     Grok,
		"/usr/local/bin/opencode":      OpenCode,
		"./harness":                    OpenCode,
		"/opt/claude-code/bin/claudia": OpenCode,
	} {
		assert.Equal(t, want, KindOf(bin), bin)
	}
	for _, k := range Headless {
		assert.True(t, IsHeadless(k), k)
	}
	assert.False(t, IsHeadless(OpenCode), "opencode is not headless")
}

// Each headless harness has a provider word of its own, so a provider's rest never spans
// two harnesses; any other kind has none.
func TestEachHeadlessHarnessHasAProviderOfItsOwn(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	for _, k := range Headless {
		p := ProviderOf(k)
		assert.Equal(t, "subscription-"+k, p)
		assert.NotContains(t, seen, p, "%s shares a provider with %s", k, seen[p])
		seen[p] = k
	}
	assert.Empty(t, ProviderOf(OpenCode))
	assert.Empty(t, ProviderOf("nonesuch"))
}
