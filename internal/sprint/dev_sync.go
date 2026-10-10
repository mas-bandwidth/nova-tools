package sprint

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// Dev sync is run by nova-sprint land only when --dev-sync is selected.
const (
	FieldDevSyncAt        = "dev_sync_at"
	FieldDevSyncBaseLacks = "dev_base_lacks"
	FieldDevSyncDevLacks  = "dev_dev_lacks"
	NDevSyncConflict      = "landing stopped: conflict syncing dev into base"
	NDevSynced            = "dev synced into base"
	DevSyncCause          = "dev sync conflict"
	DevSyncEveryLandings  = 1
	DevSyncAge            = 30 * time.Minute
)

func (d DevDrift) String() string {
	return fmt.Sprintf("base lacks %d, dev lacks %d (%dm since sync)", d.BaseLacks, d.DevLacks, d.Minutes)
}

func (d DevDrift) InSync() bool { return d.BaseLacks == 0 }

// LandedSinceSync is how many primaries (sentinels aside) landed after the last dev sync.
func LandedSinceSync(s *Snapshot) int {
	if s == nil || s.Work == nil {
		return 0
	}
	at, _, _ := LastDevSync(s)
	n := 0
	for _, c := range s.Work.Column(Landed) {
		if IsSentinel(c) {
			continue
		}
		if t, err := time.Parse(time.RFC3339, c.F("landed")); err == nil && t.After(at) {
			n++
		}
	}
	return n
}

// DevSyncDue says this land cycle syncs the development branch into the base, and why: a
// conflict is open (each cycle tries again, and the clean one closes it), none is recorded,
// DevSyncEveryLandings landed since the last, or DevSyncAge passed since it.
func DevSyncDue(s *Snapshot) (bool, string) {
	if len(openDevSyncConflicts(s)) > 0 {
		return true, "a dev sync conflict is open"
	}
	at, _, ok := LastDevSync(s)
	if !ok {
		return true, "no dev sync recorded"
	}
	if n := LandedSinceSync(s); n >= DevSyncEveryLandings {
		return true, fmt.Sprintf("%d landed since the last dev sync", n)
	}
	if s.Now.Sub(at) >= DevSyncAge {
		return true, "the last dev sync was " + s.Now.Sub(at).Truncate(time.Minute).String() + " ago"
	}
	return false, ""
}

func openDevSyncConflicts(s *Snapshot) []Open {
	if s == nil {
		return nil
	}
	var out []Open
	for _, o := range s.Open {
		if o.Note.Type == NDevSyncConflict {
			out = append(out, o)
		}
	}
	return out
}

// DevSyncReq is one dev sync in the land round's clone.
type DevSyncReq struct {
	RepoDir string   // the land round's clone, its origin the remote pushed to
	Base    string   // the base branch, e.g. sprint/mechanical-2026-10-02
	Dev     string   // the development branch; empty is DevBranch
	Remote  string   // empty is origin
	Env     []string // git's whole environment, the land round's (nil inherits)
	// Check is the land round's tree gate, run in RepoDir on the merged tree before the
	// push: a dev sync lands like a batch, through the same gate, never a second one. It is
	// required: a sync with no gate refuses.
	Check func(ctx context.Context, dir string) error
	// Streams are the streams a conflict stops; empty is every stream.
	Streams []string
}

// DevSyncFacts is what one dev sync did in git: the drift measured before it, the merge
// pushed, or the conflict and its files.
type DevSyncFacts struct {
	Base, Dev string
	Drift     DevDrift // measured on the fetched refs before the merge
	Synced    bool     // a merge was pushed onto the base
	MergeSha  string   // the base's tip after the sync (the merge, or the base when in sync)
	Conflict  bool
	Files     []string // the conflicting files
	// Left is what the clone was left holding when undoing the conflicted merge failed: the
	// land round restores its clone before the next batch.
	Left string
}

// RunDevSync merges the development branch into the base in the land round's clone: fetch,
// count the drift, merge on a detached base, and on a clean merge run the tree gate and
// push. No local branch ever holds the merge, so a red gate or a refused push leaves
// nothing behind for a batch to build on. The error is a sync that could not be decided (a
// git failure, a red gate, a refused push); a conflict is a fact, not an error.
func RunDevSync(ctx context.Context, req DevSyncReq) (DevSyncFacts, error) {
	if req.Dev == "" {
		req.Dev = DevBranch
	}
	if req.Remote == "" {
		req.Remote = "origin"
	}
	f := DevSyncFacts{Base: req.Base, Dev: req.Dev}
	switch {
	case req.Base == "":
		return f, errors.New("dev sync: no base branch")
	case req.Check == nil:
		return f, errors.New("dev sync: no tree gate; a dev sync lands through the land round's tree gate")
	}
	git := func(args ...string) (string, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{C: req.RepoDir, Env: req.Env, OwnRepo: true}, args...)
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(res.Stderr)+"\n"+string(res.Stdout)))
		}
		return strings.TrimSpace(string(res.Stdout)), nil
	}
	count := func(rng string) (int, error) {
		out, err := git("rev-list", "--count", rng)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(out)
	}
	if _, err := git("fetch", "-q", req.Remote, "+refs/heads/"+req.Base+":refs/remotes/"+req.Remote+"/"+req.Base,
		"+refs/heads/"+req.Dev+":refs/remotes/"+req.Remote+"/"+req.Dev); err != nil {
		return f, fmt.Errorf("dev sync: %w", err)
	}
	baseRef, devRef := req.Remote+"/"+req.Base, req.Remote+"/"+req.Dev
	var err error
	if f.Drift.BaseLacks, err = count(baseRef + ".." + devRef); err != nil {
		return f, fmt.Errorf("dev sync: %w", err)
	}
	if f.Drift.DevLacks, err = count(devRef + ".." + baseRef); err != nil {
		return f, fmt.Errorf("dev sync: %w", err)
	}
	if f.MergeSha, err = git("rev-parse", baseRef); err != nil {
		return f, fmt.Errorf("dev sync: %w", err)
	}
	if f.Drift.InSync() {
		return f, nil
	}
	if _, err := git("checkout", "-q", "--detach", baseRef); err != nil {
		return f, fmt.Errorf("dev sync: %w", err)
	}
	msg := fmt.Sprintf("land dev sync: merge %s into %s", req.Dev, req.Base)
	if _, mergeErr := git("merge", "--no-ff", "-m", msg, devRef); mergeErr != nil {
		files, err := git("diff", "--name-only", "--diff-filter=U")
		if err == nil {
			f.Files = strings.Fields(files)
		}
		if _, err := git("merge", "--abort"); err != nil {
			f.Left = "the conflicted merge of " + devRef + " (" + err.Error() + ")"
		}
		if len(f.Files) == 0 {
			// not a conflict: git refused the merge for its own reason
			return f, fmt.Errorf("dev sync: %w", mergeErr)
		}
		f.Conflict = true
		return f, nil
	}
	if gateErr := req.Check(ctx, req.RepoDir); gateErr != nil {
		if _, err := git("reset", "-q", "--hard", baseRef); err != nil {
			return f, errors.Join(fmt.Errorf("dev sync: the tree gate: %w", gateErr), fmt.Errorf("dev sync: the detached HEAD still holds the ungated merge: %w", err))
		}
		return f, fmt.Errorf("dev sync: the tree gate: %w", gateErr)
	}
	sha, err := git("rev-parse", "HEAD")
	if err != nil {
		return f, fmt.Errorf("dev sync: %w", err)
	}
	if _, err := git("push", "-q", req.Remote, "HEAD:refs/heads/"+req.Base); err != nil {
		return f, fmt.Errorf("dev sync: %w", err)
	}
	f.Synced, f.MergeSha = true, sha
	return f, nil
}

// DevSynced records a dev sync's facts (the pure half; docs/SPEC-SPRINT.md, "Dev sync every
// cycle"): the drift and the last sync on the merge table and on each stream's control card;
// a conflict stops each stream with ONE judgment naming the files, kept (its text brought up
// to date) while the conflict stays; a clean sync, or a base that already holds dev, closes
// it and resumes every stream it stopped.
func DevSynced(s *Snapshot, f DevSyncFacts, streams []string) Plan {
	var p Plan
	if len(streams) == 0 {
		streams = s.Streams()
	}
	now := stamp(s.Now)
	after := f.Drift
	props := map[string]string{PropDevSyncBaseLacks: strconv.Itoa(after.BaseLacks), PropDevSyncDevLacks: strconv.Itoa(after.DevLacks)}
	if !f.Conflict {
		after.BaseLacks = 0
		if f.Synced {
			after.DevLacks++ // the merge commit
		}
		props = map[string]string{PropDevSyncBaseLacks: "0", PropDevSyncDevLacks: strconv.Itoa(after.DevLacks), PropDevSyncAt: now, PropDevSyncSha: f.MergeSha}
	}
	for _, name := range []string{PropDevSyncAt, PropDevSyncSha, PropDevSyncBaseLacks, PropDevSyncDevLacks} {
		v, ok := props[name]
		if !ok {
			continue
		}
		if was, had := s.Merge.Prop(name); !had || was != v {
			p.Props = append(p.Props, PropWrite{Table: Merge, Name: name, Value: v, Was: was, WasAbsent: !had})
		}
	}
	open := openDevSyncConflicts(s)
	files := strings.Join(f.Files, ", ")
	what := fmt.Sprintf("dev sync conflict: merging %s into %s conflicts in %s (%s); merge %s into %s by hand and push, and the next land cycle resumes every stream",
		f.Dev, f.Base, files, f.Drift, f.Dev, f.Base)
	if f.Left != "" {
		what += "; the land clone still holds " + f.Left
	}
	noted, closed := false, f.Conflict || len(open) == 0
	for _, stream := range streams {
		ctl := s.StreamCtl(stream)
		if ctl == nil {
			continue
		}
		set := map[string]string{FieldDevSyncBaseLacks: props[PropDevSyncBaseLacks], FieldDevSyncDevLacks: props[PropDevSyncDevLacks]}
		if at, ok := props[PropDevSyncAt]; ok {
			set[FieldDevSyncAt] = at
		}
		u := Unit{Key: ctl.ID, Stream: stream}
		switch {
		case f.Conflict && ctl.F("state") != StreamStopped:
			set["state"], set["since"], set["cause"] = StreamStopped, now, DevSyncCause
			set[FieldConflictPaths] = cutText(strings.Join(f.Files, ","), MaxProviderErrorBytes)
			u.Moved = fmt.Sprintf("stream %s stopped: dev sync conflict in %s", stream, files)
		case !f.Conflict && ctl.F("state") == StreamStopped && ctl.F("cause") == DevSyncCause:
			set["state"], set["since"], set["cause"], set[FieldConflictPaths] = StreamMerging, now, "", ""
			u.Moved = fmt.Sprintf("stream %s resumed: %s takes %s cleanly", stream, f.Base, f.Dev)
		}
		// the one judgment, and its close, go with the first stream's unit
		if f.Conflict && len(open) == 0 && !noted {
			j := judgment(NDevSyncConflict, stream, s.Now, 0)
			j.StreamLevel, j.What = true, what
			j.Decisions = []string{"merged by hand", "wait"}
			u.Notes, noted = append(u.Notes, j), true
		}
		if !closed {
			u.Closes, closed = append(u.Closes, open...), true
		}
		changed := false
		for k, v := range set {
			if ctl.F(k) != v {
				changed = true
			}
		}
		if !changed && len(u.Notes) == 0 && len(u.Closes) == 0 {
			continue
		}
		if u.Moved == "" {
			u.Moved = fmt.Sprintf("stream %s: %s", stream, after)
		}
		u.Changes = []Change{change(Merge, setEntry(ctl, set))}
		p.Units = append(p.Units, u)
	}
	if f.Conflict && len(open) > 0 {
		for _, o := range open {
			if o.Note.What != what {
				n := o.Note
				n.What = what
				p.Updates = append(p.Updates, n)
			}
		}
	}
	if f.Synced {
		n := happened(NDevSynced, "", s.Now)
		if len(streams) > 0 {
			n.Stream = streams[0]
		}
		n.What = fmt.Sprintf("merged %s into %s at %s (%s before it)", f.Dev, f.Base, f.MergeSha, f.Drift)
		p.Notes = append(p.Notes, n)
	}
	return p
}
