package bus

import (
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// #4420 round 5, Stella's open concern: on linux a non-dumpable git has its /proc/<pid>
// entry owned by root whichever account runs it, so the entry's owner cannot prove another
// account. A supplied /proc (no real setuid, no real non-dumpable process): the reader
// serves the entry owner, a status with its Uid line or a refusal, the comm, cmdline and
// cwd. Every case runs through readProcView, classifyViews and the report on a real stale
// lock, and the counts are the ones the WAIT SCAN receipt prints.
//
// The four outcomes: OWNER (placed here, whoever runs it), FOREIGN (unplaced, and the
// status Uid proves another account), UNKNOWN (still present, and its status, cmdline or
// cwd cannot be read, or a root-owned entry with no status to prove another account), and
// GONE (ENOENT or ESRCH at any read: the process no longer exists, holds no lock, and is
// counted nowhere, #3029). Only OWNER=0 and UNKNOWN=0 unlinks.
func TestUnplacedGitIsForeignOnlyByItsStatusUID(t *testing.T) {
	t.Parallel()
	hermetic(t)
	const self, other = 501, 502
	eacces := func(op, path string) error { return &fs.PathError{Op: op, Path: path, Err: syscall.EACCES} }
	esrch := func(op, path string) error { return &fs.PathError{Op: op, Path: path, Err: syscall.ESRCH} }
	uidLine := func(uid int) []byte {
		return []byte("Name:\tgit\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\nGid:\t20\t20\t20\t20\n")
	}
	// reader serves one pid: the entry owner, and per-file answers.
	reader := func(owner uint32, status []byte, statusErr error, cmdline []byte, cwdErr error) procReader {
		return procReader{
			owner: func(string) (uint32, bool, error) { return owner, true, nil },
			readFile: func(name string) ([]byte, error) {
				switch {
				case strings.HasSuffix(name, "/comm"):
					return []byte("git\n"), nil
				case strings.HasSuffix(name, "/status"):
					return status, statusErr
				case strings.HasSuffix(name, "/cmdline"):
					return cmdline, nil
				}
				return nil, eacces("open", name)
			},
			readlink: func(name string) (string, error) {
				if cwdErr != nil {
					return "", cwdErr
				}
				return "/home/nova/elsewhere", nil
			},
		}
	}
	unplaced := []byte("git\x00commit\x00-m\x00x")
	cases := []struct {
		name    string
		r       procReader
		want    LockScan
		cleared bool
		unknown bool // the error is the ownershipUnknown sentence
	}{
		{"root-owned entry, status says self, cwd EACCES: own git out of place, UNKNOWN",
			reader(0, uidLine(self), nil, unplaced, eacces("readlink", "/proc/42/cwd")), LockScan{Unknown: 1}, false, true},
		{"root-owned entry, status says another account, cwd EACCES: FOREIGN, cleared",
			reader(0, uidLine(other), nil, unplaced, eacces("readlink", "/proc/42/cwd")), LockScan{Foreign: 1}, true, false},
		{"root-owned entry, status EACCES: UNKNOWN, never foreign",
			reader(0, nil, eacces("open", "/proc/42/status"), unplaced, eacces("readlink", "/proc/42/cwd")), LockScan{Unknown: 1}, false, true},
		{"entry owned by another uid, status EACCES: UNKNOWN, the entry owner proves nothing",
			reader(other, nil, eacces("open", "/proc/42/status"), unplaced, eacces("readlink", "/proc/42/cwd")), LockScan{Unknown: 1}, false, true},
		{"status readable but without a Uid line: UNKNOWN",
			reader(other, []byte("Name:\tgit\n"), nil, unplaced, eacces("readlink", "/proc/42/cwd")), LockScan{Unknown: 1}, false, true},
		{"status ESRCH mid-read: GONE, counted nowhere, cleared",
			reader(other, nil, esrch("open", "/proc/42/status"), unplaced, nil), LockScan{}, true, false},
		{"cwd ESRCH after the status was read: GONE, cleared",
			reader(other, uidLine(other), nil, unplaced, esrch("readlink", "/proc/42/cwd")), LockScan{}, true, false},
		{"status says self and the cwd is readable, elsewhere: neither owner nor unknown, cleared",
			reader(self, uidLine(self), nil, unplaced, nil), LockScan{}, true, false},
	}
	// One fixture checkout; the stale lock is planted again before each case.
	dir, lock := oldIndexLock(t)
	plant := func() {
		t.Helper()
		if err := os.WriteFile(lock, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-2 * time.Minute)
		if err := os.Chtimes(lock, when, when); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cases {
		plant()
		v := readProcView("42", c.r)
		rep, err := clearStaleIndexLockReport(dir, time.Now(), func() ([]gitProc, error) {
			return classifyViews([]procView{v}, self)
		}, indexLockOwner, self)
		if rep.Scan != c.want || rep.Cleared != c.cleared || !rep.Scanned {
			t.Fatalf("%s: view=%+v report=%+v err=%v, want scan=%+v cleared=%v scanned", c.name, v, rep, err, c.want, c.cleared)
		}
		if c.unknown != (err != nil && strings.HasPrefix(err.Error(), ownershipUnknown)) {
			t.Fatalf("%s: err=%v, want unknown=%v", c.name, err, c.unknown)
		}
		_, statErr := os.Lstat(lock)
		if c.cleared != os.IsNotExist(statErr) {
			t.Fatalf("%s: lock present=%v after cleared=%v", c.name, statErr == nil, c.cleared)
		}
	}

	// A placed git is an owner whoever runs it and whatever its entry looks like: the
	// non-dumpable git of this account naming the checkout with -C keeps the lock, with
	// no error, and the counts say owner=1.
	plant()
	v := readProcView("42", reader(0, nil, eacces("open", "/proc/42/status"), []byte("git\x00-C\x00"+dir+"\x00commit"), eacces("readlink", "/proc/42/cwd")))
	rep, err := clearStaleIndexLockReport(dir, time.Now(), func() ([]gitProc, error) {
		return classifyViews([]procView{v}, self)
	}, indexLockOwner, self)
	if err != nil || rep.Cleared || rep.Scan != (LockScan{Owner: 1}) {
		t.Fatalf("non-dumpable own git naming the checkout: report=%+v err=%v, want owner=1 and the lock kept", rep, err)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("owner's lock lost: %v", statErr)
	}
}

// The Uid line is "Uid:\t<real>\t<effective>\t<saved>\t<fs>"; the account is the effective
// uid, the second field. A status with no such line, or a short one, has no account.
func TestStatusEffectiveUIDIsTheSecondField(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		status string
		uid    uint32
		ok     bool
	}{
		{"Name:\tgit\nUid:\t1000\t1001\t1000\t1000\nGid:\t20\t20\t20\t20\n", 1001, true},
		{"Uid:\t0\t0\t0\t0\n", 0, true},
		{"Uid: 501 502 503 504\n", 502, true},
		{"Uid:\t501\n", 0, false},
		{"Name:\tgit\n", 0, false},
		{"Uid:\tx\ty\n", 0, false},
		{"", 0, false},
	} {
		uid, ok := statusEffectiveUID([]byte(c.status))
		if uid != c.uid || ok != c.ok {
			t.Fatalf("status %q: uid=%d ok=%v, want %d %v", c.status, uid, ok, c.uid, c.ok)
		}
	}
}

// The report's counts and its Scanned flag: an owner beside a foreign and an unknown git
// answers the question (owner=1 foreign=1 unknown=1, kept, no error); a scan that failed
// as a whole has no counts (Scanned false) and its error is the failure; a scan of
// nothing but foreign gits is complete, and the lock goes with foreign=n on the record.
func TestLockScanCountsEveryProcessItSaw(t *testing.T) {
	t.Parallel()
	hermetic(t)
	const self, other = 501, 502
	denied := &fs.PathError{Op: "readlink", Path: "/proc/9/cwd", Err: syscall.EACCES}
	dir, lock := oldIndexLock(t)
	views := []procView{
		{account: other, accountKnown: true, comm: "git\n", cmdline: []byte("git\x00status"), cwdErr: denied},
		{account: self, accountKnown: true, comm: "git\n", cmdline: []byte("git\x00status"), cwdErr: denied},
		{account: other, accountKnown: true, comm: "git\n", cmdline: []byte("git\x00-C\x00" + dir + "\x00fetch"), cwdErr: denied},
		{comm: "bash\n"},
		{commErr: &fs.PathError{Op: "stat", Path: "/proc/10", Err: syscall.ENOENT}},
	}
	rep, err := clearStaleIndexLockReport(dir, time.Now(), func() ([]gitProc, error) { return classifyViews(views, self) }, indexLockOwner, self)
	if err != nil || rep.Cleared || !rep.Scanned || rep.Scan != (LockScan{Owner: 1, Foreign: 1, Unknown: 1}) {
		t.Fatalf("owner beside foreign and unknown: report=%+v err=%v, want owner=1 foreign=1 unknown=1, kept, no error", rep, err)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("owner's lock lost: %v", statErr)
	}

	rep, err = clearStaleIndexLockReport(dir, time.Now(), func() ([]gitProc, error) { return nil, ownershipUnknownErr("ps failed") }, indexLockOwner, self)
	if err == nil || rep.Scanned || rep.Cleared || rep.Scan != (LockScan{}) {
		t.Fatalf("a scan that failed as a whole: report=%+v err=%v, want no counts and the failure", rep, err)
	}

	rep, err = clearStaleIndexLockReport(dir, time.Now(), func() ([]gitProc, error) { return classifyViews(views[:1], self) }, indexLockOwner, self)
	if err != nil || !rep.Cleared || !rep.Scanned || rep.Scan != (LockScan{Foreign: 1}) {
		t.Fatalf("only a foreign git: report=%+v err=%v, want cleared with foreign=1", rep, err)
	}
	if _, statErr := os.Lstat(lock); !os.IsNotExist(statErr) {
		t.Fatalf("stale index.lock still present: %v", statErr)
	}
}
