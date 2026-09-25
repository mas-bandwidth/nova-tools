package ci

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergedTreeGuards(t *testing.T) {
	file, err := os.Open(filepath.Join("testdata", "mergedtree-guards.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			t.Fatalf("invalid line: %s", line)
		}
		pkg := parts[0]
		testName := parts[1]
		desc := parts[2]

		t.Run(desc, func(t *testing.T) {
			// A merged-tree guard suite that runs only on the merged tree.
			// If the package doesn't exist, we skip it (treating it as PASS for this run).
			if _, err := os.Stat(filepath.Join("..", "..", pkg)); os.IsNotExist(err) {
				t.Logf("package %s missing, treating as pass for non-merged tree", pkg)
				return
			}
			cmd := exec.Command("go", "test", "./"+pkg, "-run", "^"+testName+"$")
			cmd.Dir = filepath.Join("..", "..")
			out, err := cmd.CombinedOutput()
			if err != nil {
				// We expect these scattered tests to exist and pass on the merged tree.
				t.Fatalf("guard %s failed in %s:\n%s", testName, pkg, out)
			}
		})
	}
}
