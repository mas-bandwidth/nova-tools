package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// batch_helpers_test.go holds the card fixtures both tiers write: the unit
// tests that read a cards file without running a batch, and the functional
// tests (batch_functional_test.go and its neighbours) that run one.

func itoa(n int) string { return strconv.Itoa(n) }

func writeCard(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCards(t *testing.T, dir string, cards [][2]string) string {
	t.Helper()
	var b strings.Builder
	for i, c := range cards {
		path := writeCard(t, dir, c[0]+".card", c[1])
		b.WriteString(c[0] + "\t" + itoa(i+1) + "\tmodel\t" + path + "\n")
	}
	path := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
