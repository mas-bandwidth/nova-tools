//go:build functional

package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// TestTableWritesNoFile is #3326's DONE-WHEN: without --out the sprint table
// is read from Redis and written nowhere. The other file modes (--fixture,
// --refresh pending) are unknown flags; a --redis --once render and a second
// start leave the working directory empty; and internal/sprinttable, whose
// Publish kept the last table on disk, exports no Publish.
func TestTableWritesNoFile(t *testing.T) {
	for _, flagArgs := range [][]string{
		{"--fixture", "table.txt"},
		{"--refresh", "pending"},
	} {
		args := append([]string{"table", "--redis", "127.0.0.1:1", "--once"}, flagArgs...)
		code, stdout, stderr := runSprint(args...)
		want := "flag provided but not defined: " + strings.Replace(flagArgs[0], "--", "-", 1)
		if code != 2 || stdout != "" || !strings.Contains(stderr, want) {
			t.Errorf("table %s: exit %d stdout %q stderr %q; want exit 2 and %q", flagArgs[0], code, stdout, stderr, want)
		}
	}

	sprinttablePkg, err := filepath.Abs(filepath.Join("..", "..", "internal", "sprinttable"))
	if err != nil {
		t.Fatal(err)
	}
	addr := startThrowawayRedis(t)
	loadTableFunction(t, addr)
	seed(t, addr, table.DefectFixture())
	dir := t.TempDir()
	t.Chdir(dir)
	for start := 1; start <= 2; start++ {
		code, stdout, stderr := runSprint("table", "--redis", addr, "--once")
		if code != 0 || !strings.Contains(stdout, "bench") {
			t.Fatalf("start %d: exit %d stdout %q stderr %q", start, code, stdout, stderr)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("start %d left files in the working directory: %v", start, names)
		}
	}

	pkgs, err := parser.ParseDir(token.NewFileSet(), sprinttablePkg, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if file.Scope.Lookup("Publish") != nil {
				t.Errorf("%s still declares sprinttable.Publish, the on-disk table", name)
			}
		}
	}
}
