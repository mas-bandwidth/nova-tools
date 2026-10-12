package sandbox

import "github.com/mas-bandwidth/nova-tools/pkg/pathcase"

// A FILESYSTEM IS NOT A STRING, and this file is the one place in this package that says so.
// APFS is case-INsensitive by default and NTFS is too, so two spellings that differ only in
// case are ONE file while a `strings` comparison says they are two. `Inside` and `insideAny` are
// the predicate behind six questions and no others: rule 6's `--secret`, rule 10's outside path,
// rule 9's `HOME`, rule 13's `--cwd`, rule 8's `--tmp`, and the command-directory home guard's
// "did the caller already name this". So a containment answer a case fold walks around is either
// a secret the tool says is outside the lists it sits inside (issue #145) or a sound `HOME`
// refused as `home_outside` (rule 1's silent sandbox, the other way about).
//
// `os.SameFile` is the answer wherever both sides EXIST, because device and inode is the
// question the filesystem itself answers: it holds for a case fold, for one directory mounted
// at two names, and for a hard-linked directory where one exists. Where the directory asked
// about is NOT there, nothing has an inode and the only answer available is the name -- and
// there the filesystem's case behaviour is MEASURED, on the nearest directory that exists,
// never guessed from `runtime.GOOS`.

// dirFoldsCase says whether a lookup INSIDE dir treats two spellings of one name as one name.
//
// AND THE MEASUREMENT IS MADE IN THE DIRECTORY BEING JUDGED, NEVER ON ITS OWN NAME IN ITS
// PARENT. A previous revision took a read-only shortcut -- re-case dir's own name and ask the
// parent for it back -- and that measures the PARENT's filesystem, which differs at a mount
// boundary and on a filesystem with a per-directory casefold setting (both eyes of #159:
// a friend, comment 5648066751; the Fable read, comment 5648102050; measured on a case-sensitive
// APFS image mounted under the folding `/Volumes`, where the mountpoint's own name folds and
// nothing inside it does). The shortcut was never REACHED from this package -- `Inside` asks
// this only about a directory that does not exist, and the shortcut needed it to exist -- but
// it is gone from here too, because the next caller would not know that.
//
// So the question is put by WRITING one probe file and asking for it back in another case.
// `Inside` reaches this only for a directory that is not there, so the write lands in the
// nearest existing ancestor and the climb is named at `pathcase.ProbeFolds`.
//
// Where no write can be made the answer is NO, which is the answer this package gave before the
// fold was measured at all: `Inside` has callers on both sides -- a `true` refuses a `--secret`
// and a `true` ADMITS a `HOME` -- so a filesystem that said nothing leaves today's answer
// standing rather than inventing one in a direction that is safe for only half of them.
func dirFoldsCase(dir string) bool {
	return pathcase.ProbeFolds(dir, false)
}
