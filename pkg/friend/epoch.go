package friend

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// After a clear one friend's working directory kept 323 inbox items and 535 outbox
// REPORTs from the old epoch, each refused ("not on her row") every pass: the sprint had
// moved to a new epoch and nothing moved the old epoch's work. A clear is a new sprint
// epoch, and the inbox and outbox names carry it (<work>~<epoch>[.g<gen>], ParseJob), so
// one reconcile at a new epoch moves every older-epoch inbox and outbox job, whole, to
// retired/epoch-<its epoch>/{inbox,outbox}: kept, never deleted, so finished work is not
// lost, and the live folders are the new epoch's and empty of the old. The same signal
// starts a fresh session for the next presence check and lane (laneStep), so a full or
// stale session never carries across a clear (docs/FRIENDS.md, the inbox/outbox standard).

// RetiredEpochRoot is the directory an epoch's retired work lives under: retired/.
const RetiredEpochRoot = "retired"

// RetiredEpochDir is the folder an epoch's work is kept under, named for the epoch:
// retired/epoch-<n>.
func RetiredEpochDir(epoch int) string {
	return RetiredEpochRoot + "/epoch-" + strconv.Itoa(epoch)
}

// RowEpoch is the sprint epoch her row is at: the greatest epoch any held card names,
// from the card's epoch or, when the answer sends none, its job's name (ParseJob); 0 when
// no card names one.
func RowEpoch(cards []HeldCard) int {
	epoch := 0
	for _, c := range cards {
		if int(c.Epoch) > epoch {
			epoch = int(c.Epoch)
		}
		if _, e, _, ok := ParseJob(c.Job); ok && e > epoch {
			epoch = e
		}
	}
	return epoch
}

// SnapToEpoch moves every job in her inbox and outbox that names an epoch older than
// epoch, whole, to retired/epoch-<its epoch>/{inbox,outbox} under dir. A job a lane runs
// (keep) and a job that names no epoch (a hand-placed note) are never moved, and a job of
// the current or a later epoch is left; a destination already taken is suffixed with the
// time, as retire does. record gets one line per move. It answers how many jobs moved and
// the first error; a move that fails is left where it was, for the next pass. (docs/FRIENDS.md,
// the inbox/outbox standard)
func SnapToEpoch(dir string, epoch int, keep map[string]bool, now time.Time, record func(string)) (int, error) {
	if epoch <= 0 {
		return 0, nil
	}
	at := now.UTC().Format(time.RFC3339)
	moved := 0
	var firstErr error
	for _, live := range []string{"inbox", "outbox"} {
		entries, err := os.ReadDir(filepath.Join(dir, live))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return moved, cmpErr(firstErr, err)
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || name == RetiredEpochRoot || keep[name] || !validJob(name) {
				continue
			}
			_, jobEpoch, _, ok := ParseJob(name)
			if !ok || jobEpoch >= epoch {
				continue
			}
			to := filepath.Join(dir, filepath.FromSlash(RetiredEpochDir(jobEpoch)), live)
			if err := os.MkdirAll(to, 0o755); err != nil {
				firstErr = cmpErr(firstErr, err)
				continue
			}
			dest := name
			if _, err := os.Lstat(filepath.Join(to, dest)); err == nil {
				dest = name + "." + strconv.FormatInt(now.Unix(), 10)
			}
			if err := os.Rename(filepath.Join(dir, live, name), filepath.Join(to, dest)); err != nil {
				firstErr = cmpErr(firstErr, err)
				continue
			}
			moved++
			record(fmt.Sprintf("%s epoch: retired %s/%s to %s/%s/%s: epoch %d is retired, epoch %d is live", at, live, name, RetiredEpochDir(jobEpoch), live, dest, jobEpoch, epoch))
		}
	}
	return moved, firstErr
}
