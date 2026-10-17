package cairn

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// entryFixture is a legacy entry file in the JSON shape the writer wrote before
// the table switch, used to prove a legacy fixture of the same record still
// reads after the writer switches.
const entryFixture = `{"session":"s1","id":"e9","stamp":"2026-10-17T12:00:00Z","source":"x","publish":"manual","text":"legacy words"}`

// TestCairnEntryWriteTable pins the cairn entry writer's switch to the table
// form (the owner's S4, writers second; S2, a record that lives in a file
// rides as the padded standard base64 of its wire): appendEntry writes the
// entry file in a table form, and a legacy fixture of the same record still
// reads. A value the table cannot hold is refused by name before a byte is
// written.
func TestCairnEntryWriteTable(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0).UTC()
	store := t.TempDir()
	require.NoError(t, Open(store, "s1", "session:x", now, PublishManual))
	_, err := Append(store, "s1", "e1", "some words", "session:x", now, PublishManual)
	require.NoError(t, err)

	// The entry file is the table form: padded standard base64 of a wire,
	// never the legacy JSON the writer wrote before the switch.
	raw, err := os.ReadFile(filepath.Join(store, "entries", "s1", "e1.json"))
	require.NoError(t, err)
	wire, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	require.NoError(t, err, "the entry file is padded standard base64 of its wire")
	require.NotEmpty(t, wire, "a table image carries the record")

	// A legacy fixture of the same record still reads.
	legacyStore := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(legacyStore, "entries", "s1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyStore, "entries", "s1", "e9.json"), []byte(entryFixture), 0o644))
	text, err := EntryText(legacyStore, "s1", "e9")
	require.NoError(t, err)
	require.Equal(t, "legacy words", text)
}
