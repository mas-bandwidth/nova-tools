package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The daemon follows its row's release (docs/SPEC-FRIEND.md, "The row is followed"; the
// owner, 2026-10-07: "make runner upgrades mechanical. You can set the runner version via
// nova-config, and then the runners themselves in their 1s check, see if they should be on
// a different version, then automatically quit, get the new version they need, and
// restart with the new version!"). Each beat's answer carries the row's release
// (row_release=, ParseRelease). When it names a release and this daemon's build is
// another, the daemon stops taking new lanes, fetches that release's nova-friend and
// verifies it (Fetch: the release's checksum file, and the binary's own version line,
// Verify), and once no lane holds a card (or the drain bound passes) swaps the verified
// binary into place (Swap) and runs itself again under it (Exec: the same argv, the same
// pid). A fetch that fails, or a binary that does not answer with the release, leaves the
// running binary in place, is said once per release value, and is tried again after
// FollowRetry (a minute, then five), never in a tight loop. A daemon that comes up under
// the release the row names does nothing. The model is tla/FriendFollow.tla: a daemon never
// runs a build that failed verification (NeverRunsUnverified), no lane dies from a follow
// (LanesSurvive), and a changed row is followed (Follows); its reversed witnesses swap an
// unverified binary, restart over running lanes, and never try a failed fetch again.

// FollowRetry are the waits before a failed follow is tried again: the first after the
// first failure, the last after every failure past the list.
var FollowRetry = []time.Duration{time.Minute, 5 * time.Minute}

// FollowDrain bounds the wait for the lanes to finish their cards before the restart:
// past it the daemon restarts anyway and says which lanes still ran (their cards are
// finished by the daemon that comes up, as every started card whose run is gone is,
// lane_end.go). It is the longest default lane cap (DefaultLaneCaps) and a little over.
const FollowDrain = 3 * time.Hour

// Follower is the daemon's follow of its row's release: the pure machine, every edge a
// field, so a test drives it with fakes and no process is replaced.
type Follower struct {
	// Build is this daemon's build, its version stamp; "" is an unstamped build (a hand
	// build), which follows nothing: a build nobody cut is nobody's to replace.
	Build string
	Now   func() time.Time
	// Fetch fetches and verifies the release's nova-friend and answers the staged path
	// beside the running binary (release.FetchTool); Verify runs the staged binary's
	// version verb and answers its version word; Swap renames the staged binary into
	// place; Exec runs this process again under the binary in place, with the same
	// argv, and returns only when it could not; Remove drops a staged binary.
	Fetch  func(ctx context.Context, release string) (staged string, err error)
	Verify func(ctx context.Context, staged string) (version string, err error)
	Swap   func(staged string) error
	Exec   func() error
	Remove func(staged string) error
	Record func(line string)

	mu        sync.Mutex
	want      string // the row's release as last read
	staged    string // the verified binary waiting for the lanes, "" while none
	fetching  bool
	results   chan followResult
	failedFor string // the release value whose failure was said
	failures  int
	retryAt   time.Time
	drainFrom time.Time // when the follow began holding the lanes; zero while none
	saidFor   string    // the release value the unstamped refusal was said for
}

type followResult struct {
	release string
	staged  string
	err     error
}

// ParseRelease is the row's release as the beat's answer carries it (row_release=<tag>,
// beside row_mode and row_width); ok is false when the answer carries none, which is a row
// that names none.
func ParseRelease(answer string) (release string, ok bool) {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_release="); found {
			return v, true
		}
	}
	return "", false
}

// Want is the row's release as the beat last answered it, read each beat; a change is
// said and starts the follow over.
func (f *Follower) Want(release string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if release == f.want {
		return
	}
	f.say("CONFIG release=%s (was %s) from the friend row", dash(release), dash(f.want))
	f.want = release
	f.failedFor, f.failures, f.retryAt = "", 0, time.Time{}
	f.dropStaged()
}

// Following says whether a follow is under way: the row names a release this build is not.
func (f *Follower) Following() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.want != "" && f.want != f.Build && f.Build != ""
}

// Step is the follow's step, once a daemon step: idle says no lane holds a card and no turn
// or read runs. It answers hold, whether the daemon takes no new lane, turn or read this
// step. The fetch runs beside the daemon and its result is read here; the swap and the
// restart run here, in the daemon's own step, so nothing starts between them.
func (f *Follower) Step(ctx context.Context, now time.Time, idle bool) (hold bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.want == "" || f.want == f.Build {
		f.dropStaged()
		f.drainFrom = time.Time{}
		return false
	}
	if f.Build == "" {
		if f.saidFor != f.want {
			f.saidFor = f.want
			f.say("release follow: the row names %s and this build is unstamped (a hand build); it follows nothing", f.want)
		}
		return false
	}
	if f.drainFrom.IsZero() {
		f.drainFrom = now
		f.say("release follow: the row names %s and this daemon runs %s: taking no new lanes; fetching it", f.want, f.Build)
	}
	f.takeResult(now)
	if f.staged == "" && !f.fetching && !now.Before(f.retryAt) {
		f.fetching = true
		if f.results == nil {
			f.results = make(chan followResult, 1)
		}
		release := f.want
		go func() {
			staged, err := f.Fetch(ctx, release)
			if err == nil {
				var version string
				version, err = f.Verify(ctx, staged)
				if err == nil && version != release {
					err = fmt.Errorf("the fetched binary answers version %s, not %s", dash(version), release)
				}
				if err != nil && f.Remove != nil {
					_ = f.Remove(staged) // ignored: a staged binary that failed verification is dropped; the failure that drops it is the one said
				}
			}
			f.results <- followResult{release: release, staged: staged, err: err}
		}()
		return true
	}
	if f.staged == "" {
		return true
	}
	if !idle {
		if now.Sub(f.drainFrom) < FollowDrain {
			return true
		}
		f.say("release follow: lanes still run %s after the row named %s; restarting under it anyway, their cards are finished by the daemon that comes up", FollowDrain, f.want)
	}
	if err := f.Swap(f.staged); err != nil {
		f.fail(now, fmt.Errorf("the verified binary cannot be put in place: %w", err))
		return true
	}
	f.say("release follow: %s is in place; restarting under it with the same arguments", f.want)
	if err := f.Exec(); err != nil {
		f.staged = ""
		f.fail(now, fmt.Errorf("the restart under %s failed: %w", f.want, err))
	}
	return true
}

// takeResult reads a finished fetch, if one is there: a result for the release still
// wanted is staged, or its failure counted; one for a release the row has left is dropped.
func (f *Follower) takeResult(now time.Time) {
	if !f.fetching {
		return
	}
	select {
	case r := <-f.results:
		f.fetching = false
		switch {
		case r.release != f.want:
			if r.err == nil && f.Remove != nil {
				_ = f.Remove(r.staged) // ignored: the row moved on while it was fetched; nothing of it is kept
			}
		case r.err != nil:
			f.fail(now, r.err)
		default:
			f.staged = r.staged
			f.say("release follow: %s fetched and verified at %s; waiting for the lanes to finish their cards", r.release, r.staged)
		}
	default:
	}
}

// fail counts a failure of the release wanted, says it once per release value, and sets
// when the follow is tried again.
func (f *Follower) fail(now time.Time, err error) {
	f.failures++
	wait := FollowRetry[min(f.failures, len(FollowRetry))-1]
	f.retryAt = now.Add(wait)
	if f.failedFor != f.want {
		f.failedFor = f.want
		f.say("release follow: %s not followed: %s; the running build %s stays in place; tried again in %s, then every %s", f.want, oneLine(err.Error(), 300), f.Build, wait, FollowRetry[len(FollowRetry)-1])
	}
}

func (f *Follower) dropStaged() {
	if f.staged != "" && f.Remove != nil {
		_ = f.Remove(f.staged) // ignored: a staged binary the row no longer wants
	}
	f.staged = ""
}

func (f *Follower) say(format string, a ...any) {
	if f.Record == nil {
		return
	}
	f.Record(f.Now().UTC().Format(time.RFC3339) + " " + fmt.Sprintf(format, a...))
}

// ErrNoReleaseRepo is the fetch's refusal when the daemon knows no repository to fetch a
// release from (nova-friend run --release-repo).
var ErrNoReleaseRepo = errors.New("no release repository: run the daemon with --release-repo <owner/name> (nova-friend install writes it into the agent)")
