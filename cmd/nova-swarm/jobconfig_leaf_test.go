package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// TestAJobConfigWriteFailureNamesTheFile: each of writeJobConfig's two write failures names
// its own thing. writeJobConfig makes dataHome/.config/opencode and then writes
// opencode.json into it, and the two failures are two facts an operator acts on
// differently: a directory that could not be MADE, and the leaf that could not be WRITTEN.
// The directory refusal therefore names the directory and says made, and the write refusal
// names opencode.json and says written, each carrying the underlying error escaped onto the
// same one line -- the shape copyAuth gives its own two writes (docs/STANDARD.md section 2,
// the status word leads every line: a refusal is one line and a typed value in it is
// oneline.Field; section 3, ONBOARDING point 2: a refusal says what it wants, so recovery
// takes one turn).
func TestAJobConfigWriteFailureNamesTheFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// plant puts the data home in the state that fails one of the two operations and
		// returns the path that operation names and the error it fails with: the two the
		// refusal carries.
		plant func(t *testing.T, dataHome string) (string, error)
		// wantWord is what the refusal says failed to happen to the path it names; notWord
		// is the other branch's word, whose presence sends an operator to the wrong place.
		wantWord string
		notWord  string
	}{
		{
			// opencode.json is a directory: the mkdir of its own directory succeeds and the
			// write of the leaf fails, so the reason read here is the write's own.
			name: "the_leaf_could_not_be_written",
			plant: func(t *testing.T, dataHome string) (string, error) {
				leaf := filepath.Join(dataHome, ".config", "opencode", "opencode.json")
				require.NoError(t, os.MkdirAll(leaf, 0o755), "the leaf is planted as a directory, so the mkdir succeeds and the write fails")
				return leaf, os.WriteFile(leaf, []byte("{}"), 0o600)
			},
			wantWord: "could not be written",
			notWord:  "could not be made",
		},
		{
			// A regular file stands where the config directory goes: the mkdir fails and no
			// write is attempted, so the reason read here is the directory's own.
			name: "the_directory_could_not_be_made",
			plant: func(t *testing.T, dataHome string) (string, error) {
				require.NoError(t, os.WriteFile(filepath.Join(dataHome, ".config"), []byte("a regular file, not a directory\n"), 0o644), "the config directory's parent is planted as a regular file")
				dir := filepath.Join(dataHome, ".config", "opencode")
				return dir, os.MkdirAll(dir, 0o755)
			},
			wantWord: "could not be made",
			notWord:  "could not be written",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dataHome := t.TempDir()
			failed, err := tc.plant(t, dataHome)
			require.Error(t, err, "the planted data home fails the operation on %s", failed)

			var notes bytes.Buffer
			sha, reason, proxy := writeJobConfig(nativeRunConfig{}, "fake", dataHome, t.TempDir(), nil, &notes)
			require.Nil(t, proxy, "a config naming no provider entry opens no read-deadline proxy")
			require.Empty(t, sha, "a refused config write records no sha8")
			require.NotEmpty(t, reason, "writeJobConfig refuses when %s fails", failed)

			assert.Contains(t, reason, oneline.Field(failed), "the refusal names the path that failed: %q", reason)
			assert.Contains(t, reason, tc.wantWord, "the refusal says what failed to happen to it: %q", reason)
			assert.NotContains(t, reason, tc.notWord, "the refusal reads as the other branch's failure, so an operator looks in the wrong place: %q", reason)
			assert.Contains(t, reason, oneline.Escape(err.Error()), "the refusal carries the underlying error: %q", reason)
			assert.NotContains(t, reason, "\n", "the refusal is one line: %q", reason)
		})
	}
}

// A local route's job config declares the provider local at the serving machine's
// endpoint, its model under it, and no key; the read-deadline proxy stands in front of it
// as of any provider with an http baseURL (docs/SPEC-LOCAL.md, "Fleet").
func TestAJobOnALocalRouteDeclaresTheProviderAtItsServingMachine(t *testing.T) {
	t.Parallel()
	dataHome := t.TempDir()
	var notes bytes.Buffer
	sha, reason, proxy := writeJobConfig(nativeRunConfig{model: "local/gemma4-32k", localBase: "http://g1.test:11434/v1"}, "local", dataHome, t.TempDir(), nil, &notes)
	require.Empty(t, reason)
	require.NotEmpty(t, sha)
	require.NotNil(t, proxy, "the read-deadline proxy stands in front of the serving machine")
	t.Cleanup(func() { _ = proxy.Close() }) // ignored: the test's own proxy, closed at its end
	raw, err := os.ReadFile(filepath.Join(dataHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)
	var cfg struct {
		Provider map[string]struct {
			NPM     string         `json:"npm"`
			Options map[string]any `json:"options"`
			Models  map[string]any `json:"models"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	local := cfg.Provider["local"]
	assert.Equal(t, "@ai-sdk/openai-compatible", local.NPM)
	assert.Contains(t, local.Models, "gemma4-32k")
	assert.NotContains(t, local.Options, "apiKey")
	assert.Equal(t, proxy.HarnessURL(), local.Options["baseURL"])
}
