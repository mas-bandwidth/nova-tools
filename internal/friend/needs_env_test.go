package friend

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The names a daemon needs in its environment: the flag's, else the provider's key for
// --model; a provider not in the table needs the flag. Missing is what the environment
// answers empty for, and the reason is the owner's words, "no key sealed: NAME".
func TestNeedsEnvIsTheFlagsElseTheProvidersKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "INCEPTION_API_KEY", ProviderKeyEnv("inception/mercury-2.5"))
	assert.Equal(t, "DEEPSEEK_API_KEY", ProviderKeyEnv("deepseek/deepseek-v4.1-flash"))
	assert.Equal(t, "", ProviderKeyEnv("abliteration-ai/some-model"), "a provider the table does not know names no key: --needs-env names it")
	assert.Equal(t, "", ProviderKeyEnv("mercury-2.5"), "no provider, no key")
	assert.Equal(t, []string{"INCEPTION_API_KEY"}, NeedsEnvOf("", "inception/mercury-2.5"))
	assert.Equal(t, []string{"A_KEY", "B_KEY"}, NeedsEnvOf("B_KEY, A_KEY,,B_KEY", "inception/mercury-2.5"), "the flag wins, sorted, no duplicates")
	assert.Empty(t, NeedsEnvOf("", "abliteration-ai/x"))
	env := map[string]string{"A_KEY": "set"}
	assert.Equal(t, []string{"B_KEY"}, MissingEnv([]string{"A_KEY", "B_KEY"}, func(k string) string { return env[k] }))
	assert.Empty(t, MissingEnv([]string{"A_KEY"}, func(k string) string { return env[k] }))
	assert.Equal(t, []string{"A_KEY"}, MissingEnv([]string{"A_KEY"}, nil), "no environment reader: every name is missing")
	assert.Equal(t, "no key sealed: B_KEY,C_KEY", NeedsEnvReason([]string{"B_KEY", "C_KEY"}))
}
