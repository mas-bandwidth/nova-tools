package main

// landledger.go is what land does past a plain merge of a card's recorded head
// (docs/SPEC-SPRINT.md section 7, the generated ledgers). A merge whose every unmerged
// path is a generated ledger (landLedgers: the class ledgers diffcheck.Ledger names,
// narrowed to a family owned by named tests) is resolved, not refused: the tip's side of
// each conflicted file is taken, the owning tests' update mode (NOVA_CI_UPDATE=1) runs on
// the merged tree until it changes nothing, and the merge is committed with a message
// naming the card and the ledgers. A shrink-only ledger is a function of the tree, so two
// cards that both delete rows and both move the ceiling line conflict line by line and
// regenerate to one answer. A conflict in any other file is refused as before.
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
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// landLedger is a family of generated, shrink-only files and the tests that own them: the
// update run regenerates them at the tree it runs in.
type landLedger struct {
	owns  func(p string) bool // the family's files
	roots []string            // the directories and files the family lives in: what the update rewrites
	tests string              // the owning tests, as the commit and the card's note name them
	run   []string            // the update run, in the clone, with NOVA_CI_UPDATE=1
}

// landLedgers are the generated ledgers land regenerates at a merge. The generality
// ledgers are one family: the text scan reads the Go scan's shards and lists both in its
// fixtures allowlist, so its update run (which drops the fixtures rows that went stale)
// regenerates them together.
var landLedgers = []landLedger{{
	owns:  diffcheck.GeneralityLedger,
	roots: diffcheck.GeneralityRoots,
	tests: "TestGeneralityGuardrail, TestGeneralityText",
	run:   []string{"go", "test", "-count=1", "-timeout", "600s", "-run", "^(TestGeneralityGuardrail|TestGeneralityText)$", "./internal/ci"},
}}

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

// ledgerNote is the card's note for a resolved merge, on its timeline: the ledgers and
// the tests that regenerated them.
func ledgerNote(paths []string, tests string) string {
	return "the generated ledgers " + sprint.Preview(paths, ", ") + " conflicted and were regenerated at the merge by " + tests + " (" + diffcheck.UpdateEnv + "=1)"
}

// ledgerMessage is the merge commit of a resolved merge: the landing's own subject, and
// a body naming the ledgers and how they were made.
func ledgerMessage(id, stream string, paths []string, tests string) []string {
	return []string{"land " + id + " (sprint stream " + stream + ")",
		"The generated ledgers " + strings.Join(paths, ", ") + " conflicted. The tip's side was taken and " + tests +
			" regenerated them at the merged tree (" + diffcheck.UpdateEnv + "=1)."}
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
	// before any update run: no link in the tree or on disk anywhere the update writes
	if p := cmp.Or(familyLink(files, owners), onDiskLink(dir, familyPaths(files, owners))); p != "" {
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

// regen runs one update run in the clone, in the caller's environment with the update
// variable set; its combined output.
func (l *lander) regen(ctx context.Context, dir string, run []string) (string, error) {
	b := subproc.Prepare(ctx, landRegenBudget, run[0], run[1:]...)
	defer b.Cancel()
	env := l.a.gitEnv
	if env == nil {
		env = os.Environ()
	}
	b.Cmd.Dir, b.Cmd.Env = dir, append(slices.Clone(env), diffcheck.UpdateEnv+"=1")
	out, err := b.Cmd.CombinedOutput()
	return string(out), b.Wrap(strings.Join(run, " "), err)
}
