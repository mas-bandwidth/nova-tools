package typedrec_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// TestReadCardLineIsTheScannersLineOne: line 1 as bufio.Scanner read it for
// the wrapper before #4429: the bytes before the first newline, one
// trailing CR dropped, spaces kept; "" for an empty text and for a line of
// MaxCardLine bytes or more (the CR counts), so the commit subject falls
// back as it did.
func TestReadCardLineIsTheScannersLineOne(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("y", typedrec.MaxCardLine-1)
	for _, tc := range []struct{ in, want string }{
		{"", ""}, {"\n", ""}, {"\r\n", ""}, {"\r", ""},
		{"fix: x (#1)   \nDONE\n", "fix: x (#1)   "},
		{"fix: x (#1)\r\nDONE\r\n", "fix: x (#1)"},
		{"fix: x (#1)", "fix: x (#1)"}, {"fix: x (#1)\r", "fix: x (#1)"},
		{"fix: x (#1)\t\r\n", "fix: x (#1)\t"}, {"a\rb\n", "a\rb"}, {"a\r\r\n", "a\r"},
		{long, long}, {long + "\n", long}, {long + "\r\n", ""}, {long + "\nDONE\n", long},
		{long + "y", ""}, {long + "y\n", ""}, {strings.Repeat("y", 70000) + "\nDONE\n", ""},
	} {
		if got := typedrec.ReadCardLine([]byte(tc.in)); string(got) != tc.want {
			t.Errorf("ReadCardLine(%.30q...): len %d, want len %d", tc.in, len(got), len(tc.want))
		}
	}
}
