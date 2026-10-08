package selftalk

import "testing"

// TestLocationFlatteningKeepsTheExistingText pins one source line per flattened byte for inputs from
// empty to invalid UTF-8, all through the package rig.
func TestLocationFlatteningKeepsTheExistingText(t *testing.T) {
	t.Parallel()
	rig := NewRig(t)
	for _, text := range []string{
		"", " \t\n", "# heading\r\n\r\n**I cannot\ncheck my own work.**",
		" I cannot check. ", "café\t\r\n私 I am fallible.", "a\vb\fc",
		string([]byte{'a', 0xff, '\n', 'b'}),
	} {
		got, lines := rig.FlattenWithLines(text)
		rig.Locations(text, got, lines)
	}
}
