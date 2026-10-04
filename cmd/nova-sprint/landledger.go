package main

// landledger.go is what land does past a plain merge of a card's recorded head
// (docs/SPEC-SPRINT.md section 7, the generated ledgers). A merge whose every unmerged
// path is a generated ledger (landLedgers: the class ledgers diffcheck.Ledger names,
// narrowed to a family owned by named tests) is resolved, not refused: the tip's side of
// each conflicted file is taken and the merge committed (deferLedgers), and once the
// batch's heads are merged the owning tests' update mode (NOVA_CI_UPDATE=1) runs on the
// batch's merged tree until it changes nothing, what it wrote amended into the batch's
// tip merge (settleLedgers, part of the gate of a prefix, landbatch.go): one
// regeneration a batch, not one a head. A shrink-only ledger is a function of the tree, so
// two cards that both delete rows and both move the ceiling line conflict line by line
// and regenerate to one answer, whichever tree it is regenerated at. A conflict in any
// other file is refused as before.
//
// After a conflict a resume puts the card back at its head, and the next land merges that
// head again: a merge of a shrink-only ledger is the same whoever makes it, so a
// resolution on the card's branch would land nothing a merge here does not, and one
// outside the ledgers is unread work, answered by rework or drop.
//
// The decisions (which ledgers own the paths, which changes an update made, when a
// regeneration is done, the words) are functions of their inputs, apart from the git and
// the update runs that feed them.

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// landLedger is a family of generated, shrink-only files and the tests that own them: the
// update run regenerates them at the tree it runs in.
type landLedger struct {
	owns  func(p string) bool // the family's files
	roots []string            // the directories and files the family lives in: what the update rewrites
	tests string              // the owning tests, as the commit and the card's note name them
	run   []string            // the update run, in the clone, with NOVA_CI_UPDATE=1
}

// landLedgers are the generated ledgers land regenerates at a batch's tip: a card's own
// change to one is taken off its merge (dropLedgers) and a conflict in them takes the
// tip's side (deferLedgers), and the family's update run makes them at the merged tree.
// The generality ledgers are one family: the text scan reads the Go scan's shards and
// lists both in its fixtures allowlist, so its update run (which drops the fixtures rows
// that went stale) regenerates them together. The lint ledgers are another: staticcheck's
// and errcheck's package shards, whose class tests run under -tags functional (a base
// without them has no such files, so no card changes one and the run is never made).
var landLedgers = []landLedger{{
	owns:  diffcheck.GeneralityLedger,
	roots: diffcheck.GeneralityRoots,
	tests: "TestGeneralityGuardrail, TestGeneralityText",
	run:   []string{"go", "test", "-count=1", "-timeout", "600s", "-run", "^(TestGeneralityGuardrail|TestGeneralityText)$", "./internal/ci"},
}, {
	owns:  diffcheck.LintLedger,
	roots: diffcheck.LintRoots,
	tests: "TestStaticcheckFindings, TestUncheckedErrors",
	run:   []string{"go", "test", "-tags", "functional", "-count=1", "-timeout", "600s", "-run", "^(TestStaticcheckFindings|TestUncheckedErrors)$", "./internal/ci"},
}}

// landRegenPasses bounds the update runs of one resolution: an update that writes
// fails once with "updated, rerun", and a ledger that reads another (the text scan
// reads the Go scan's shards) settles on the next pass.
const landRegenPasses = 4

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

// familyLink is a tracked symlink an update could write through, from git ls-files -s:
// one a ledger names, or one that is a family root, lies above one or lies under one;
// "" for none. An update rewrites its whole family in place, whichever paths conflicted,
// and a link would take a write outside the clone, where git status cannot see it.
func familyLink(lsFiles string, ledgers []landLedger) string {
	for _, line := range strings.Split(lsFiles, "\n") {
		meta, p, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(meta, "120000 ") {
			continue
		}
		for _, l := range ledgers {
			if l.owns(p) || slices.ContainsFunc(l.roots, func(r string) bool {
				return p == r || strings.HasPrefix(r, p+"/") || strings.HasPrefix(p, r+"/")
			}) {
				return p
			}
		}
	}
	return ""
}

// familyPaths is what an update of the ledgers writes, from git ls-files -s: each
// family's roots and every tracked file it owns; the disk check (onDiskLink) walks each
// and the directories on the way to it.
func familyPaths(lsFiles string, ledgers []landLedger) []string {
	var out []string
	for _, l := range ledgers {
		out = append(out, l.roots...)
	}
	for _, line := range strings.Split(lsFiles, "\n") {
		if _, p, ok := strings.Cut(line, "\t"); ok && owned(p, ledgers) {
			out = append(out, p)
		}
	}
	return out
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
	return err == nil, err != nil && strings.Contains(out, diffcheck.UpdatedRerun)
}

// testsOf is the owners' tests, as one list.
func testsOf(owners []landLedger) string {
	var t []string
	for _, o := range owners {
		t = append(t, o.tests)
	}
	return strings.Join(t, ", ")
}

// ledgerNote is the card's note for a landing that took the tip's side of generated
// ledgers, on its timeline: the ledgers, how the card met them (conflicted at its merge,
// or changed by its head and taken off it), and the tests that regenerated them, at the
// card's own merge (at "": the card's merge is the batch's tip) or at the batch's merge of
// at, the tip they were regenerated at.
func ledgerNote(paths []string, tests, at string, conflicted bool) string {
	where := "at the merge"
	if at != "" {
		where = "once for the batch, at its merge of " + at + ","
	}
	how := "conflicted and were regenerated "
	if !conflicted {
		how = "its head changed were taken off its merge and regenerated "
	}
	return "the generated ledgers " + sprint.Preview(paths, ", ") + " " + how + where + " by " + tests + " (" + diffcheck.UpdateEnv + "=1)"
}

// ledgerMessage is the merge commit of a merge stopped only on generated ledgers: the
// landing's own subject, and a body naming the ledgers and how they are made (the tip's
// side taken here, the regeneration at the batch's tip, settleLedgers).
func ledgerMessage(id, stream string, paths []string, tests string) []string {
	return []string{"land " + id + " (sprint stream " + stream + ")",
		"The generated ledgers " + strings.Join(paths, ", ") + " conflicted. The tip's side was taken, and " + tests +
			" regenerate them at the batch's merged tree (" + diffcheck.UpdateEnv + "=1), once for the batch, in its last merge."}
}

// dropMessage is the paragraph a merge gains when its head's own changes to generated
// ledgers are taken off it (dropLedgers).
func dropMessage(paths []string, tests string) string {
	return "The head's changes to the generated ledgers " + strings.Join(paths, ", ") + " were taken off this merge: " + tests +
		" regenerate them at the batch's merged tree (" + diffcheck.UpdateEnv + "=1), once for the batch, in its last merge."
}

// regenMessage is the paragraph the batch's tip merge gains when the regeneration
// changed the ledgers: which merges took the tip's side, and what regenerated them.
func regenMessage(ids, paths []string, tests string) string {
	return "The generated ledgers " + strings.Join(paths, ", ") + " were taken from the tip at the merge of " + strings.Join(ids, ", ") +
		" and " + tests + " regenerated them at this merged tree (" + diffcheck.UpdateEnv + "=1), once for the batch."
}

// ledgerDefer is a merge that took the tip's side of generated ledgers, whose
// regeneration waits for the batch's gate (landbatch.go): a merge stopped only on them
// (deferLedgers), or one whose head changed them (dropLedgers), or both; the owners'
// update runs regenerate them once, at the tip of the prefix gated (settleLedgers).
type ledgerDefer struct {
	paths      []string     // the generated ledgers taken from the tip
	owners     []landLedger // the families that own them
	conflicted bool         // the merge stopped on them (else the head changed them and merged)
	merge      string       // git's words on a merge that stopped: the refusal's, should the regeneration fail
	kind       string       // the conflict as the merge left it (conflictCard)
	all        []string     // every unmerged path of a merge that stopped, shrink-only ledgers included
}

// deferLedgers takes the tip's side of a merge stopped on unmerged paths that are all
// generated ledgers (paths, with ours the ones the tip holds) and commits the merge; the
// owners' update runs come later, once for the batch (settleLedgers). env is a git failure
// that is not the card's, the merge ended and the clone reset.
func (l *lander) deferLedgers(ctx context.Context, dir, stream string, c landCard, paths []string, ours map[string]bool, owners []landLedger) (env string) {
	if env = l.takeTips(ctx, dir, stream, c, paths, ours, owners); env != "" {
		env = l.restore(ctx, dir, nil, env)
	}
	return env
}

// takeTips is deferLedgers before the reset.
func (l *lander) takeTips(ctx context.Context, dir, stream string, c landCard, paths []string, ours map[string]bool, owners []landLedger) (env string) {
	for _, p := range paths {
		args := []string{"rm", "-q", "--", p} // the tip deleted it: it stays deleted
		if ours[p] {
			args = []string{"checkout", "--ours", "--", p}
		}
		if _, err := l.git(ctx, dir, args...); err != nil {
			return "the tip's side of " + p + " could not be taken: " + firstLine("", err)
		}
	}
	if _, err := l.git(ctx, dir, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return "the ledgers could not be staged: " + firstLine("", err)
	}
	msg := ledgerMessage(c.id, stream, paths, testsOf(owners))
	if _, err := l.git(ctx, dir, "commit", "-q", "-m", msg[0], "-m", msg[1]); err != nil {
		return "the merge of " + c.id + " could not be committed: " + firstLine("", err)
	}
	return ""
}

// dropLedgers takes off the merge of c, made on before, every change its head made to a
// generated ledger (a file a family of l.ledgers() owns): each such file put back as the
// tip holds it (deleted where the tip has none), the merge amended, and the ledgers left
// to the batch's regeneration (d, added to the merge's own deferral, if any). A child that
// committed its ledger is no stop: the lander makes the ledger at the merged tree itself
// (the coordinator, 2026-10-04: five lint cards stopped the lint stream, thirty queued
// behind each stop, for errcheck and staticcheck shards outside their PATHS). env is a
// git failure that is not the card's.
func (l *lander) dropLedgers(ctx context.Context, dir string, c landCard, before string, d *ledgerDefer) (*ledgerDefer, string) {
	after, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || after == before {
		return d, ""
	}
	changed, err := l.git(ctx, dir, "diff", "--name-only", "--no-renames", before, after)
	if err != nil {
		return d, "the files the merge of " + c.id + " changed could not be listed: " + firstLine("", err)
	}
	var paths []string
	for _, p := range strings.Split(changed, "\n") {
		if p != "" && owned(p, l.ledgers()) {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return d, ""
	}
	held, err := l.git(ctx, dir, append([]string{"ls-tree", "-r", "--name-only", before, "--"}, paths...)...)
	if err != nil {
		return d, "the tip's ledgers could not be listed: " + firstLine("", err)
	}
	tip := strings.Split(held, "\n")
	var keep, gone []string
	for _, p := range paths {
		if slices.Contains(tip, p) {
			keep = append(keep, p)
		} else {
			gone = append(gone, p)
		}
	}
	if len(keep) > 0 {
		if _, err := l.git(ctx, dir, append([]string{"checkout", before, "--"}, keep...)...); err != nil {
			return d, "the tip's side of the ledgers " + c.id + " changed could not be taken: " + firstLine("", err)
		}
	}
	if len(gone) > 0 {
		if _, err := l.git(ctx, dir, append([]string{"rm", "-q", "-f", "--"}, gone...)...); err != nil {
			return d, "the ledgers " + c.id + " added could not be taken off its merge: " + firstLine("", err)
		}
	}
	owners, _ := ledgerOwners(paths, l.ledgers())
	msg, err := l.git(ctx, dir, "log", "-1", "--format=%B", "HEAD")
	if err != nil {
		return d, "the merge of " + c.id + " could not be read: " + firstLine("", err)
	}
	if _, err := l.git(ctx, dir, "commit", "-q", "--amend", "--allow-empty", "-m", msg, "-m", dropMessage(paths, testsOf(owners))); err != nil {
		return d, "the merge of " + c.id + " could not be amended without its ledgers: " + firstLine("", err)
	}
	if d == nil {
		d = &ledgerDefer{}
	}
	for _, p := range paths {
		if !slices.Contains(d.paths, p) {
			d.paths = append(d.paths, p)
		}
	}
	for _, o := range owners {
		if !slices.ContainsFunc(d.owners, func(x landLedger) bool { return x.tests == o.tests }) {
			d.owners = append(d.owners, o)
		}
	}
	return d, ""
}

// settleLedgers regenerates, at the clone's tip, the generated ledgers the merges of the
// cards of a prefix of the batch took the tip's side of (defers, one per card, nil for a
// card whose merge needed none): the owners' update runs to a fixed point, and what they
// wrote amended into the tip's merge. card is why the prefix is red in its ledgers, env a
// git failure that is no card's; either way the clone is put back at its tip (restore).
func (l *lander) settleLedgers(ctx context.Context, dir string, cards []landCard, defers []*ledgerDefer) (card, env string) {
	var ids, paths []string
	used := map[int]bool{}
	ledgers := l.ledgers()
	for i, d := range defers {
		if d == nil {
			continue
		}
		ids = append(ids, cards[i].id)
		for _, p := range d.paths {
			if !slices.Contains(paths, p) {
				paths = append(paths, p)
			}
		}
		for _, o := range d.owners {
			used[slices.IndexFunc(ledgers, func(x landLedger) bool { return x.tests == o.tests })] = true
		}
	}
	if len(ids) == 0 {
		return "", ""
	}
	var owners []landLedger
	for i, o := range ledgers {
		if used[i] {
			owners = append(owners, o)
		}
	}
	before, err := l.git(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", "the clone's untracked files could not be listed: " + firstLine("", err)
	}
	card, env = l.regenerate(ctx, dir, ids, paths, owners)
	if card != "" || env != "" {
		env = l.restore(ctx, dir, strings.Split(before, "\x00"), env)
	}
	return card, env
}

// regenerate is settleLedgers before the restore: every path an update writes held to be
// no symlink, then the owners' update runs until one writes nothing (at most
// landRegenPasses), every file they wrote held to be a ledger of theirs, and the tip's
// merge amended with what they wrote and regenMessage.
func (l *lander) regenerate(ctx context.Context, dir string, ids, paths []string, owners []landLedger) (card, env string) {
	tests := testsOf(owners)
	files, err := l.git(ctx, dir, "ls-files", "-s")
	if err != nil {
		return "", "the clone's files could not be listed: " + firstLine("", err)
	}
	// before any update run: no link in the tree or on disk anywhere the update writes
	if p := cmp.Or(familyLink(files, owners), onDiskLink(dir, familyPaths(files, owners))); p != "" {
		return "its generated ledgers are regenerated here and " + p + " is a symlink, which an update would write through", ""
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
				return "its generated ledgers were taken from the tip and " + o.tests + " did not regenerate them: " + oneline.Err(err) + checkTail(out), ""
			}
		}
		if !again {
			break
		}
		if runs >= landRegenPasses {
			return "its generated ledgers were taken from the tip and " + tests + " still rewrote them after " + strconv.Itoa(runs) + " update runs", ""
		}
	}
	status, err := l.git(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "", "what the update runs changed could not be listed: " + firstLine("", err)
	}
	written := updateWrote(status)
	for _, p := range written {
		if !owned(p, owners) {
			return "its generated ledgers were taken from the tip and the update run changed " + p + ", which is no ledger of " + tests, ""
		}
	}
	if len(written) == 0 {
		return "", "" // the tip's sides were the merged tree's already
	}
	if _, err := l.git(ctx, dir, append([]string{"add", "-A", "--"}, written...)...); err != nil {
		return "", "the regenerated ledgers could not be staged: " + firstLine("", err)
	}
	msg, err := l.git(ctx, dir, "log", "-1", "--format=%B", "HEAD")
	if err != nil {
		return "", "the batch's tip merge could not be read: " + firstLine("", err)
	}
	if _, err := l.git(ctx, dir, "commit", "-q", "--amend", "-m", msg, "-m", regenMessage(ids, paths, tests)); err != nil {
		return "", "the regenerated ledgers could not be committed to the batch's tip: " + firstLine("", err)
	}
	return "", ""
}

// restore puts the clone back at its tip after a failed resolution, ending any merge in
// progress, and takes back what the update runs wrote: the tracked files reset, the
// untracked files that were not there before (known) removed; env is the failure already
// met, else the restore's own.
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

// onDiskLink is a path, or a directory on the way to it, that is a symlink on disk
// (os.Lstat), tracked or not; "" for none.
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

// regen runs one update run in the clone (goRun) with the update variable set; its
// combined output.
func (l *lander) regen(ctx context.Context, dir string, run []string) (string, error) {
	return l.goRun(ctx, dir, run, diffcheck.UpdateEnv+"=1")
}
