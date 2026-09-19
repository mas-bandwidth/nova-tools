package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs/TESTS.md promises that every block is real output pasted whole -- type these
// lines, see these lines, in this order and no others. The `## nova-secrets` section's
// first-run block documents `keygen` with five lines, but the receipt builder prints
// seven; the last two are the plain closing lines that tell a first-time reader what to
// do next, which is the whole point of the section.
//
// keygenLines is called directly because it is a pure string formatter: this test must
// never make, read or print a key. The age1... values are public keys the document
// already carries.
func TestTheKeygenTranscriptIsEveryLineTheReceiptPrints(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")

	start := -1
	for i, l := range lines {
		if strings.TrimRight(l, "\r") == "## nova-secrets" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("docs/TESTS.md has no `## nova-secrets` section")
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	section := lines[start+1 : end]

	cmd := -1
	for i, l := range section {
		if strings.HasPrefix(l, "$ nova-secrets keygen ") {
			cmd = i
			break
		}
	}
	if cmd < 0 {
		t.Fatal("docs/TESTS.md has no `$ nova-secrets keygen ` command in the `## nova-secrets` section")
	}
	var documented []string
	for _, l := range section[cmd+1:] {
		if strings.HasPrefix(l, "$ ") || strings.HasPrefix(l, "```") || strings.TrimSpace(l) == "" {
			break
		}
		documented = append(documented, l)
	}
	if len(documented) == 0 {
		t.Fatal("docs/TESTS.md documents no output lines for `nova-secrets keygen`")
	}

	printed := keygenLines(
		"rowan",
		"/Users/me/.config/nova-secrets/rowan.key",
		"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk",
		"age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata",
		false,
	)

	if len(documented) != len(printed) {
		t.Errorf("docs/TESTS.md documents %d line(s) where nova-secrets keygen prints %d; the document is missing:\n%s",
			len(documented), len(printed), strings.Join(printed[len(documented):], "\n"))
	}
	for i := 0; i < len(documented) && i < len(printed); i++ {
		if documented[i] != printed[i] {
			t.Errorf("line %d differs:\n  documented: %q\n  printed:    %q", i+1, documented[i], printed[i])
		}
	}
}
