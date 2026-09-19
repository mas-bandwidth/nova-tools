package swarm

// Per-bench queues with work stealing (docs/SPEC-JOBS.md section 2).
//
// Each bench holds queue/ as a directory of card files. A worker takes one card by
// rename(queue/<name>.card, taken/<worker>-<name>.card): the rename is atomic within
// the directory, so two workers cannot take one card, and it is the ownership record
// -- there is no central counter and no lock around the whole queue. A worker drains
// its own taken/ before it reaches for another bench, and an idle worker steals from
// the fullest bench on the mirror's five-minute timer, never emptying the victim
// below its own capacity line.
//
// This file is the file operations and the two arithmetic rules only. It starts no
// process, reaches no network, and owns no lease: a lease is section 3.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The per-bench queue's names and cadence, as section 2 prints them.
const (
	// CardExt is the suffix that makes a file in queue/ a card.
	CardExt = ".card"
	// QueueName is the per-bench directory of waiting cards.
	QueueName = "queue"
	// TakenName is the per-bench directory of cards a worker owns, one
	// <worker>-<name>.card each.
	TakenName = "taken"
	// MirrorTimer is the bench mirror's fetch-only cadence: an idle worker steals
	// from the fullest bench once per five minutes, not once per look.
	MirrorTimer = 5 * time.Minute
)

// QueueDir is the bench's queue/ directory.
func QueueDir(benchDir string) string { return filepath.Join(benchDir, QueueName) }

// TakenDir is the bench's taken/ directory, the ownership record of its queue.
func TakenDir(benchDir string) string { return filepath.Join(benchDir, TakenName) }

// QueueCards lists the cards waiting in a bench's queue/, without the .card suffix,
// in sorted order so two workers walk the same list and race on the same first card.
// A bench with no queue/ directory has no cards, not an error.
func QueueCards(benchDir string) ([]string, error) {
	entries, err := os.ReadDir(QueueDir(benchDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, CardExt) {
			continue
		}
		names = append(names, strings.TrimSuffix(name, CardExt))
	}
	sort.Strings(names)
	return names, nil
}

// OwnedCards lists the cards a worker already holds on a bench: the
// taken/<worker>-<name>.card ownership records. A worker drains its own taken/
// before it reaches for another bench.
func OwnedCards(benchDir, worker string) ([]string, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return nil, fmt.Errorf("worker name is empty")
	}
	entries, err := os.ReadDir(TakenDir(benchDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	prefix := worker + "-"
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, CardExt) {
			continue
		}
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(name, prefix), CardExt))
	}
	sort.Strings(names)
	return names, nil
}

// ClaimTimeout is the maximum duration an uncompleted card claim remains valid before
// being considered abandoned by a crashed or hung worker process.
const ClaimTimeout = 10 * time.Second

// emptyClaimGrace is the grace period before an unparseable or 0-byte claim file is
// considered abandoned by a crashed process.
var emptyClaimGrace = 20 * time.Millisecond

var (
	linkFile         = os.Link
	noReplacePublish = noReplaceRename
)

var (
	cardLockWait              = 2 * time.Second
	beforeReclaimLockHook     func()
	betweenClaimAndRenameHook func()
)

var randFallbackCounter atomic.Uint64

func newClaimToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), randFallbackCounter.Add(1))
	}
	return hex.EncodeToString(b[:])
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return Alive(pid, "")
}

// claimPath returns the exclusive claim record path for a card in taken/.
func claimPath(takenDir, cardName string) string {
	return filepath.Join(takenDir, cardName+".claim")
}

// ErrCardLockTimeout is returned when takeCardLock cannot acquire an exclusive advisory flock
// within the allotted wait duration due to lock contention from another process.
var ErrCardLockTimeout = errors.New("timeout waiting for card lock")

func cardLockPath(takenDir, cardName string) (string, error) {
	locksDir := filepath.Join(filepath.Dir(takenDir), ".locks")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir card lock dir %s: %w", locksDir, err)
	}
	return filepath.Join(locksDir, cardName+".lock"), nil
}

// takeCardLock takes an exclusive advisory flock on <cardName>.lock in .locks.
// It synchronizes card claiming, renaming, and claim release across all processes.
// On contention timeout, it returns an error wrapping ErrCardLockTimeout.
// On filesystem or setup failure, it returns the unwrapped system error.
func takeCardLock(takenDir, cardName string, wait time.Duration) (func(), error) {
	path, err := cardLockPath(takenDir, cardName)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open card lock %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("lock card %s: %w", path, lockErr)
		}
		if ok {
			released := false
			return func() {
				if released {
					return
				}
				released = true
				unlockFile(f)
				_ = f.Close()
			}, nil
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%w: %s", ErrCardLockTimeout, path)
		}
		time.Sleep(lockPoll)
	}
}

type claimInfo struct {
	raw       string
	worker    string
	pid       int
	startTime time.Time
	token     string
	valid     bool
	empty     bool
}

func parseClaim(raw []byte) claimInfo {
	s := string(raw)
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return claimInfo{raw: s, empty: true}
	}
	var info claimInfo
	info.raw = s
	info.worker = fields[0]
	if len(fields) >= 2 {
		info.pid, _ = strconv.Atoi(fields[1])
	}
	if len(fields) >= 3 {
		if val, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
			if val < 1e12 {
				info.startTime = time.Unix(val, 0)
			} else {
				info.startTime = time.Unix(0, val)
			}
		}
	}
	if len(fields) >= 4 {
		info.token = fields[3]
		info.valid = true
	} else if len(fields) == 3 && !info.startTime.IsZero() {
		info.valid = true
	}
	return info
}

func isClaimStale(info claimInfo) bool {
	if info.empty || !info.valid {
		return true
	}
	if info.pid > 0 {
		// A living process is never stale regardless of elapsed time.
		return !processAlive(info.pid)
	}
	if !info.startTime.IsZero() && time.Since(info.startTime) > ClaimTimeout {
		return true
	}
	return false
}

// publishClaimFile writes content to a unique temporary file in takenDir and safely publishes
// it to claimPath(takenDir, cardName) via linkFile (or noReplacePublish), ensuring no empty
// or partially written file is ever left at the destination.
func publishClaimFile(takenDir, cardName string, content []byte) error {
	final := claimPath(takenDir, cardName)
	temp := filepath.Join(takenDir, fmt.Sprintf(".claim-tmp-%d-%s", os.Getpid(), newClaimToken()))
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(temp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(temp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	linkErr := linkFile(temp, final)
	if linkErr == nil {
		_ = os.Remove(temp)
		return nil
	}
	if errors.Is(linkErr, os.ErrExist) {
		_ = os.Remove(temp)
		return os.ErrExist
	}
	renameErr := noReplacePublish(temp, final)
	_ = os.Remove(temp)
	if renameErr == nil {
		return nil
	}
	if errors.Is(renameErr, os.ErrExist) {
		return os.ErrExist
	}
	return linkErr
}

// holdsClaim reports whether the claim file on disk for cardName currently holds token.
func holdsClaim(takenDir, cardName, token string) bool {
	if token == "" {
		return false
	}
	raw, err := os.ReadFile(claimPath(takenDir, cardName))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(raw))
	return len(fields) >= 4 && fields[3] == token
}

// releaseClaim unlinks cardName.claim under takeCardLock if and only if the file on disk currently holds token.
// If the claim was replaced by another claimant, the replacement's record is never unlinked.
func releaseClaim(takenDir, cardName, token string) {
	if token == "" {
		return
	}
	unlock, err := takeCardLock(takenDir, cardName, cardLockWait)
	if err != nil {
		return
	}
	defer unlock()
	releaseClaimLocked(takenDir, cardName, token)
}

func releaseClaimLocked(takenDir, cardName, token string) {
	path := claimPath(takenDir, cardName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	fields := strings.Fields(string(raw))
	if len(fields) >= 4 && fields[3] == token {
		_ = os.Remove(path)
	}
}

// claimCard attempts to acquire an exclusive, cross-process claim on cardName in takenDir.
// It writes a unique claim token into the claim file: worker pid start_time_nano token\n.
// Stale claims from dead processes are reclaimed under takeCardLock.
func claimCard(takenDir, cardName, worker string) (string, bool, error) {
	if err := os.MkdirAll(takenDir, 0o755); err != nil {
		return "", false, err
	}
	unlock, err := takeCardLock(takenDir, cardName, cardLockWait)
	if err != nil {
		if errors.Is(err, ErrCardLockTimeout) {
			return "", false, nil
		}
		return "", false, err
	}
	defer unlock()
	return claimCardLocked(takenDir, cardName, worker)
}

func claimCardLocked(takenDir, cardName, worker string) (string, bool, error) {
	token := newClaimToken()
	content := []byte(fmt.Sprintf("%s %d %d %s\n", worker, os.Getpid(), time.Now().UnixNano(), token))

	path := claimPath(takenDir, cardName)
	err := publishClaimFile(takenDir, cardName, content)
	if err == nil {
		return token, true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return "", false, err
	}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			if pErr := publishClaimFile(takenDir, cardName, content); pErr == nil {
				return token, true, nil
			}
			return "", false, nil
		}
		return "", false, readErr
	}

	info := parseClaim(raw)
	if (info.empty || !info.valid) && (info.pid <= 0 || processAlive(info.pid)) {
		if emptyClaimGrace > 0 {
			time.Sleep(emptyClaimGrace)
		}
		raw2, readErr2 := os.ReadFile(path)
		if readErr2 != nil {
			if errors.Is(readErr2, os.ErrNotExist) {
				if pErr := publishClaimFile(takenDir, cardName, content); pErr == nil {
					return token, true, nil
				}
				return "", false, nil
			}
			return "", false, readErr2
		}
		info = parseClaim(raw2)
		raw = raw2
	}

	if !isClaimStale(info) {
		return "", false, nil
	}

	if beforeReclaimLockHook != nil {
		beforeReclaimLockHook()
	}

	// Double check claim on disk hasn't changed before replacing.
	curRaw, curErr := os.ReadFile(path)
	if curErr == nil {
		curInfo := parseClaim(curRaw)
		if info.token != "" && curInfo.token != info.token {
			return "", false, nil
		}
		if !isClaimStale(curInfo) {
			return "", false, nil
		}
	}

	_ = os.Remove(path)
	if pErr := publishClaimFile(takenDir, cardName, content); pErr != nil {
		return "", false, pErr
	}

	return token, true, nil
}

// TakeCard takes one card from a bench's queue by renaming it into the bench's
// taken/ as taken/<worker>-<name>.card, and reports the card's name and whether one
// was taken. Claiming, renaming, and claim cleanup are synchronized per card under
// takeCardLock, eliminating all check-to-action windows across processes.
func TakeCard(benchDir, worker string) (string, bool, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return "", false, fmt.Errorf("worker name is empty")
	}
	taken := TakenDir(benchDir)
	queue := QueueDir(benchDir)
	names, err := QueueCards(benchDir)
	if err != nil {
		return "", false, err
	}
	if len(names) == 0 {
		return "", false, nil
	}
	if err := os.MkdirAll(taken, 0o755); err != nil {
		return "", false, err
	}
	for _, name := range names {
		unlock, err := takeCardLock(taken, name, cardLockWait)
		if err != nil {
			if errors.Is(err, ErrCardLockTimeout) {
				continue // card lock held by another process
			}
			return "", false, err
		}
		token, claimed, err := claimCardLocked(taken, name, worker)
		if err != nil {
			unlock()
			return "", false, err
		}
		if !claimed {
			unlock()
			continue
		}
		if betweenClaimAndRenameHook != nil {
			betweenClaimAndRenameHook()
		}
		dst := filepath.Join(taken, worker+"-"+name+CardExt)
		src := filepath.Join(queue, name+CardExt)
		rErr := renameSteady(src, dst)
		releaseClaimLocked(taken, name, token)
		unlock()
		if rErr != nil {
			if errors.Is(rErr, os.ErrNotExist) {
				continue // another worker took this card first
			}
			return "", false, rErr
		}
		return name, true, nil
	}
	return "", false, nil
}

// CapacityLine is a bench's capacity line: min(cores*1.5 - load1, (free_gb-25)/2,
// memfree_gb/2), floored to a whole number of workers and never negative
// (docs/SPEC-JOBS.md section 2). A non-positive cores count is a bench with no
// pinning and no bound on the load term, so the line is the smaller memory term.
func CapacityLine(cores int, load1, freeGB, memFreeGB float64) int {
	line := math.Min((freeGB-25)/2, memFreeGB/2)
	if cores > 0 {
		line = math.Min(line, float64(cores)*1.5-load1)
	}
	if line < 0 {
		return 0
	}
	return int(math.Floor(line))
}

// StealCount is how many cards an idle worker may steal from a victim whose queue
// holds queued cards and whose capacity line is capacity: everything above the line,
// so the victim keeps enough to fill its own workers. A victim at or below its line
// is left alone; a negative capacity is an unbounded line and nothing is stolen.
func StealCount(queued, capacity int) int {
	if capacity < 0 || queued <= capacity {
		return 0
	}
	return queued - capacity
}

// FullestBench returns the bench with the most cards in its queue/ among the named
// bench directories, its queued count, and whether any of them holds a card. Ties
// go to the lexicographically first name so the choice is deterministic.
func FullestBench(benches []string) (name string, queued int, ok bool, err error) {
	sorted := append([]string(nil), benches...)
	sort.Strings(sorted)
	for _, b := range sorted {
		cards, cerr := QueueCards(b)
		if cerr != nil {
			return "", 0, false, cerr
		}
		if len(cards) > queued {
			name, queued, ok = b, len(cards), true
		}
	}
	return name, queued, ok, nil
}

// Steal takes from a victim bench's queue/ everything above its capacity line,
// renaming each card into the victim's taken/ as taken/<worker>-<name>.card, and
// returns the names taken. It never empties the victim below its line: each card is
// counted before it is renamed, and a victim at or below the line is left alone. A
// negative capacity is an unbounded line and steals nothing.
// Stealing is synchronized per card under takeCardLock.
func Steal(victimDir, worker string, capacity int) ([]string, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return nil, fmt.Errorf("worker name is empty")
	}
	if capacity < 0 {
		return nil, nil
	}
	queue := QueueDir(victimDir)
	taken := TakenDir(victimDir)
	var stolen []string

	names, err := QueueCards(victimDir)
	if err != nil {
		return stolen, err
	}
	toSteal := len(names) - capacity
	if toSteal <= 0 {
		return stolen, nil
	}
	if err := os.MkdirAll(taken, 0o755); err != nil {
		return stolen, err
	}

	for _, name := range names {
		if len(stolen) >= toSteal {
			break
		}
		unlock, err := takeCardLock(taken, name, cardLockWait)
		if err != nil {
			if errors.Is(err, ErrCardLockTimeout) {
				continue // card lock held by another process; advance to next candidate card
			}
			return stolen, err // lock setup or filesystem failure; propagate immediately
		}
		token, claimed, cErr := claimCardLocked(taken, name, worker)
		if cErr != nil {
			unlock()
			return stolen, cErr
		}
		if !claimed {
			unlock()
			continue // claimed by another worker; advance to next candidate card
		}
		if betweenClaimAndRenameHook != nil {
			betweenClaimAndRenameHook()
		}
		src := filepath.Join(queue, name+CardExt)
		dst := filepath.Join(taken, worker+"-"+name+CardExt)
		rErr := renameSteady(src, dst)
		releaseClaimLocked(taken, name, token)
		unlock()
		if rErr != nil {
			if errors.Is(rErr, os.ErrNotExist) {
				continue
			}
			return stolen, rErr
		}
		stolen = append(stolen, name)
	}
	return stolen, nil
}

// MirrorDue reports whether an idle worker's steal is due: the bench mirror fetches
// on a five-minute timer, so a worker steals at most once per MirrorTimer. A zero
// lastSteal is a worker that has never stolen and is due.
func MirrorDue(lastSteal, now time.Time) bool {
	if lastSteal.IsZero() {
		return true
	}
	return now.Sub(lastSteal) >= MirrorTimer
}
