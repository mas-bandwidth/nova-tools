package swarm

import (
	"os"
	"path/filepath"
	"strings"
)

// A FILESYSTEM IS NOT A STRING, and this file is the one place in this package that says so.
// APFS is case-INsensitive by default and NTFS is too, so two spellings that differ only in
// case are ONE file while a `strings` comparison says they are two. Every containment answer
// this package gives about a key file serves SPEC-SANDBOX rule 6 -- "the secret is never
// inside either list" -- and a containment answer that a case fold walks around is a key read
// inside the wall under a green `SANDBOX OK` (issue #100 for `worker_dir` and `read_roots`,
// issue #145 for the slot directory).
//
// `os.SameFile` is the answer wherever the two sides EXIST, because device and inode is the
// question the filesystem itself answers: it holds for a case fold, for one directory mounted
// at two names, and for a hard-linked directory where one exists. The slot directory is the
// case where that is not available -- slots are created at run, so at load there may be no
// inode to compare -- and there the filesystem's case behaviour is MEASURED, on the directory
// in question, never guessed from `runtime.GOOS`.

// namesOneFile says whether two names in one directory name the same thing on the filesystem
// that holds it. It is not `strings.EqualFold`: on a case-SENSITIVE filesystem `Worker` and
// `worker` are two directories and folding them would refuse a placement that is sound.
func namesOneFile(dir, a, b string) bool {
	if a == b {
		return true
	}
	if !strings.EqualFold(a, b) {
		return false // nothing but a case fold can make two spellings one name here
	}
	// The pair itself, where the filesystem is there to answer about both sides: two names
	// that answer to one device and inode ARE one name. `os.Lstat`, not `os.Stat`, because
	// the question is about the NAMES: a symlink `<dir>/Worker -> <dir>/worker` is a
	// different name that happens to lead to the same directory, and the `<dir>/Worker-1`
	// beside it is nobody's slot.
	if fa, err := os.Lstat(filepath.Join(dir, a)); err == nil {
		if fb, err := os.Lstat(filepath.Join(dir, b)); err == nil {
			return os.SameFile(fa, fb)
		}
	}
	return dirFoldsCase(dir)
}

// sameDir says whether two paths are one directory: the same string, or the same device and
// inode where both are there to be asked. A parent spelled another way -- a case fold, one
// directory mounted at two names -- is still the same parent.
func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// dirFoldsCase says whether the filesystem holding dir treats two spellings of one name as
// one name, MEASURED and never taken from runtime.GOOS: APFS can be formatted
// case-sensitive (this repo's own test runs prove the skip branch on a case-sensitive sparse
// image) and a linux mount can fold, so the platform is not the answer.
//
// The question is asked read-only first: where dir exists, its own name in another case
// either answers to the same device and inode -- the filesystem folded -- or does not answer
// at all. Where dir's name carries no letter there is nothing to re-case, and the question is
// put by WRITING a probe file and asking for it back in another case.
//
// Where neither can run the answer is YES. This package asks the question only of a name that
// already differs from a slot directory's by case alone, and there a refusal a person can read
// and edit is worth more than a key file copied inside the wall (rule 6).
func dirFoldsCase(dir string) bool {
	if base := filepath.Base(dir); recased(base) != base {
		if orig, err := os.Lstat(dir); err == nil {
			other, err := os.Lstat(filepath.Join(filepath.Dir(dir), recased(base)))
			return err == nil && os.SameFile(orig, other)
		}
	}
	return writtenProbeFolds(dir, true)
}

// recased is one name spelled in another case, or the name itself where it carries no letter
// to re-case (a slot's digits, most often).
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
// nearest EXISTING ancestor, because the directory asked about may be one that is not there
// yet; it does not climb past a directory that IS there and refused the write, because the
// next one up may be another filesystem. `unanswerable` is what it answers where the probe
// could not be written at all -- the one case where the filesystem said nothing.
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
