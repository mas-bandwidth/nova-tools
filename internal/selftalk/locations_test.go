package selftalk

import (
	"testing"
)

// Every byte of the flattened text has its source line, whatever the input holds.
func TestLocationFlatteningKeepsTheExistingText(t *testing.T) {
	t.Parallel()
	rig := NewRig(t)
	for _, text := range []string{
		"", " \t\n", "# heading\r\n\r\n**I cannot\ncheck my own work.**",
		" I cannot check. ", "café\t\r\n私 I am fallible.", "a\vb\fc",
		string([]byte{'a', 0xff, '\n', 'b'}),
	} {
		got, lines := rig.FlattenWithLines(text)
		rig.Locations(text, got, lines)
	}
}
