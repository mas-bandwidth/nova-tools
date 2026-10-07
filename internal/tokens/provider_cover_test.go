package tokens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProviderCoverKnownParser pins the label-to-parser gate: every kind the tool
// ships is known, and a kind that names no parser is refused rather than guessed at.
func TestProviderCoverKnownParser(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kind string
		want bool
	}{
		{"google", "google", true},
		{"openai", "openai", true},
		{"xai", "xai", true},
		{"unlisted", "anthropic", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, KnownParser(tc.kind), "KnownParser(%q)", tc.kind)
		})
	}
}

// TestProviderCoverParserColumns pins that one parser names its own sorted columns and
// stamps them in the refusal's own order, while an unknown kind has none to name.
func TestProviderCoverParserColumns(t *testing.T) {
	t.Parallel()

	want := []string{"timestamp or date", "cache_read", "cache_write", "input", "model", "output", "reasoning"}
	assert.Equal(t, want, ParserColumns("xai"), "xai columns")
	assert.Nil(t, ParserColumns("anthropic"), "an unknown parser names no columns")
}


 TestProviderCoverXaiUsageErrorRefusals pins both xAI path refusals: a missing path
// says what the flag wants and unwraps to os.ErrNotExist, and a non-regular path is
// refused rather than walked.
func TestProviderCoverXaiUsageErrorRefusals(t *testing.T) {
	t.Parallel()

	missing := &XaiUsageMissingError{Path: "/tmp/usage.json"}
	assert.Contains(t, missing.Error(), "not there")
	assert.ErrorIs(t, missing, os.ErrNotExist)

	notFile := &XaiUsageNotFileError{Path: "/tmp"}
	assert.Contains(t, notFile.Error(), "not one file")
}

// TestProviderCoverProviderJSONReason pins the refusal for JSON that is neither known
// shape: it names the parser and both shapes it reads.
func TestProviderCoverProviderJSONReason(t *testing.T) {
	t.Parallel()

	reason := providerJSONReason("xai")
	assert.Contains(t, reason, "xai")
	assert.Contains(t, reason, "comma-separated")
	assert.Contains(t, reason, "grok usage JSON")
}
