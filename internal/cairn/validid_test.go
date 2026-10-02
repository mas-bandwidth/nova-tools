package cairn

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ValidID admits exactly the ids that name one file or directory of that name
// inside the store on every platform.
func TestValidIDAdmitsOnlyAnIDThatIsOnePathComponent(t *testing.T) {
	t.Parallel()
	for _, good := range []string{"s1", "a.b", ".hidden", "b9395d11", "2026-10-02_x", "con-test", "nul1", "COM10", "café"} {
		assert.True(t, ValidID(good), "%q refused", good)
	}
	for _, bad := range []string{"", ".", "..", "...", "a..b", "s.", "a/b", `a\b`, "a:b", "a*b", "a?b", `a"b`, "a<b", "a>b", "a|b",
		"s ", "a b", "a\tb", "a\x01b", "con", "Nul", "aux.txt", "COM1", "lpt9", "prn.md", string(make([]byte, 129))} {
		assert.False(t, ValidID(bad), "%q admitted", bad)
	}
}

// The defect this pins: a session of "." stored its entry as entries/e.json,
// which no index reads. It is refused before anything is written.
func TestADotSessionIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	require.Error(t, opened(Open(store, ".", "", now, PublishManual)))
	_, err := Append(store, ".", "e", "words", "", now, PublishManual)
	require.Error(t, err)
	_, _, err = Index(store, ".", 0)
	require.Error(t, err)
	entries, err := os.ReadDir(store)
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused id wrote to the store")
}
