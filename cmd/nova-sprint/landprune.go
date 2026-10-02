package main

// The landing's cleanup (docs/SPEC-SPRINT.md, land). A landed card's commits are on the
// base, so its branch on origin has nothing left to give; a repository that keeps one
// branch per card of every run makes every push receive thousands of refs (the fleet pass
// of 2026-10-01: 3,564 branches, each push 20 to 35 s, and landing is the sprint's one
// serial stage). A batch whose report is recorded only TAGS its cards' branches: they go
// on a queue in this process's memory (no store field, table or column). The cleaner
// deletes them later, many in one push, then removes the clone's remote-tracking refs of
// branches origin no longer holds. It never runs while a landing builds or pushes: the
// one-shot land flushes once, after every stream; the server's land loop flushes between
// its rounds, on a round with nothing queued to merge or once PruneEvery branches wait,
// so a clone is never touched by two gits of land's at once and no lock is held. A failed
// cleanup is a PRUNE FAILED line; the loop keeps the branches queued and tries again
// after PruneRetry. A crash loses the queue, and those branches stay on origin.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// PruneEvery is how many queued branches make the land loop clean up between two busy rounds.
const PruneEvery = 256

// PruneRetry is how long the land loop waits after a failed cleanup before it tries again.
const PruneRetry = time.Minute

// pruneChunk bounds the branches one delete push names.
const pruneChunk = 500

// cardBranchRE is a branch a card may name for deletion: one the sprint names
// (sprint.BranchOf: sprint/<prefix><card>.g<gen>.e<epoch>), with no refspec character.
var cardBranchRE = regexp.MustCompile(`^sprint/[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// landPrune is what a landed batch put on the cleanup queue (a dry run: would put).
type landPrune struct {
	Queued int `json:"queued"`
	// Kept is each branch a card names that is never deleted, and why: "<card>: why".
	Kept []string `json:"kept,omitempty"`
	// Why is why the cards' records could not be read: nothing was queued.
	Why string `json:"why,omitempty"`
}

// pruneWhy is why a branch a card records is never deleted, "" when it may be: an empty
// name, the batch's base, a name that is an option or no branch the sprint names.
func pruneWhy(branch, base string) string {
	switch {
	case branch == "":
		return "it records no branch"
	case branch == base:
		return "its branch " + branch + " is the base"
	case strings.HasPrefix(branch, "-"):
		return "its branch " + branch + " is not a branch name"
	case !cardBranchRE.MatchString(branch) || strings.Contains(branch, "..") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".lock"):
		return "its branch " + branch + " is not one the sprint names (sprint/...)"
	}
	return ""
}

// tag reads the landed cards' work cards of every attempt (each attempt is its own work
// card and its own branch, sprint.WorkCardID) and puts every branch they recorded on the
// cleanup queue; a dry run only counts them. b.Prune says how many and which are kept.
func (l *lander) tag(ctx context.Context, b *landBatch, cards []landCard) {
	p := &landPrune{}
	b.Prune = p
	var ids []string
	for _, c := range cards {
		n, err := strconv.Atoi(c.attempt)
		if err != nil || n < 1 {
			p.Kept = append(p.Kept, c.id+": its attempt "+dashed(c.attempt)+" names no work card")
			continue
		}
		for k := 1; k <= n; k++ {
			ids = append(ids, sprint.WorkCardID(c.id, k))
		}
	}
	l.a.serial.Lock()
	works, err := l.st.Records(ctx, sprint.Fleet, ids)
	var bases []string
	if err == nil {
		var snapshot *sprint.Snapshot
		snapshot, err = l.st.Load(ctx, []string{sprint.Work}, nil)
		if err == nil {
			bases = append(bases, b.Base)
			for _, primary := range snapshot.Work.Cards() {
				if base := swarm.ReadCardBase([]byte(primary.F("brief"))).Ref; base != "" {
					bases = append(bases, base)
				}
			}
		}
	}
	l.a.serial.Unlock()
	if err != nil {
		p.Why = "the cards' work records could not be read (" + oneline.Err(err) + "); their branches stay on origin"
		return
	}
	slices.SortFunc(works, func(x, y *sprint.Card) int { return strings.Compare(x.ID, y.ID) })
	var branches []pruneBranch
	for _, w := range works {
		branch := w.F("branch")
		if branch == "" {
			continue // an attempt that recorded no branch (a finish by hand) has none to delete
		}
		if why := pruneWhy(branch, b.Base); why != "" {
			p.Kept = append(p.Kept, w.ID+": "+why)
			continue
		}
		if slices.Contains(bases, branch) {
			p.Kept = append(p.Kept, w.ID+": its branch "+branch+" is a stream base")
			continue
		}
		expected := sprint.BranchOf(l.st.Names.Prefix, l.epoch, w.ID, w.Int("gen"))
		head := sprint.PushedHead(w)
		if branch != expected || head == "" {
			p.Kept = append(p.Kept, w.ID+": its branch or head does not prove ownership of "+expected)
			continue
		}
		pin := pruneBranch{Branch: branch, Head: head}
		if !slices.Contains(branches, pin) {
			branches = append(branches, pin)
		}
	}
	p.Queued = len(branches)
	if p.Queued == 0 && len(p.Kept) == 0 {
		b.Prune = nil // nothing recorded, nothing to say
		return
	}
	if !l.dry && p.Queued > 0 {
		l.a.prune.add(b.Dir, b.Base, branches)
		for _, base := range bases {
			l.a.prune.add(b.Dir, base, nil)
		}
	}
}

// pruneQueue is the landed cards' branches waiting to be deleted, by the clone whose
// origin holds them; this process's memory only.
type pruneQueue struct {
	mu      sync.Mutex
	dirs    map[string]*pruneDir
	retryAt time.Time // after a failed cleanup, the loop waits until then
}

// pruneBranch pins the recorded successful attempt's branch and head. Cleanup
// never adopts a newer remote tip (docs/SPEC-SPRINT.md, land).
type pruneBranch struct{ Branch, Head string }

// pruneDir is one clone's waiting branches, and the bases landed in it: their
// remote-tracking refs are the landing's own. A clone queued with no branch is owed a
// look at its remote-tracking refs.
type pruneDir struct {
	branches []pruneBranch
	bases    []string
}

// add queues branches of the clone dir, landed on base.
func (q *pruneQueue) add(dir, base string, branches []pruneBranch) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.dirs == nil {
		q.dirs = map[string]*pruneDir{}
	}
	d := q.dirs[dir]
	if d == nil {
		d = &pruneDir{}
		q.dirs[dir] = d
	}
	if !slices.Contains(d.bases, base) {
		d.bases = append(d.bases, base)
	}
	for _, b := range branches {
		if !slices.Contains(d.branches, b) {
			d.branches = append(d.branches, b)
		}
	}
}

// waiting is how many branches are queued.
func (q *pruneQueue) waiting() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, d := range q.dirs {
		n += len(d.branches)
	}
	return n
}

// due says the land loop cleans up now: something is queued, no failed cleanup is
// waiting out PruneRetry, and the round found nothing to land or PruneEvery branches wait.
func (q *pruneQueue) due(idle bool, now time.Time) bool {
	q.mu.Lock()
	empty, wait := len(q.dirs) == 0, now.Before(q.retryAt)
	q.mu.Unlock()
	if empty || wait {
		return false
	}
	return idle || q.waiting() >= PruneEvery
}

// pruneResult is one clone's cleanup, a PRUNE line and an item of land's --json.
type pruneResult struct {
	Status   string  `json:"status"` // ok, failed
	Dir      string  `json:"dir"`
	Branches int     `json:"branches"` // deleted from origin (one already gone counts)
	Left     int     `json:"left"`     // not deleted: queued again, or (the command ending) left on origin
	Refs     int     `json:"refs"`     // remote-tracking refs removed from the clone
	Took     float64 `json:"took"`
	Reason   string  `json:"reason,omitempty"`
}

// line is the cleanup's output line.
func (r pruneResult) line(last bool) string {
	l := fmt.Sprintf("PRUNE %s branches=%d refs=%d dir=%s took=%.1fs", strings.ToUpper(r.Status), r.Branches, r.Refs, oneline.Field(r.Dir), r.Took)
	if r.Status == "ok" {
		return l
	}
	then := "they stay queued and the cleanup is tried again"
	if last {
		then = "they stay on origin"
	}
	return l + fmt.Sprintf(" left=%d reason=%s; %s", r.Left, oneline.Escape(r.Reason), then)
}

// flushPrune cleans up every clone with something queued: one delete push per pruneChunk
// branches, then the clone's stale remote-tracking refs. What did not go is queued again
// (last: the command is ending, and it is dropped and stays on origin); a failure sets
// the loop's retry time.
func (a *app) flushPrune(ctx context.Context, last bool) []pruneResult {
	q := &a.prune
	q.mu.Lock()
	dirs := q.dirs
	q.dirs = nil
	q.mu.Unlock()
	keys := make([]string, 0, len(dirs))
	for k := range dirs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var out []pruneResult
	failed := false
	for _, dir := range keys {
		d := dirs[dir]
		start := time.Now()
		r := pruneResult{Status: "ok", Dir: dir}
		left, why := a.deleteBranches(ctx, dir, d.branches, d.bases)
		r.Branches, r.Left = len(d.branches)-len(left), len(left)
		refs, rwhy := a.tidyRefs(ctx, dir, d.bases)
		r.Refs = refs
		r.Took = time.Since(start).Seconds()
		if why == "" && rwhy != "" {
			why = "the clone's remote-tracking refs: " + rwhy
		}
		if why != "" {
			r.Status, r.Reason, failed = "failed", why, true
			if !last {
				for i, base := range d.bases {
					if i > 0 {
						left = nil
					}
					q.add(dir, base, left)
				}
			}
		}
		out = append(out, r)
	}
	q.mu.Lock()
	q.retryAt = time.Time{}
	if failed {
		q.retryAt = a.now().Add(PruneRetry)
	}
	q.mu.Unlock()
	return out
}

// deleteBranches deletes branches from the clone's origin, pruneChunk to a push; left is
// those not deleted, why the first failure. A branch origin no longer holds is deleted.
func (a *app) deleteBranches(ctx context.Context, dir string, branches []pruneBranch, bases []string) (left []pruneBranch, why string) {
	for start := 0; start < len(branches); start += pruneChunk {
		chunk := branches[start:min(start+pruneChunk, len(branches))]
		listing := []string{"ls-remote", "--heads", "origin"}
		for _, b := range chunk {
			listing = append(listing, "refs/heads/"+b.Branch)
		}
		listed, err := a.pruneGit(ctx, dir, nil, listing...)
		if err != nil {
			return append(left, branches[start:]...), "the branch tips could not be read: " + firstLine("", err)
		}
		have := map[string]string{}
		for _, line := range strings.Split(listed, "\n") {
			if f := strings.Fields(line); len(f) == 2 {
				have[strings.TrimPrefix(f[1], "refs/heads/")] = f[0]
			}
		}
		args := []string{"push", "--porcelain", "--no-verify"}
		var deletes []string
		done := map[string]bool{}
		for _, b := range chunk {
			if slices.Contains(bases, b.Branch) {
				left = append(left, b)
				if why == "" {
					why = "the branch " + b.Branch + " is a stream base"
				}
				continue
			}
			if have[b.Branch] == "" {
				done[b.Branch] = true
				continue
			}
			args = append(args, "--force-with-lease=refs/heads/"+b.Branch+":"+b.Head)
			deletes = append(deletes, ":refs/heads/"+b.Branch)
		}
		args = append(args, "--", "origin")
		args = append(args, deletes...)
		out := ""
		if len(deletes) > 0 {
			out, err = a.pruneGit(ctx, dir, nil, args...)
		}
		var refused []string
		for _, line := range strings.Split(out, "\n") {
			f := strings.Split(line, "\t")
			if len(f) < 3 || !strings.HasPrefix(f[1], ":refs/heads/") {
				continue
			}
			b := strings.TrimPrefix(f[1], ":refs/heads/")
			switch {
			case f[0] == "-", strings.Contains(f[2], "does not exist"):
				done[b] = true
			default:
				refused = append(refused, b+" "+f[2])
			}
		}
		for _, b := range chunk {
			if !done[b.Branch] && !slices.Contains(left, b) {
				left = append(left, b)
			}
		}
		switch {
		case why != "":
		case len(refused) > 0:
			why = "origin refused the delete of " + strings.Join(refused, ", ")
		case err != nil && len(left) > 0:
			why = "the delete push failed: " + firstLine("", err)
		}
	}
	if len(left) > 0 && why == "" {
		why = fmt.Sprintf("the delete push named no outcome for %d branches", len(left))
	}
	return left, why
}

// tidyRefs removes the clone's remote-tracking refs of branches origin no longer holds.
// A clone holding none but its bases' (and origin/HEAD) makes no exchange with origin;
// otherwise one listing of origin's branch names (ls-remote: names, no objects) decides,
// and one local update-ref removes the rest. refs is how many were removed.
func (a *app) tidyRefs(ctx context.Context, dir string, bases []string) (refs int, why string) {
	const prefix = "refs/remotes/origin/"
	local, err := a.pruneGit(ctx, dir, nil, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return 0, firstLine("", err)
	}
	var others []string
	for _, ref := range strings.Fields(local) {
		// origin/HEAD, the clone's symbolic ref, names no branch of origin's
		if name := strings.TrimPrefix(ref, prefix); ref != prefix+"HEAD" && !slices.Contains(bases, name) {
			others = append(others, name)
		}
	}
	if len(others) == 0 {
		return 0, ""
	}
	listed, err := a.pruneGit(ctx, dir, nil, "ls-remote", "--heads", "origin")
	if err != nil {
		return 0, firstLine("", err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(listed, "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			have[strings.TrimPrefix(f[1], "refs/heads/")] = true
		}
	}
	var del bytes.Buffer
	for _, name := range others {
		if !have[name] {
			fmt.Fprintf(&del, "delete %s%s\n", prefix, name)
			refs++
		}
	}
	if refs == 0 {
		return 0, ""
	}
	if _, err := a.pruneGit(ctx, dir, &del, "update-ref", "--stdin"); err != nil {
		return 0, firstLine("", err)
	}
	return refs, ""
}

// pruneGit runs one git of the cleaner in the clone dir, in land's environment: its
// trimmed stdout (kept on a failure too: a push says what it did to each ref), and an
// error carrying git's words.
func (a *app) pruneGit(ctx context.Context, dir string, stdin io.Reader, args ...string) (string, error) {
	res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true, Stdin: stdin}, args...)
	out := strings.TrimSpace(string(res.Stdout))
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(res.Stderr)+"\n"+out))
	}
	return out, nil
}
