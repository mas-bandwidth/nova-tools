package selftalk

import "testing"

func TestLocationFlatteningKeepsTheExistingText(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"", " \t\n", "# heading\r\n\r\n**I cannot\ncheck my own work.**",
		"\u2003I cannot check.\u2003", "café\t\r\n私 I am fallible.", "a\vb\fc",
		string([]byte{'a', 0xff, '\n', 'b'}),
	} {
		got, lines := flattenWithLines(text)
		if want := Flatten(text); got != want || len(lines) != len(got) {
			t.Errorf("text %q: got %q (%d locations), want %q", text, got, len(lines), want)
		}
	}
}
