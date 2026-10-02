package memindex

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetrievalKeepsSourceLinesAndOriginalText(t *testing.T) {
	t.Parallel()
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(ending, func(t *testing.T) {
			t.Parallel()
			text := "---\nname: rocket\ntype: reference\n---\n\n# Heading\n\n\nThe **Rocket Engine** burns hot fuel daily.\nIts flame is Bright.\n\nA second rocket paragraph burns cooler fuel."
			c, err := Build(fstest.MapFS{"a.md": {Data: []byte(strings.ReplaceAll(text, "\n", ending))}}, nil)
			require.NoError(t, err)
			for _, ch := range c.Chunks {
				assert.Equal(t, Normalize(ch.Original), ch.Text)
			}
			hits := Retrieve(c, []Channel{NewBM25(c)}, "bright", 1)
			require.Len(t, hits, 1)
			assert.Equal(t, 9, hits[0].Line)
			assert.Equal(t, "The **Rocket Engine** burns hot fuel daily.\nIts flame is Bright.", hits[0].Snippet)
			assert.Greater(t, hits[0].Para, 0)
			trigram := Retrieve(c, []Channel{NewTrigram(c)}, "rocket engine burns hot fuel daily", 1)
			require.Len(t, trigram, 1)
			assert.Equal(t, hits[0].Para, trigram[0].Para)
		})
	}
}
