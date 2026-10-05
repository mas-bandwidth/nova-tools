package sprint

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// THE LANDER'S PAUSE (docs/SPEC-SPRINT.md section 7, the lander's pause). The lander does
// not land onto a branch while a merge window is open, for its duration with its reason
// shown, nor while that branch's merge queue on the forge holds a group: a direct push
// under a group being checked moves the base the group was cut from. The cards stay
// queued, nothing is recorded and no stream stops; the next round asks again. A queue
// that cannot be read pauses as a held one does: an unreadable queue is not an empty one.

// The merge window's properties, the merge table's (merge-window open): its end, RFC 3339
// in UTC, and its reason. A clear starts the next epoch with neither, as it starts every
// property.
const (
	PropMergeWindowUntil  = "merge_window_until"
	PropMergeWindowReason = "merge_window_reason"
)

// MergeWindow is a window the coordinator opened: landing pauses until Until, for Reason.
// The zero window is none.
type MergeWindow struct {
	Until  time.Time
	Reason string
}

// MergeWindowReq is merge-window open: how long, why, and who asks.
type MergeWindowReq struct {
	For    string `json:",omitempty"`
	Reason string `json:",omitempty"`
	Who    string
}

// MergeWindowOpen opens the merge window from the step's clock for r.For (docs/SPEC-SPRINT.md
// section 7, the lander's pause): its end and reason written as the merge table's
// properties, guarded on the values read, over any window open before. Refused whole,
// writing nothing, naming every problem: an actor who is not the coordinator, a duration
// that is none or not above zero, no reason, or one over the text bound.
func MergeWindowOpen(s *Snapshot, r MergeWindowReq) Plan {
	var p Plan
	var why []string
	if w := notCoordinator(s, r.Who, "merge-window open"); w != "" {
		why = append(why, strings.Replace(w, "answers a judgment, which is", "is", 1))
	}
	d, err := time.ParseDuration(r.For)
	if err != nil || d <= 0 {
		why = append(why, "--for wants a duration above zero (10m, 1h); found "+orDash(r.For))
	}
	switch {
	case strings.TrimSpace(r.Reason) == "":
		why = append(why, "--reason wants why landing pauses, shown on every paused landing")
	case len(r.Reason) > MaxCardTextBytes:
		why = append(why, fmt.Sprintf("--reason is %d bytes, over the bound of %d", len(r.Reason), MaxCardTextBytes))
	}
	if len(why) > 0 {
		p.refuse("merge-window", strings.Join(why, "; "))
		return p
	}
	until := s.Now.Add(d).UTC().Format(time.RFC3339)
	for _, kv := range [][2]string{{PropMergeWindowUntil, until}, {PropMergeWindowReason, r.Reason}} {
		was, had := s.Merge.Prop(kv[0])
		p.Props = append(p.Props, PropWrite{Table: Merge, Name: kv[0], Value: kv[1], Was: was, WasAbsent: !had})
	}
	p.Units = append(p.Units, Unit{Key: "merge-window", Moved: "merge window open until " + until + " (" + r.Reason + "): landing pauses"})
	return p
}

// MergeWindow is the merge window the merge table's properties hold, the zero window when
// none was opened; an end that cannot be read is an error, never no window.
func (s *Snapshot) MergeWindow() (MergeWindow, error) {
	v, ok := s.Merge.Prop(PropMergeWindowUntil)
	if !ok {
		return MergeWindow{}, nil
	}
	until, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return MergeWindow{}, fmt.Errorf("the merge table's %s %q is not an RFC 3339 time: %w", PropMergeWindowUntil, v, err)
	}
	reason, _ := s.Merge.Prop(PropMergeWindowReason)
	return MergeWindow{Until: until, Reason: reason}, nil
}

// MergeQueue is a forge's merge queue of a branch as the lander asks it: whether it holds
// a group (an entry queued or being checked) now. The lander's is the forge's API; a test
// gives a fake and asks no forge.
type MergeQueue interface {
	HoldsGroup(ctx context.Context, repo, branch string) (bool, error)
}

// LandPause is why the lander does not land onto branch of repo at now, "" when it lands
// (docs/SPEC-SPRINT.md section 7, the lander's pause): the window open, first and asking
// no forge; then the branch's merge queue holding a group, or not read. A nil q asks no
// queue (a dry run reads the store only).
func LandPause(ctx context.Context, w MergeWindow, now time.Time, q MergeQueue, repo, branch string) string {
	const stay = "; the cards stay queued and land when it "
	if now.Before(w.Until) {
		return "paused: a merge window is open until " + w.Until.UTC().Format(time.RFC3339) + " (" + w.Reason + ")" + stay + "closes"
	}
	if q == nil {
		return ""
	}
	held, err := q.HoldsGroup(ctx, repo, branch)
	switch {
	case err != nil:
		return "paused: the merge queue of " + branch + " could not be read (" + err.Error() + "), and an unreadable queue is not an empty one" + stay + "is read clear"
	case held:
		return "paused: the merge queue of " + branch + " holds a group" + stay + "clears"
	}
	return ""
}
