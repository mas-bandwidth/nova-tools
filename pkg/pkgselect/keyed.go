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
// (nova-tools#5111): a test reads a doc, a golden or a script as text, a
// package embeds a file as its own data, and no import edge says so, so a
// change to that file moved the package's tests without moving the package.
// Three rules, all read from the tree at HEAD:
//
//   - a package whose _test.go files or testdata name a changed file
//     (referenced),
//   - the package a changed file under its testdata belongs to (owned), and
//   - a package whose //go:embed directive names a changed file (embedded).
//
// A test names a file by its base name, as often as not through
// filepath.Join("..", "..", "docs", "X.md"), so the full path is not in the
// text; the base name is what is looked for, and only as a whole name: the
// character before it may not continue one (a letter, digit, `_`, `-` or `.`),
// so SPEC-X.md is not NOT-SPEC-X.md. A reference over-selects a package (a
// README.md names many) and never under-selects one. A non-test source file's
// mention does not select its package: its own change would have, and the
// dependents rule covers what imports it. An embed pattern is package-relative
// and cannot reach outside the package directory, so a changed file matches a
// directive only under that directory.
func (s *selector) keyedPackages(changed []string) (map[string]bool, error) {
	owned := map[string]bool{}
	names := map[string]bool{}
	paths := map[string]bool{}
	for _, f := range changed {
		if strings.HasSuffix(f, ".go") {
			continue
		}
		names[path.Base(f)] = true
		paths[f] = true
		if hasRootDir(f) {
			if dir, _, ok := strings.Cut(f, "/testdata/"); ok {
				owned["./"+dir] = true
			}
		}
	}
	if len(names) == 0 {
		return owned, nil
	}
	for _, root := range []string{"cmd", "internal", "pkg", "tools"} {
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
			var pkg, srcDir string
			if dir, _, ok := strings.Cut(rel, "/testdata/"); ok {
				pkg = "./" + dir
			} else if strings.HasSuffix(rel, ".go") {
				pkg, srcDir = "./"+path.Dir(rel), path.Dir(rel)
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
			if srcDir != "" && embedsAny(string(b), srcDir, paths) {
				owned[pkg] = true
				return nil
			}
			if (srcDir == "" || strings.HasSuffix(rel, "_test.go")) && namesAny(string(b), names) {
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

// embedsAny reports whether the source text of the package directory dir holds
// a //go:embed directive naming one of the changed paths. The pattern is read
// relative to dir: exact for a plain name, path.Match for the glob characters
// the Go tool allows, and a name or directory pattern also matches what lives
// under it (an all: pattern embeds a directory recursively). Like a reference,
// this over-selects and never under-selects.
func embedsAny(text, dir string, paths map[string]bool) bool {
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), "//go:embed")
		if !ok || rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		for _, p := range strings.Fields(rest) {
			p = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(p, "all:"), "embed:"), "/")
			if p == "" {
				continue
			}
			for f := range paths {
				rel, ok := strings.CutPrefix(f, dir+"/")
				if !ok {
					continue
				}
				if p == rel {
					return true
				}
				if strings.ContainsAny(p, "*?[]") {
					if m, err := path.Match(p, rel); err == nil && m {
						return true
					}
					continue
				}
				if strings.HasPrefix(rel, p+"/") {
					return true
				}
			}
		}
	}
	return false
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
