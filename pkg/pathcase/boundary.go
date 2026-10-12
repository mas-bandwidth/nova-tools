package pathcase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// measuredFold is the GROUND TRUTH about one directory: a file written INSIDE
// it and asked for again in another case. ok is false where the directory would
// not take the write, which is the one case no measurement can answer.
func measuredFold(dir string) (folds, ok bool) {
	f, err := os.CreateTemp(dir, "GroundTruth")
	if err != nil {
		return false, false
	}
	// ignored: the ground-truth probe is closed before it is looked up; a close error cannot change what lookup answers.
	f.Close()
	name := f.Name()
	// ignored: the ground-truth probe is removed either way; a remove error cannot change what lookup answers.
	defer os.Remove(name)
	orig, err := os.Lstat(name)
	if err != nil {
		return false, false
	}
	other, err := os.Lstat(filepath.Join(dir, strings.ToLower(filepath.Base(name))))
	return err == nil && os.SameFile(orig, other), true
}

// nameFoldsInParent is the answer the REMOVED shortcut gave: the spelling of
// the directory's own name, looked up in its PARENT. It is kept here as the
// wrong answer the boundary test is about.
func nameFoldsInParent(dir string) (folds, ok bool) {
	base := filepath.Base(dir)
	lower := strings.ToLower(base)
	if lower == base {
		return false, false
	}
	orig, err := os.Lstat(dir)
	if err != nil {
		return false, false
	}
	other, err := os.Lstat(filepath.Join(filepath.Dir(dir), lower))
	return err == nil && os.SameFile(orig, other), true
}

// CaseBoundaryDir finds a directory whose OWN NAME folds one way in its parent
// while lookups INSIDE it fold the other way -- a MOUNT BOUNDARY, the one place
// the two can differ (the case-sensitive-image setup this codebase already uses:
// `TMPDIR=<volume>/tmp` puts the volume root on that path). It looks along the
// ancestors of t.TempDir() up to one level above os.TempDir(), and the bound
// keeps every probe write inside temp directories and their volume root. Where
// no boundary is reachable it returns found=false.
func CaseBoundaryDir(t *testing.T) (dir string, folds bool, found bool) {
	t.Helper()
	stop := filepath.Dir(filepath.Clean(os.TempDir()))
	for d := t.TempDir(); ; {
		inside, okInside := measuredFold(d)
		byName, okName := nameFoldsInParent(d)
		if okInside && okName && inside != byName {
			return d, inside, true
		}
		if d == stop {
			return "", false, false
		}
		up := filepath.Dir(d)
		if up == d {
			return "", false, false
		}
		d = up
	}
}
