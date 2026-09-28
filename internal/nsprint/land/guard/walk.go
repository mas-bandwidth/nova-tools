package guard

import (
	"os"
	"path/filepath"
	"strings"
)

// walkGo calls fn with every non-test .go file's repository-relative path
// and source, skipping .git, testdata, vendor and node_modules.
func walkGo(root string, fn func(file, src string)) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(rel(root, path), string(raw))
		return nil
	})
}
