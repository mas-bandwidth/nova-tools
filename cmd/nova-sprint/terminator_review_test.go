package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerReviewTerminatorKeepsFollowingWordsLiteral(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"file after id", []string{"--stream", "s1", "--", "card1", "--brief-file", "brief.txt"}, []string{"card1", "--brief-file", "brief.txt"}},
		{"flag after literal dash word", []string{"--stream", "s1", "--", "--literal", "--brief-file", "brief.txt"}, []string{"--literal", "--brief-file", "brief.txt"}},
		{"terminator is a flag value", []string{"--stream", "s1", "--brief", "--", "card1"}, []string{"card1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := verbFlags("add")
			require.NotNil(t, fs)
			pos, err := parse(fs, tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, pos)
			assert.Empty(t, fs.Lookup("brief-file").Value.String(), "literal words must not become file flags")
		})
	}
}

func TestServerReviewForwardingDoesNotConsumeAFileAfterTerminator(t *testing.T) {
	t.Parallel()
	argv := []string{"add", "--stream", "s1", "--", "card1", "--brief-file", "brief.txt"}
	got := absolutePaths(append([]string(nil), argv...))
	assert.Equal(t, argv, got, "literal words are not paths to rewrite")
	v := readVerb(got)
	require.NoError(t, v.err)
	require.NotNil(t, v.fs)
	assert.Empty(t, v.fs.Lookup("brief-file").Value.String(), "the server must not read a literal word as a relative file")
}
