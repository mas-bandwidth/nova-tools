// Package pathcase measures whether a filesystem folds the case of names: the
// one place in this codebase where a probe file is written and read back under
// another case. Two callers ask the same question the same way -- pkg/sandbox
// for its containment predicates and pkg/swarm for its slot containment -- so
// the probe lives here once instead of twice.
//
// The answer a silent filesystem gives is the CALLER's choice and is passed in
// as `unanswerable`: pkg/sandbox leans `false` (a containment answer that a
// case fold walks around is then a secret refused rather than a sound HOME
// refused), while pkg/swarm leans `true` (there a refusal a person can read
// and edit is worth more than a key file copied inside the wall). The rule is
// named in each caller's comment at the call site.
package pathcase

import (
	"os"
	"path/filepath"
	"strings"
)

// recased is one name spelled in another case, or the name itself where it
// carries no letter to re-case.
func recased(name string) string {
	if lower := strings.ToLower(name); lower != name {
		return lower
	}
	if upper := strings.ToUpper(name); upper != name {
		return upper
	}
	return name
}

// ProbeFolds puts the question to the filesystem by writing IN THE DIRECTORY
// ASKED ABOUT: one probe file whose name carries capitals, asked for again in
// another case, and removed either way. The write is the only way to ask a
// directory about its own lookups, and it happens while the policy is built,
// never inside the wall.
//
// THE CLIMB IS THE ONE INFERENCE LEFT, and it is only for a directory that IS
// NOT THERE: a path nobody has created cannot be asked, so the nearest existing
// ancestor answers for it, and that ancestor can be on another filesystem. It
// does not climb past a directory that IS there and refused the write, because
// that would answer about a volume nobody asked about. `unanswerable` is what
// it answers where no write could be made at all -- the one case where the
// filesystem said nothing, and the caller decides which way it then leans.
func ProbeFolds(dir string, unanswerable bool) bool {
	d := dir
	for {
		if _, err := os.Stat(d); err == nil {
			break
		}
		up := filepath.Dir(d)
		if up == d {
			return unanswerable
		}
		d = up
	}
	f, err := os.CreateTemp(d, "NovaCaseProbe")
	if err != nil {
		return unanswerable
	}
	// ignored: the probe file is closed before it is looked up; a close error cannot change the fold answer.
	f.Close()
	name := f.Name()
	// ignored: the probe file is removed either way; a remove error cannot change the fold answer.
	defer os.Remove(name)
	orig, err := os.Lstat(name)
	if err != nil {
		return unanswerable
	}
	other, err := os.Lstat(filepath.Join(d, recased(filepath.Base(name))))
	return err == nil && os.SameFile(orig, other)
}
