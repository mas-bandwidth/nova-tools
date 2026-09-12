package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// A FILESYSTEM IS NOT A STRING, and this file is the one place in this package that says so.
// APFS is case-INsensitive by default and NTFS is too, so two spellings that differ only in
// case are ONE file while a `strings` comparison says they are two. `Inside` is what rule 6
// asks of `--secret`, what rule 10 asks of the probe's outside path, and -- through
// `insideAny` -- what rules 9 and 13 ask of `HOME` and `--cwd`, so a containment answer a case
// fold walks around is either a secret the tool says is outside the lists it sits inside
// (issue #145) or a sound `HOME` refused as `home_outside` (rule 1's silent sandbox, the other
// way about).
//
// `os.SameFile` is the answer wherever both sides EXIST, because device and inode is the
// question the filesystem itself answers: it holds for a case fold, for one directory mounted
// at two names, and for a hard-linked directory where one exists. Where the directory asked
// about is NOT there, nothing has an inode and the only answer available is the name -- and
// there the filesystem's case behaviour is MEASURED, on the nearest directory that exists,
// never guessed from `runtime.GOOS`.

// dirFoldsCase says whether the filesystem holding dir treats two spellings of one name as one
// name. It is asked read-only first: where dir exists, its own name in another case either
// answers to the same device and inode -- the filesystem folded -- or does not answer at all.
// Where dir's name carries no letter to re-case, the question is put by WRITING a probe file
// and asking for it back in another case.
//
// Where neither can run the answer is NO, which is the answer this package gave before the
// fold was measured at all: `Inside` has callers on both sides -- a `true` refuses a `--secret`
// and a `true` ADMITS a `HOME` -- so a filesystem that said nothing leaves today's answer
// standing rather than inventing one in a direction that is safe for only half of them.
func dirFoldsCase(dir string) bool {
	if base := filepath.Base(dir); recased(base) != base {
		if orig, err := os.Lstat(dir); err == nil {
			other, err := os.Lstat(filepath.Join(filepath.Dir(dir), recased(base)))
			return err == nil && os.SameFile(orig, other)
		}
	}
	return writtenProbeFolds(dir, false)
}

// recased is one name spelled in another case, or the name itself where it carries no letter
// to re-case.
func recased(name string) string {
	if lower := strings.ToLower(name); lower != name {
		return lower
	}
	if upper := strings.ToUpper(name); upper != name {
		return upper
	}
	return name
}

// writtenProbeFolds puts the question to the filesystem by writing: one probe file whose name
// carries capitals, asked for again in lower case, and removed either way. It climbs to the
// nearest EXISTING ancestor, because the directory asked about is one that is not there; it
// does not climb past a directory that IS there and refused the write, because the next one up
// may be another filesystem. `unanswerable` is what it answers where the probe could not be
// written at all -- the one case where the filesystem said nothing.
func writtenProbeFolds(dir string, unanswerable bool) bool {
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
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	orig, err := os.Lstat(name)
	if err != nil {
		return unanswerable
	}
	other, err := os.Lstat(filepath.Join(d, strings.ToLower(filepath.Base(name))))
	return err == nil && os.SameFile(orig, other)
}
