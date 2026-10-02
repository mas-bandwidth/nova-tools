package pkgselect

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxKeyedFile is the largest file read for a reference to a changed path: a
// fixture that big is data, not a test naming a doc.
const maxKeyedFile = 2 << 20

// keyedPackages is the packages a changed file that is not Go selects
// (nova-tools#5111): a test reads a doc, a golden or a script as text, and no
// import edge says so, so a change to that file moved the package's tests
// without moving the package. Two rules, both read from the tree at HEAD:
//
//   - a package whose _test.go files or testdata name a changed file
//     (referenced), and
//   - the package a changed file under its testdata belongs to (owned).
//
// A test names a file by its base name, as often as not through
// filepath.Join("..", "..", "docs", "X.md"), so the full path is not in the
// text; the base name is what is looked for, and only as a whole name: the
// character before it may not continue one (a letter, digit, `_`, `-` or `.`),
// so SPEC-X.md is not NOT-SPEC-X.md. A reference over-selects a package (a
// README.md names many) and never under-selects one. A non-test source file's
// mention does not select its package: its own change would have, and the
// dependents rule covers what imports it.
func (s *selector) keyedPackages(changed []string) (map[string]bool, error) {
	owned := map[string]bool{}
	names := map[string]bool{}
	for _, f := range changed {
		if strings.HasSuffix(f, ".go") {
			continue
		}
		names[path.Base(f)] = true
		if hasRootDir(f) {
			if dir, _, ok := strings.Cut(f, "/testdata/"); ok {
				owned["./"+dir] = true
			}
		}
	}
	if len(names) == 0 {
		return owned, nil
	}
	for _, root := range []string{"cmd", "internal", "tools"} {
		top := filepath.Join(s.o.Root, root)
		err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) && p == top {
					return nil
				}
				return err
			}
			if d.IsDir() {
				if n := d.Name(); n == "vendor" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") {
					return fs.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(s.o.Root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			var pkg string
			if dir, _, ok := strings.Cut(rel, "/testdata/"); ok {
				pkg = "./" + dir
			} else if strings.HasSuffix(rel, "_test.go") {
				pkg = "./" + path.Dir(rel)
			} else {
				return nil
			}
			if owned[pkg] || !d.Type().IsRegular() {
				return nil
			}
			if info, err := d.Info(); err != nil || info.Size() > maxKeyedFile {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if namesAny(string(b), names) {
				owned[pkg] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return owned, nil
}

// namesAny reports whether text holds any of the names as a whole name.
func namesAny(text string, names map[string]bool) bool {
	for n := range names {
		for from := 0; ; {
			i := strings.Index(text[from:], n)
			if i < 0 {
				break
			}
			i += from
			if i == 0 || !nameChar(text[i-1]) {
				return true
			}
			from = i + 1
		}
	}
	return false
}

func nameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}
