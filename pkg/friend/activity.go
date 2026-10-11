package friend

import (
	"io/fs"
	"time"
)

// The session's last activity (docs/SPEC-FRIEND.md, last session activity): a daemon's
// pong shows the daemon answers, never that her session is moving (the finding of
// 2026-10-04: a friend idle from 2:40 to 4:34 PM read up with 8 working, and another
// read working=0 while she was busy). The signal is the newest file write under her
// working directory, the outbox and inbox first, found by one stat walk bounded in files
// and in time, and carried on her beat.

// ActivityEvery is how often the daemon walks: the beat goes every BeatEvery and carries
// the last walk's answer in between.
const ActivityEvery = 10 * time.Second

// ActivityLimits bounds one walk: the files it reads the time of (a directory costs time,
// not files) and the time it may take on the clock it is given. A walk that reaches either
// answers with the newest write it has read.
type ActivityLimits struct {
	Files int
	Time  time.Duration
}

// DefaultActivityLimits is what the daemon walks with: two thousand files, fifty
// milliseconds.
var DefaultActivityLimits = ActivityLimits{Files: 2000, Time: 50 * time.Millisecond}

// activitySkip are the directories no session writes its work in: a clone's index and
// the build caches change under a tool, not under her, and .nova-friend is her daemon's
// own state (StateDirIn), rewritten every few seconds.
var activitySkip = map[string]bool{".git": true, ".cache": true, "node_modules": true, ".nova-friend": true}

// activitySkipFile are the files her daemon writes, never her session: a job's lane mark,
// refreshed every LaneMarkEvery while a lane runs (one_lane.go).
var activitySkipFile = map[string]bool{LaneMarkFile: true}

// ActivityRoots are the places the walk reads, in this order, under her working directory:
// the outbox (her results), the inbox (the cards she was dealt), then the jobs and the
// rest of the directory, so a large clone cannot use the bound up before the outbox is read.
var ActivityRoots = []string{"outbox", "inbox", "jobs", "."}

// NewestWrite is the newest modification time of a file under the roots of fsys, zero when
// there is none (a root that is not there, an empty tree). It reads at most lim.Files files
// and runs at most lim.Time by now's clock, never descends into activitySkip, and reads a root named earlier once.
func NewestWrite(fsys fs.FS, roots []string, now func() time.Time, lim ActivityLimits) time.Time {
	var newest time.Time
	start, files, walked := now(), 0, map[string]bool{}
	for _, root := range roots {
		walked[root] = true
		// ignored: the walk's own error is every entry's, handled in the callback; a tree that cannot be walked has no write
		_ = fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // a root or entry that is not there is no write; a directory that cannot be read is skipped
			}
			if now().Sub(start) > lim.Time {
				return fs.SkipAll
			}
			if d.IsDir() {
				if path != root && (activitySkip[d.Name()] || walked[path]) { // a root read before is not read twice
					return fs.SkipDir
				}
				return nil
			}
			if files >= lim.Files {
				return fs.SkipAll
			}
			if activitySkipFile[d.Name()] {
				return nil
			}
			files++
			if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			return nil
		})
		if files >= lim.Files || now().Sub(start) > lim.Time {
			break
		}
	}
	return newest
}
