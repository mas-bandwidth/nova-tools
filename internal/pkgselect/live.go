package pkgselect

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DeprecatedFile is the list of deprecated packages, relative to the
// repository root. It was deprecated/PACKAGES until the deprecated/ folder was
// removed (2026-10-01).
const DeprecatedFile = "internal/pkgselect/DEPRECATED"

// Deprecated is DeprecatedFile read: the packages that are deprecated and
// still in the tree because living tools import them. A path names that package
// and everything under it; a line `keep <path>` names one package under such a
// path that stays tested until it is lifted out into a shared module.
//
// DEPRECATED PACKAGES ARE NEVER TESTED: tests do not run for deprecated tools
// and modules, builds do not stop for them, and CI is not bogged down by them.
// Every place that chooses the packages a run tests reads its list through
// Live: Select, the hosted deal and the race-dependency build.
// internal/ci's TestDeprecatedPackagesAreNeverSelected holds them.
//
// A nil *Deprecated drops nothing (a checkout with no DeprecatedFile).
type Deprecated struct {
	module string // the module path; a listed import path under it is ./<rest>
	drop   []string
	keep   map[string]bool
}

// ParseDeprecated reads the text of DeprecatedFile: `#` starts a comment,
// blank lines are ignored, a `keep` line names a kept package and every other
// line names a dropped path. module is the module path, so a package listed by
// import path is read as the directory under it.
func ParseDeprecated(text, module string) *Deprecated {
	d := &Deprecated{module: module, keep: map[string]bool{}}
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.Trim(line, " \t\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "keep ") || strings.HasPrefix(line, "keep\t") {
			d.keep[strings.Trim(line[len("keep"):], " \t")] = true
			continue
		}
		d.drop = append(d.drop, line)
	}
	return d
}

// LoadDeprecated reads DeprecatedFile under root. A missing file is not an
// error: it is a nil *Deprecated, which keeps every package.
func LoadDeprecated(root string) (*Deprecated, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(DeprecatedFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseDeprecated(string(b), ModulePath(root)), nil
}

// LivePackage reports whether the package at p is live: p is an import path
// under the module, or ./<dir>, or <dir>. A keep line wins; otherwise a
// package is dropped when it is a listed path or under one (a name that only
// starts the same is another package).
func (d *Deprecated) LivePackage(p string) bool {
	if d == nil {
		return true
	}
	if d.module != "" {
		p = strings.TrimPrefix(p, d.module+"/")
	}
	p = strings.TrimPrefix(p, "./")
	if d.keep[p] {
		return true
	}
	for _, drop := range d.drop {
		if p == drop || strings.HasPrefix(p, drop+"/") {
			return false
		}
	}
	return true
}

// Live returns pkgs without the deprecated ones, each line unchanged and in
// order.
func (d *Deprecated) Live(pkgs []string) []string {
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		if d.LivePackage(p) {
			out = append(out, p)
		}
	}
	return out
}
