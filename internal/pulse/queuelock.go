package pulse

// ONE WRITER PER QUEUE.
//
// bin/pulse-loop.sh's line 5 is a pidfile: a second loop on the same queue refuses and
// exits 1. Nothing in nova-pulse had that. `run`, `fill` and `manager` all write the same
// queue -- <queue>/NEXT read-modify-write (manager.go's nextNumber), the launched markers
// fill writes, the receipts and the ledger -- and two of them on one queue hand the same
// card number out twice and race the markers that hold a lane. That is the dogfood edge of
// 2026-09-18, gap 2.
//
// THE PROTOCOL. Stella's cold read of #1430 found four races in the first version, all from
// one mistake: `O_EXCL` creates an EMPTY file and the content arrives later, so there is a
// window in which the lock exists and says nothing. A second writer that read it in that
// window saw no pid, called it an orphan, and deleted a LIVE owner's lock.
//
//  1. CREATION AND CONTENT ARE ONE STEP. The record is written to a temp file beside the
//     lock and hard-linked onto the lock name. `link` fails when the name exists, so it is
//     the exclusion; and the lock path never exists without its whole record. No window.
//  2. RELEASE IS IDENTITY-CHECKED. Every record carries a NONCE, and a lock is released only
//     when the record on disk is still the one this handle wrote. An owner whose lock was
//     recovered out from under it does not then delete its replacement's.
//  3. STALE RECOVERY IS SERIALIZED. Deciding a lock is dead and taking it over happens under
//     <queue>/.lock.take, so two recoverers cannot both unlink and both relink -- and the
//     holder is read AGAIN under it, because the one the caller saw may have been replaced
//     in between. THE TAKE IS A LOCK TOO and is held to every rule here: its own record, and
//     cleared only when its taker is provably gone. AGE IS NOT DEATH. It was cleared past a
//     minute of wall clock for one day, and a recoverer that was merely slow -- a stopped
//     process, a paused container, a machine that swapped -- was robbed of it while alive
//     and mid-recovery, which is two writers inside the very thing it excludes.
//  4. REENTRANCY IS BY NONCE, NOT BY PID. `loop` runs three verbs that each ask for this
//     lock, and one process is one writer -- but a pid is a small number the operating
//     system hands out again, and "the holder's pid equals mine" would let a recycled pid
//     walk into a lock this process never took. A lock is ours when the record's nonce is
//     one THIS process is holding.
//
// STALE IF DEAD, like nova-sandbox's owner marker. A lock whose holder is gone is not a
// lock: a SIGKILLed loop would otherwise stop the bench until somebody noticed the file.
// Both halves must hold for a lock to stand -- the pid is RUNNING, and the process wearing
// that number is the one that wrote the file.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// lockAlive and lockStart are the kernel probes this file judges a holder by. They are
// variables, as cmd/nova-sandbox's reaper does it, so a test drives BOTH halves -- a live
// holder and a dead one -- with no process to spawn and no pid to guess. A test that
// guessed a pid ("ours plus one") would pass or fail by what else the machine is running.
var (
	lockAlive = swarm.Alive
	lockStart = swarm.StartStamp
)

// QueueLockName is the lock file inside the queue directory. It begins with a dot and is
// not a card-<n>.md, so every glob the queue verbs run steps over it.
const QueueLockName = ".lock"

// TakeLockName serializes stale recovery. IT IS A LOCK, and it is held to the same rule as
// the one beside it: it carries its taker's record, it is cleared only when that taker is
// provably gone, and it is released only by the taker that wrote it.
//
// It was cleared BY AGE for one day: past a minute of wall clock, whoever came along took
// it. Age is not death. A recoverer that is merely slow -- a stopped process, a paused
// container, a machine that swapped -- was robbed of the take while it was alive and
// mid-recovery, and then two writers were inside the recovery the take exists to serialize
// (Stella's re-read of #1430).
const TakeLockName = ".lock.take"

// held is the nonces THIS process is holding, by lock path. It is what makes reentrancy
// exact: `loop` asks for the lock it already has, and a recycled pid cannot.
var held = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// LockHolder is what a lock file says about who holds it.
type LockHolder struct {
	PID   int
	Start string // the kernel's start stamp for that pid, or "-" where the machine gives none
	Nonce string // this holding's identity; release unlinks only its own record
	Verb  string
	At    string
}

// Line names the holder in one line, which is what a refused second writer is owed.
func (h LockHolder) Line() string {
	return fmt.Sprintf("pid=%d verb=%s since=%s", h.PID, oneline.Field(nonEmpty(h.Verb, "-")), oneline.Field(nonEmpty(h.At, "-")))
}

// QueueLock is a held queue lock. Release gives it back; a handle for a lock this process
// already held releases nothing.
type QueueLock struct {
	path  string
	nonce string
	own   bool
}

// LockedError is a second writer's refusal: the lock, and who is holding it.
type LockedError struct {
	Path   string
	Holder LockHolder
	// Why names the case when it is not a plain live holder -- a recovery in flight, whose
	// holder the record may not name.
	Why string
}

func (e *LockedError) Error() string {
	if e.Why != "" {
		return fmt.Sprintf("the queue is locked: %s (%s); one writer per queue -- wait for it, or remove %s if that process is gone",
			e.Why, e.Holder.Line(), oneline.Field(e.Path))
	}
	return fmt.Sprintf("the queue is locked by another writer (%s); one writer per queue -- wait for it, or remove %s if that process is gone",
		e.Holder.Line(), oneline.Field(e.Path))
}

// LockQueue takes <queue>/.lock for the named verb. It returns a handle, or a *LockedError
// naming the live holder. A stale lock -- one whose holder is not running, or is a
// different process now wearing that pid -- is taken over, once, and never waited on.
func LockQueue(queue, verb string) (*QueueLock, error) {
	return lockQueueAt(queue, verb, time.Now().UTC(), os.Getpid())
}

// lockQueueAt is LockQueue with the clock and the pid handed in, so a test drives both
// halves -- a live holder and a dead one -- with no process to spawn.
func lockQueueAt(queue, verb string, now time.Time, self int) (*QueueLock, error) {
	if strings.TrimSpace(queue) == "" {
		return nil, fmt.Errorf("cannot lock a queue with no directory; refusing to guess (name the queue directory)")
	}
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return nil, fmt.Errorf("cannot open the queue %s: %s (name a writable directory)", oneline.Field(queue), oneline.Err(err))
	}
	path := filepath.Join(queue, QueueLockName)

	// Ours already? Two things must both hold: this really is this process (the nonce
	// registry is per PROCESS, and a caller standing in for another one passes its pid), and
	// the record on disk is still the one we wrote. The pid is never the test on its own.
	if self == os.Getpid() {
		if mine, ok := ourNonce(path); ok {
			if readLockHolder(path).Nonce == mine {
				return &QueueLock{path: path}, nil
			}
			// Our record is gone: somebody took the lock from under us. That is not ours to
			// walk into -- forget it and contend for it like anybody else.
			forgetNonce(path)
		}
	}

	// Two turns and no more: take it, or recover ONE stale holder and take it. A loop here
	// would be a wait, and a wait with no deadline is the thing we do not write.
	for turn := 0; turn < 2; turn++ {
		nonce, err := publishRecord(path, verb, now, self)
		if err == nil {
			// Only a lock THIS process holds goes in the registry; a caller standing in for
			// another process must not leave our name on its lock.
			if self == os.Getpid() {
				rememberNonce(path, nonce)
			}
			return &QueueLock{path: path, nonce: nonce, own: true}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("cannot take the lock %s: %s", oneline.Field(path), oneline.Err(err))
		}
		if holder := readLockHolder(path); lockHolderLive(holder) {
			return nil, &LockedError{Path: path, Holder: holder}
		}
		if turn > 0 {
			break
		}
		if err := recoverStale(path, now); err != nil {
			return nil, err
		}
	}
	return nil, &LockedError{Path: path, Holder: readLockHolder(path)}
}

// publishRecord writes the whole record to a temp file beside the name and hard-links it
// onto it. `link` fails with ErrExist when the name is taken, so it is BOTH the exclusion
// and the publication: the path never exists holding half a record, which is the window the
// first version left open (Stella's cold read, defect 2). Both locks are taken this way --
// <queue>/.lock and the <queue>/.lock.take that serializes its recovery.
func publishRecord(path, verb string, now time.Time, self int) (string, error) {
	nonce, err := lockNonce()
	if err != nil {
		return "", err
	}
	temp := path + "." + strconv.Itoa(self) + "." + nonce
	body := fmt.Sprintf("pid=%d\nstart=%s\nnonce=%s\nverb=%s\nat=%s\n",
		self, nonEmpty(strings.TrimSpace(lockStart(self)), "-"), nonce,
		oneline.Field(verb), now.Format(time.RFC3339))
	if err := os.WriteFile(temp, []byte(body), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(temp)
	if err := os.Link(temp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fs.ErrExist
		}
		// A filesystem that will not hard-link cannot give an atomic exclusive create, and
		// there is no second best here: a lock with a window is a lock that deletes a live
		// owner's file. It is named, not worked around.
		return "", fmt.Errorf("%s does not support the hard link this lock is taken with: %s (the queue must sit on a filesystem that does)",
			oneline.Field(filepath.Dir(path)), oneline.Err(err))
	}
	return nonce, nil
}

// THE CLAIM: THE ONLY WAY A RECORD IS EVER REMOVED.
//
// Reading a record and then unlinking the path is check-then-act, and no number of re-reads
// closes it. Stella's third read of #1430 walked it: A reads the record and finds its owner
// dead; B reads the same and finds the same; B removes it and publishes its own, LIVE;
// A resumes and unlinks -- B's live record -- and both are inside the thing the file
// excludes. The second read A does before unlinking is just a smaller window.
//
// So the removal is ATOMIC WITH THE VALIDATION, and the atom is `rename`. A recoverer first
// renames the record to a name only it knows, <path>.claim-<nonce>. Exactly one rename can
// succeed; every other recoverer gets ENOENT and backs off, having touched nothing. THEN the
// winner judges the record it is holding, which nobody else can reach, and unlinks it -- or,
// if the owner turns out to be alive after all, puts it back.
//
// The put-back is `link`, never `rename`: rename would clobber a record somebody published
// into the gap, and a lock protocol may not overwrite a file it did not read.

// lockClaimHook runs between the claim and the judgement. It is nil in production and is
// what lets a test PAUSE one recoverer exactly inside the window, with a second one running,
// and prove the live record survives.
var lockClaimHook func()

// claimRecord takes a record out of everyone else's reach, atomically, and hands back what
// it was and where it now is. ok is false when somebody else won the claim, or there was
// nothing there: either way this caller has touched nothing and must back off.
func claimRecord(path, nonce string) (claim string, was LockHolder, ok bool) {
	claim = path + ".claim-" + nonce
	if err := os.Rename(path, claim); err != nil {
		return "", LockHolder{}, false
	}
	if lockClaimHook != nil {
		lockClaimHook()
	}
	return claim, readLockHolder(claim), true
}

// unclaim puts a claimed record back where its owner expects it. `link` and not `rename`: a
// writer may legitimately have published into the gap while the claim was held, and this one
// has no right to overwrite that. When the gap is taken, the claim is dropped rather than
// forced -- one of the two owners then finds its record gone, and an identity-checked
// release means it removes nothing that is not its own.
func unclaim(claim, path string) {
	if err := os.Link(claim, path); err != nil {
		_ = os.Remove(claim)
		return
	}
	_ = os.Remove(claim)
}

// releaseRecord unlinks a record ONLY when it is still the one this nonce wrote. A cleanup
// that removes the path whatever is at it is a cleanup that deletes the file of whoever
// rightfully replaced you -- which is two writers inside the thing the file was excluding.
func releaseRecord(path, nonce string) {
	if nonce == "" {
		return
	}
	claim, was, ok := claimRecord(path, nonce)
	if !ok {
		return // already gone, or claimed by a recoverer that will judge it
	}
	if was.Nonce == nonce {
		_ = os.Remove(claim)
		return
	}
	unclaim(claim, path)
}

// clearDeadRecord removes a lock or a take whose owner is PROVABLY GONE. It never judges by
// age: a process is dead when the kernel says so, and the clock says nothing at all about a
// process that is merely slow. The judgement happens under the claim, so the record it reads
// and the record it unlinks are the same file and nobody else can reach it in between.
func clearDeadRecord(path string) bool {
	nonce, err := lockNonce()
	if err != nil {
		return false
	}
	claim, was, ok := claimRecord(path, nonce)
	if !ok {
		return false
	}
	if lockHolderLive(was) {
		unclaim(claim, path)
		return false
	}
	return os.Remove(claim) == nil
}

// recoverStale takes a dead holder's lock away, under <queue>/.lock.take so two recoverers
// cannot both unlink and both relink. The take is taken the same way the lock is, and it is
// held to the same three rules: whole record or nothing, cleared only when its owner is
// provably gone, released only by the taker that wrote it.
func recoverStale(path string, now time.Time) error {
	take := filepath.Join(filepath.Dir(path), TakeLockName)
	nonce, err := publishRecord(take, "recover", now, os.Getpid())
	if err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("cannot start the recovery of %s: %s", oneline.Field(path), oneline.Err(err))
		}
		// Somebody else is recovering. A LIVE taker is never disturbed, however long it has
		// been holding: age is not death, and a slow recoverer robbed of its take puts two
		// writers inside the recovery this file exists to serialize.
		taker := readLockHolder(take)
		if !clearDeadRecord(take) {
			return &LockedError{Path: path, Holder: taker,
				Why: "another writer is recovering a stale lock right now"}
		}
		// The dead taker is gone. One more attempt, and if somebody beat us to it, we give
		// way rather than loop: a wait with no deadline is the thing we do not write.
		nonce, err = publishRecord(take, "recover", now, os.Getpid())
		if err != nil {
			return &LockedError{Path: path, Holder: readLockHolder(take),
				Why: "another writer is recovering a stale lock right now"}
		}
	}
	defer releaseRecord(take, nonce)

	// Read the LOCK again here: between the caller's read and this take, the dead holder may
	// have been cleared by somebody else and a live one put in its place.
	holder := readLockHolder(path)
	if lockHolderLive(holder) {
		return &LockedError{Path: path, Holder: holder}
	}
	if !clearDeadRecord(path) {
		if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
			return nil // already gone: the next turn takes it
		}
		return &LockedError{Path: path, Holder: readLockHolder(path),
			Why: "the stale lock moved while it was being recovered"}
	}
	return nil
}

// Release gives the lock back, and only when the record on disk is still THE ONE THIS HANDLE
// WROTE. An owner whose lock was recovered out from under it -- rightly, because it looked
// dead -- must not then delete the lock its replacement is holding (Stella's cold read,
// defect 2). A handle for a lock this process already held releases nothing.
func (l *QueueLock) Release() {
	if l == nil || !l.own {
		return
	}
	l.own = false
	releaseRecord(l.path, l.nonce)
	forgetNonce(l.path)
}

func lockNonce() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("the lock's identity could not be made: %s", oneline.Err(err))
	}
	return hex.EncodeToString(b[:]), nil
}

func ourNonce(path string) (string, bool) {
	held.Lock()
	defer held.Unlock()
	n, ok := held.m[path]
	return n, ok
}

func rememberNonce(path, nonce string) {
	held.Lock()
	defer held.Unlock()
	held.m[path] = nonce
}

func forgetNonce(path string) {
	held.Lock()
	defer held.Unlock()
	delete(held.m, path)
}

// readLockHolder reads the lock file's fields. Anything unreadable is the zero holder,
// whose pid is 0 and which lockHolderLive answers is not live.
func readLockHolder(path string) LockHolder {
	var h LockHolder
	raw, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "pid":
			h.PID, _ = strconv.Atoi(v)
		case "start":
			h.Start = v
		case "nonce":
			h.Nonce = v
		case "verb":
			h.Verb = v
		case "at":
			h.At = v
		}
	}
	return h
}

// lockHolderLive answers the one question a second writer may not get wrong. Both halves
// must hold: the pid is running, AND the process running under that number is the one that
// wrote the lock. Where the machine gives no start stamp the comparison is dash against
// dash and "alive" is the answer -- the process is real and only the evidence is missing.
func lockHolderLive(h LockHolder) bool {
	if h.PID <= 0 {
		return false
	}
	if !lockAlive(h.PID, h.Start) {
		return false
	}
	now := strings.TrimSpace(lockStart(h.PID))
	if now == "" || now == "-" || h.Start == "" || h.Start == "-" {
		return true
	}
	return now == h.Start
}
