package swarm

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/pathcase"
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
// that holds it. It is not `strings.EqualFold`: on a case-SENSITIVE filesystem `Worker-1` and
// `worker-1` are two directories and folding them would refuse a placement that is sound.
//
// The pair is asked of dir ITSELF -- `os.Lstat` of `<dir>/<a>` and `<dir>/<b>` is a lookup in
// the directory whose behaviour is in question, which is what makes it the right question at a
// mount boundary. The caller passes the FULL names it is judging (the candidate slot directory
// and the slot spelling `SlotDir` would build), so where the expected slot exists this answers
// from the two directories themselves and the fold is never guessed at all.
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

// dirFoldsCase says whether a lookup INSIDE dir treats two spellings of one name as one name,
// MEASURED and never taken from runtime.GOOS: APFS can be formatted case-sensitive (this
// repo's own test runs prove the skip branch on a case-sensitive sparse image) and a linux
// mount can fold, so the platform is not the answer.
//
// AND THE MEASUREMENT IS MADE IN THE DIRECTORY BEING JUDGED, NEVER ON ITS OWN NAME IN ITS
// PARENT. A previous revision took a read-only shortcut -- re-case dir's own name and ask the
// parent for it back -- and that measures the PARENT's filesystem. The two differ at a mount
// boundary and on a filesystem with a per-directory casefold setting: measured on a
// case-sensitive APFS image mounted under the folding `/Volumes`, `dirFoldsCase(<mnt>)`
// answered true while a file written inside the volume said false, and `LoadWorker` then
// REFUSED a sound `<mnt>/Worker-1/.key` -- and read the other way, a folding volume under a
// case-sensitive parent would admit a folded slot. Both eyes of #159 found it (a friend, comment
// 5648066751; the Fable read, comment 5648102050). The question is about CHILDREN of dir, so it
// is put to dir: one probe file written there and asked for again in another case.
//
// Where the write cannot be made the answer is YES. This package asks the question only of a
// name that already differs from a slot directory's by case alone, and there a refusal a person
// can read and edit is worth more than a key file copied inside the wall (rule 6).
func dirFoldsCase(dir string) bool {
	return pathcase.ProbeFolds(dir, true)
}

// AbsResolved is a path made absolute and then symlink-resolved: the one spelling admission
// records for a slot or a root, so every later compare -- the wall's argv, a containment
// check, a test's expectation -- is against the same name the filesystem itself uses.
//
// Absolute alone is not enough. On darwin `t.TempDir()` and `$TMPDIR` are handed out under
// `/var`, which IS a symlink to `/private/var`, so `filepath.Abs` of a relative spelling
// resolves it (the cwd `os.Getwd` returns is already resolved) while an absolute spelling
// passes through unresolved -- one directory under two names, and a run's own paths
// disagreeing with each other depending on how the caller typed them (issue #578).
//
// A path that does not exist YET -- the slot a run is about to create -- cannot be resolved
// whole, so its deepest existing ancestor is resolved and the rest of the spelling is kept.
// That is the same answer the path will have once it is made, short of a symlink introduced
// later inside it.
func AbsResolved(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rest := ""
	for cur := abs; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return filepath.Clean(real), nil
			}
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil // nothing on the way up exists; the absolute spelling is the answer
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
