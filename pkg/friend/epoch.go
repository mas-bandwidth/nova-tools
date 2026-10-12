package friend

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// The epoch snap (the finding of 2026-10-10: after a clear, one friend's working dir kept 323
// inbox items and 535 outbox REPORTs of epoch 15, each outbox one refused on every pass, "not
// on her row"). On the sprint's new epoch the daemon moves every older-epoch inbox and outbox
// item to the retired folder named for its epoch — kept, never deleted, so finished work is
// not lost — and the live folders start the new epoch empty. On the same signal the lanes
// forget their sessions and the present is owed, so a full or stale session never carries
// across a clear (docs/SPEC-FRIEND.md, the inbox; the inbox's retire and the present, present.go).

// RetiredEpochs is the folder under the friend's working directory that keeps the items the
// epochs retired, named for its epoch: retired/epoch-<n>/inbox/<job> and
// retired/epoch-<n>/outbox/<job>.
const RetiredEpochs = "retired"

// SnapEpoch moves every older-epoch inbox and outbox item under dir to the retired folder
// named for its epoch, at the sprint's epoch now (Row.Epoch, her row's answer): every inbox
// and outbox job whose name parses to an epoch older than it (ParseJob), that no lane runs
// (keep) and that no card on her row holds (row.Cards), and whose directory stood before the
// ask began (asked, as the inbox's retire guards friend sync's write), is moved whole, so
// finished work is kept, never deleted, and the live folders start the new epoch empty. A job
// whose name parses to no epoch, and one that is no single path element, is never moved (a job
// any other hand put there: docs/SPEC-FRIEND.md, the inbox). record gets one line per move;
// the first error is answered, the rest still moved. It answers how many moved. Zero is a
// sprint epoch of no card's naming: nothing is retired on it.
func SnapEpoch(dir string, row Row, keep map[string]bool, asked, now time.Time, record func(string)) (int, error) {
	if row.Epoch == 0 {
		return 0, nil
	}
	want := map[string]bool{}
	for _, h := range row.Cards {
		want[h.Job] = true
	}
	moved, firstErr := 0, error(nil)
	for _, sub := range []string{"inbox", "outbox"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				firstErr = cmp.Or(firstErr, err)
			}
			continue
		}
		for _, e := range entries {
			job := e.Name()
			if !e.IsDir() || !validJob(job) {
				continue
			}
			_, epoch, _, ok := ParseJob(job)
			if !ok || uint64(epoch) >= row.Epoch || want[job] || keep[job] || !writtenBefore(filepath.Join(dir, sub, job), asked) {
				continue
			}
			to, err := retireEpoch(dir, sub, job, epoch, now)
			if err != nil {
				firstErr = cmp.Or(firstErr, err)
				continue
			}
			moved++
			record(fmt.Sprintf("%s epoch: retired %s/%s to %s: its card is of epoch %d, the sprint is at %d", now.UTC().Format(time.RFC3339), sub, job, to, epoch, row.Epoch))
		}
	}
	return moved, firstErr
}

// retireEpoch moves <sub>/<job> to retired/epoch-<e>/<sub>/<job>, or <job>.<unix> when that
// is taken; it answers where it went, as a path under the working directory.
func retireEpoch(dir, sub, job string, epoch int, now time.Time) (string, error) {
	to := filepath.Join(dir, RetiredEpochs, "epoch-"+strconv.Itoa(epoch), sub)
	if err := os.MkdirAll(to, 0o755); err != nil {
		return "", err
	}
	name := job
	if _, err := os.Lstat(filepath.Join(to, name)); err == nil {
		name = job + "." + strconv.FormatInt(now.Unix(), 10)
	}
	if err := os.Rename(filepath.Join(dir, sub, job), filepath.Join(to, name)); err != nil {
		return "", err
	}
	return RetiredEpochs + "/epoch-" + strconv.Itoa(epoch) + "/" + sub + "/" + name, nil
}

// freshLanes is the signal's session half (docs/SPEC-FRIEND.md, the lanes keep their state,
// lanes.json): the lanes' sessions are forgotten, so the next open of every lane is fresh
// (the seed given again, LaneSeed), never a full or stale one carried across a clear. A lane
// whose turn runs keeps nothing to resume: its DeliverTo was taken before the signal.
func (l *loop) freshLanes(now time.Time) {
	s := l.lanes
	s.state.Epoch = l.d.epoch
	s.state.Sessions = map[int]string{}
	for _, ln := range s.lanes {
		ln.session = ""
	}
	l.saveLanes(now)
	l.d.Record(fmt.Sprintf("%s epoch: the lanes open fresh sessions (the sprint is at epoch %d)", now.UTC().Format(time.RFC3339), l.d.epoch))
}
