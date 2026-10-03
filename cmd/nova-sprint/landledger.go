package main

// landledger.go is what land does past a plain merge of a card's recorded head
// (docs/SPEC-SPRINT.md section 7, the generated ledgers and the resumed tip):
//
//   - a merge whose every unmerged path is a generated ledger (landLedgers: the class
//     ledgers diffcheck.Ledger names, narrowed to a family owned by named tests) is
//     resolved, not refused: the tip's side of each conflicted file is taken, the owning
//     tests' update mode (NOVA_CI_UPDATE=1) runs on the merged tree until it changes
//     nothing, and the merge is committed with a message naming the card and the
//     ledgers. A shrink-only ledger is a function of the tree, so two cards that both
//     delete rows and both move the ceiling line conflict line by line and regenerate to
//     one answer. A conflict in any other file is refused as before.
//   - a card a resume put back after a conflict (sprint.FieldResumedHead) lands its
//     branch's tip in place of its recorded head only when the tip is exactly what land
//     itself makes of that head: the base merged into it, the ledgers regenerated. No
//     change a reader has not read lands. The tip is pinned on the card the first time it
//     is verified (sprint.FieldResumedTip), so a land run again merges that commit and no
//     later one.
//
// The decisions (which ledgers own the paths, which changes an update made, when a
// regeneration is done, the words) are functions of their inputs, apart from the git and
// the update runs that feed them.

import (
	"cmp"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// landLedger is a family of generated, shrink-only files and the tests that own them: the
// update run regenerates them at the tree it runs in.
type landLedger struct {
	owns  func(p string) bool // the family's files
	tests string              // the owning tests, as the commit and the card's note name them
	run   []string            // the update run, in the clone, with NOVA_CI_UPDATE=1
}

// landLedgers are the generated ledgers land regenerates at a merge. The generality
// ledgers are one family: the text scan reads the Go scan's shards and lists both in its
// fixtures allowlist, so its update run (which drops the fixtures rows that went stale)
// regenerates them together.
var landLedgers = []landLedger{{
	owns:  generalityLedger,
	tests: "TestGeneralityGuardrail, TestGeneralityText",
	run:   []string{"go", "test", "-count=1", "-timeout", "600s", "-run", "^(TestGeneralityGuardrail|TestGeneralityText)$", "./internal/ci"},
}}

// generalityLedger says p is a generality ledger: a class ledger as the lander's checks
// define one (diffcheck.Ledger: a .txt shard of a counted-ledger directory or a list file,
// never a directory, a Go file or a class test's fixture), in the generality family (the
// shards under generality/ and generality-text/, and the text scan's fixtures allowlist).
func generalityLedger(p string) bool {
	rest, _ := strings.CutPrefix(p, diffcheck.LedgerDir)
	dir, _, nested := strings.Cut(rest, "/")
	return diffcheck.Ledger(p) && (nested && (dir == "generality" || dir == "generality-text") || rest == "generality_text_fixtures_allowlist.txt")
}

// landRegenPasses bounds the update runs of one resolution: an update that writes
// fails once with "updated, rerun", and a ledger that reads another (the text scan
// reads the Go scan's shards) settles on the next pass.
const landRegenPasses = 4

// landRegenBudget bounds one update run: a build and two tests of one package.
const landRegenBudget = 15 * time.Minute

// owned says one of the ledgers owns p.
func owned(p string, ledgers []landLedger) bool {
	return slices.ContainsFunc(ledgers, func(l landLedger) bool { return l.owns(p) })
}

// ledgerOwners is the ledgers that own the paths, in the list's order, and the paths
// none owns: a resolution needs every path owned.
func ledgerOwners(paths []string, ledgers []landLedger) (owners []landLedger, outside []string) {
	used := make([]bool, len(ledgers))
	for _, p := range paths {
		i := slices.IndexFunc(ledgers, func(l landLedger) bool { return l.owns(p) })
		if i < 0 {
			outside = append(outside, p)
			continue
		}
		used[i] = true
	}
	for i, l := range ledgers {
		if used[i] {
			owners = append(owners, l)
		}
	}
	return owners, outside
}

// unmergedPaths is git ls-files --unmerged as the paths it names, in order and each
// once, and the ones with the tip's side (stage 2, ours) present.
func unmergedPaths(out string) (paths []string, ours map[string]bool) {
	ours = map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		meta, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
		if f := strings.Fields(meta); len(f) == 3 && f[2] == "2" {
			ours[p] = true
		}
	}
	return paths, ours
}

// linkedLedger is a symlink an update could write through, from git ls-files -s: a
// tracked link that a ledger names, or a directory a conflicted path lies under; "" for
// none. An update writes the ledgers in place, and a link would take that write outside
// the clone, unseen by the check of what it changed.
func linkedLedger(lsFiles string, paths []string, ledgers []landLedger) string {
	for _, line := range strings.Split(lsFiles, "\n") {
		meta, p, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(meta, "120000 ") {
			continue
		}
		if owned(p, ledgers) || slices.ContainsFunc(paths, func(q string) bool { return strings.HasPrefix(q, p+"/") }) {
			return p
		}
	}
	return ""
}

// updateWrote is what an update run changed, from git status --porcelain=v1 -z
// --untracked-files=all during the merge: the paths whose worktree differs from the
// index, and the untracked ones (a new file is a change too); the merge's own staged
// changes are not the run's.
func updateWrote(status string) []string {
	var out []string
	entries := strings.Split(status, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		xy, p := e[:2], e[3:]
		if xy[0] == 'R' || xy[0] == 'C' {
			i++ // the entry after a rename or a copy is the path it came from
		}
		if xy == "??" || xy[1] != ' ' {
			out = append(out, p)
		}
	}
	return out
}

// regenDone reads one update run: done when it passed (nothing left to write), again
// when it wrote and asks for a rerun; neither is a failure, with its words.
func regenDone(err error, out string) (done, again bool) {
	return err == nil, err != nil && strings.Contains(out, allowlist.UpdatedRerun)
}

// testsOf is the owners' tests, as one list.
func testsOf(owners []landLedger) string {
	var t []string
	for _, o := range owners {
		t = append(t, o.tests)
	}
	return strings.Join(t, ", ")
}

// ledgerNote is the card's note for a resolved merge, on its timeline: the ledgers and
// the tests that regenerated them.
func ledgerNote(paths []string, tests string) string {
	return "the generated ledgers " + sprint.Preview(paths, ", ") + " conflicted and were regenerated at the merge by " + tests + " (" + allowlist.UpdateEnv + "=1)"
}

// ledgerMessage is the merge commit of a resolved merge: the landing's own subject, and
// a body naming the ledgers and how they were made.
func ledgerMessage(id, stream string, paths []string, tests string) []string {
	return []string{"land " + id + " (sprint stream " + stream + ")",
		"The generated ledgers " + strings.Join(paths, ", ") + " conflicted. The tip's side was taken and " + tests +
			" regenerated them at the merged tree (" + allowlist.UpdateEnv + "=1)."}
}

// tipNote is the card's note when it landed its branch tip, on its timeline.
func tipNote(c landCard, tip string) string {
	return "resumed after a conflict: landed the tip " + tip + " of the branch " + c.branch + ", the base merged into the head " + c.head + " it stopped on and nothing else"
}

// tipNotDescending is the one line a resumed tip that does not descend from the head is
// refused with.
func tipNotDescending(c landCard, tip string) string {
	return "the tip " + tip + " of the branch " + c.branch + " of " + c.id + " does not descend from its recorded head " + c.head +
		"; merge the base into the branch and push it (never a rebase), or rework the card; run: nova-sprint card " + c.id
}

// tipBeyond is the one line a resumed tip that is more than the base merged into the head
// (and the ledgers regenerated) is refused with: what is more is unread.
func tipBeyond(c landCard, tip string) string {
	return "the tip " + tip + " of the branch " + c.branch + " of " + c.id + " carries changes beyond the base merge and the ledgers of its head " + c.head +
		"; rework it so a reader sees them: nova-sprint return " + c.id + " --reason 'resolved by hand', then nova-sprint rework " + c.id + " --fix '<the resolution>'"
}

// resolveLedgers resolves a merge stopped on unmerged paths that are all generated
// ledgers (paths, with ours the ones the tip holds): the tip's side of each taken, the
// owners' update run to a fixed point, the merge committed. note is the card's note
// when it is resolved; card is why it is not, env a git failure that is not the card's;
// either way the merge is ended and the clone restored (restore).
func (l *lander) resolveLedgers(ctx context.Context, dir, stream string, c landCard, paths []string, ours map[string]bool, owners []landLedger) (note, card, env string) {
	before, err := l.git(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", "", l.restore(ctx, dir, nil, "the clone's untracked files could not be listed: "+firstLine("", err))
	}
	known := strings.Split(before, "\x00")
	note, card, env = l.resolve(ctx, dir, stream, c, paths, ours, owners)
	if note == "" {
		env = l.restore(ctx, dir, known, env)
	}
	return note, card, env
}

// restore ends a merge whose resolution failed and takes back what the update runs
// wrote: the tracked files reset to the batch branch, the untracked files that were not
// there before (known) removed; env is the failure already met, else the restore's own.
func (l *lander) restore(ctx context.Context, dir string, known []string, env string) string {
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard"); err != nil {
		return cmp.Or(env, "the clone could not be reset after a failed resolution: "+firstLine("", err))
	}
	if known == nil {
		return env
	}
	after, err := l.git(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return cmp.Or(env, "the clone's untracked files could not be listed: "+firstLine("", err))
	}
	var stray []string
	for _, p := range strings.Split(after, "\x00") {
		if p != "" && !slices.Contains(known, p) {
			stray = append(stray, p)
		}
	}
	if len(stray) > 0 {
		if _, err := l.git(ctx, dir, append([]string{"clean", "-q", "-f", "--"}, stray...)...); err != nil {
			return cmp.Or(env, "the files a failed update wrote could not be removed: "+firstLine("", err))
		}
	}
	return env
}

// resolve is resolveLedgers before the restore.
func (l *lander) resolve(ctx context.Context, dir, stream string, c landCard, paths []string, ours map[string]bool, owners []landLedger) (note, card, env string) {
	tests := testsOf(owners)
	for _, p := range paths {
		args := []string{"rm", "-q", "--", p} // the tip deleted it: it stays deleted
		if ours[p] {
			args = []string{"checkout", "--ours", "--", p}
		}
		if _, err := l.git(ctx, dir, args...); err != nil {
			return "", "", "the tip's side of " + p + " could not be taken: " + firstLine("", err)
		}
	}
	files, err := l.git(ctx, dir, "ls-files", "-s")
	if err != nil {
		return "", "", "the clone's files could not be listed: " + firstLine("", err)
	}
	if p := cmp.Or(linkedLedger(files, paths, owners), onDiskLink(dir, paths)); p != "" {
		return "", "its generated ledgers conflict and " + p + " is a symlink, which an update would write through", ""
	}
	if _, err := l.git(ctx, dir, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return "", "", "the ledgers could not be staged: " + firstLine("", err)
	}
	for runs := 0; ; {
		again := false
		for _, o := range owners {
			out, err := l.regen(ctx, dir, o.run)
			runs++
			done, more := regenDone(err, out)
			switch {
			case more:
				again = true
			case !done:
				return "", "its generated ledgers conflict and " + o.tests + " did not regenerate them: " + oneline.Err(err) + checkTail(out), ""
			}
		}
		if !again {
			break
		}
		if runs >= landRegenPasses {
			return "", "its generated ledgers conflict and " + tests + " still rewrote them after " + strconv.Itoa(runs) + " update runs", ""
		}
	}
	status, err := l.git(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "", "", "what the update runs changed could not be listed: " + firstLine("", err)
	}
	written := updateWrote(status)
	for _, p := range written {
		if !owned(p, owners) {
			return "", "its generated ledgers conflict and the update run changed " + p + ", which is no ledger of " + tests, ""
		}
	}
	if len(written) > 0 {
		if _, err := l.git(ctx, dir, append([]string{"add", "-A", "--"}, written...)...); err != nil {
			return "", "", "the regenerated ledgers could not be staged: " + firstLine("", err)
		}
	}
	msg := ledgerMessage(c.id, stream, paths, tests)
	if _, err := l.git(ctx, dir, "commit", "-q", "-m", msg[0], "-m", msg[1]); err != nil {
		return "", "", "the resolved merge of " + c.id + " could not be committed: " + firstLine("", err)
	}
	return ledgerNote(paths, tests), "", ""
}

// onDiskLink is a conflicted path, or a directory it lies under, that is a symlink on
// disk (os.Lstat); "" for none.
func onDiskLink(dir string, paths []string) string {
	for _, p := range paths {
		for q := p; q != "." && q != "/" && q != ""; q = filepath.Dir(q) {
			if fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(q))); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				return q
			}
		}
	}
	return ""
}

// regen runs one update run in the clone, in the caller's environment with the update
// variable set; its combined output.
func (l *lander) regen(ctx context.Context, dir string, run []string) (string, error) {
	b := subproc.Prepare(ctx, landRegenBudget, run[0], run[1:]...)
	defer b.Cancel()
	env := l.a.gitEnv
	if env == nil {
		env = os.Environ()
	}
	b.Cmd.Dir, b.Cmd.Env = dir, append(slices.Clone(env), allowlist.UpdateEnv+"=1")
	out, err := b.Cmd.CombinedOutput()
	return string(out), b.Wrap(strings.Join(run, " "), err)
}

// resumeTip is the commit a card a resume put back after a conflict lands in place of
// its recorded head, "" (the head lands) when its branch is gone from origin or still at
// the head: the tip pinned on the card, else its branch's tip on origin when that
// descends from the head and its tree is exactly the base merged into the head with the
// ledgers regenerated (baseMerge), then pinned. card is why the card is refused; env a
// failure that is not the card's.
func (l *lander) resumeTip(ctx context.Context, dir, stream string, c landCard) (tip, card, env string) {
	if c.pinned != "" {
		return c.pinned, "", ""
	}
	if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin", "refs/heads/"+c.branch); err != nil {
		if containsAny(err.Error(), notOnOrigin) {
			return "", "", ""
		}
		return "", "", "the fetch of the branch " + c.branch + " of " + c.id + " failed: " + firstLine("", err)
	}
	tip, err := l.git(ctx, dir, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return "", "", "the tip of the branch " + c.branch + " of " + c.id + " could not be read: " + firstLine("", err)
	}
	if strings.HasPrefix(tip, c.head) {
		return "", "", ""
	}
	// every ancestor of the tip came with it: a head the clone lacks is none of them
	if _, missing := l.git(ctx, dir, "cat-file", "-e", c.head+"^{commit}"); missing != nil {
		return "", tipNotDescending(c, tip), ""
	}
	if ok, why := l.ancestor(ctx, dir, c.head, tip); why != "" || !ok {
		return "", tipNotDescending(c, tip), why
	}
	same, card, env := l.baseMerge(ctx, dir, stream, c, tip)
	switch {
	case env != "" || card != "":
		return "", card, env
	case !same:
		return "", tipBeyond(c, tip), ""
	}
	if why := l.pinTip(ctx, stream, c, tip); why != "" {
		return "", "", "the tip " + tip + " of " + c.id + " could not be pinned (" + why + "); run land again"
	}
	return tip, "", ""
}

// ancestor says a is an ancestor of b; why is git's failure, not an answer.
func (l *lander) ancestor(ctx context.Context, dir, a, b string) (bool, string) {
	_, err := l.git(ctx, dir, "merge-base", "--is-ancestor", a, b)
	if x := (*exec.ExitError)(nil); err != nil && errors.As(err, &x) && x.ExitCode() == 1 {
		return false, ""
	}
	if err != nil {
		return false, "git could not say whether " + b + " descends from " + a + ": " + firstLine("", err)
	}
	return true, ""
}

// baseMerge says the tip's tree is exactly what land makes of the card's head and the
// base commit the tip merged (the tip's parent on the base that the head does not reach):
// that base merged into the head in a scratch worktree, the ledgers regenerated where
// they conflict. A tip with no such parent, or whose base merge conflicts outside the
// ledgers, is more than a base merge, and card says so.
func (l *lander) baseMerge(ctx context.Context, dir, stream string, c landCard, tip string) (same bool, card, env string) {
	parents, err := l.git(ctx, dir, "rev-list", "--parents", "-n", "1", tip)
	if err != nil {
		return false, "", "the parents of the tip " + tip + " could not be read: " + firstLine("", err)
	}
	base := ""
	for _, p := range strings.Fields(parents)[1:] {
		onBase, why := l.ancestor(ctx, dir, p, "refs/remotes/origin/"+c.base)
		fromHead, why2 := l.ancestor(ctx, dir, c.head, p)
		if why+why2 != "" {
			return false, "", why + why2
		}
		if onBase && !fromHead {
			base = p
			break
		}
	}
	if base == "" {
		return false, tipBeyond(c, tip), ""
	}
	gitDir, err := l.git(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return false, "", "the clone's git directory could not be read: " + firstLine("", err)
	}
	// the scratch worktree lives in the clone's own git directory, git's to make and remove
	wt := filepath.Join(gitDir, "nova-land-resume")
	// ignored: this removes a worktree a crash left, if any; the add below says if one stands
	_, _ = l.git(ctx, dir, "worktree", "remove", "--force", wt)
	if _, err := l.git(ctx, dir, "worktree", "add", "-q", "--detach", wt, c.head); err != nil {
		return false, "", "a scratch worktree for the base merge of " + c.id + " could not be made: " + firstLine("", err)
	}
	defer func() {
		// ignored: a worktree left here is removed by the next base merge before its add
		_, _ = l.git(ctx, dir, "worktree", "remove", "--force", wt)
	}()
	if _, err := l.git(ctx, wt, "merge", "--no-ff", "--no-edit", "-m", "base merge of "+c.id, base); err != nil {
		unmerged, uerr := l.git(ctx, wt, "ls-files", "--unmerged")
		paths, ours := unmergedPaths(unmerged)
		owners, outside := ledgerOwners(paths, l.ledgers())
		if uerr != nil || unmerged == "" || len(outside) > 0 {
			return false, tipBeyond(c, tip), ""
		}
		if note, card, env := l.resolveLedgers(ctx, wt, stream, c, paths, ours, owners); note == "" {
			return false, card, env
		}
	}
	_, err = l.git(ctx, wt, "diff", "--quiet", "HEAD", tip)
	if x := (*exec.ExitError)(nil); err != nil && errors.As(err, &x) && x.ExitCode() == 1 {
		return false, "", ""
	}
	if err != nil {
		return false, "", "the tip " + tip + " could not be compared with the base merge of " + c.id + ": " + firstLine("", err)
	}
	return true, "", ""
}

// pinTip records the verified tip on the card's merge card (sprint.PinTip), fenced to the
// epoch land read; why is why it was not recorded.
func (l *lander) pinTip(ctx context.Context, stream string, c landCard, tip string) string {
	r := sprint.PinTipReq{Stream: stream, Card: c.id, Head: c.head, Tip: tip, Who: l.c.actor}
	step := store.Step{Args: store.ArgsOf(r), Verb: "land", Load: []string{sprint.Merge, sprint.Work},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.PinTip(s, r) }}
	epoch := l.epoch
	step.Epoch = &epoch
	l.a.serial.Lock()
	defer l.a.serial.Unlock()
	res, err := l.st.Run(ctx, step)
	if stepExit(res, err) != 0 {
		return stepWhy(res, err)
	}
	return ""
}
