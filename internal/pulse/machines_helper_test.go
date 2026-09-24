package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// machinesFile writes a small registry: every name in benches is a bench, every name in
// runners is a CI-only runner host.
func machinesFile(t *testing.T, dir string, benches, runners []string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("# name\tssh\tos/arch\troles\tseat\tcores\tnotes\n")
	for _, name := range benches {
		b.WriteString(name + "\t" + name + "\tlinux/x64\tbench\tswarm-" + name + "\t64\t-\n")
	}
	for _, name := range runners {
		b.WriteString(name + "\t" + name + "\tdarwin/amd64\trunner\t-\t8\tCI-only\n")
	}
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
