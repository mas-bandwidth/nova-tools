package main

// landledger.go is what land does past a plain merge of a card's recorded head
// (docs/SPEC-SPRINT.md section 7, the generated ledgers and the resumed tip):
//
//   - a merge whose every unmerged path is a generated ledger (landLedgers: a fixed
//     list of path globs, each owned by named tests) is resolved, not refused: the
//     tip's side of each conflicted file is taken, the owning tests' update mode
//     (NOVA_CI_UPDATE=1) runs on the merged tree until it changes nothing, and the merge
//     is committed with a message naming the card and the ledgers. A shrink-only ledger
//     is a function of the tree, so two cards that both delete rows and both move the
//     ceiling line conflict line by line and regenerate to one answer. A conflict in any
//     other file is refused as before.
//   - a card a resume put back after a conflict (sprint.FieldResumedHead) lands its
//     branch's tip in place of its recorded head when the tip descends from that head:
//     the coordinator merged the base into the branch and pushed it. A tip that does not
//     descend is refused, naming both commits.
//
// The decisions (which ledgers own the paths, when a regeneration is done, the words)
// are functions of their inputs, apart from the git and the update runs that feed them.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// landLedger is a set of generated, shrink-only files and the tests that own them: the
// update run regenerates them at the tree it runs in.
type landLedger struct {
	globs []string // the files, as PATHS globs (hygiene.MatchGlob)
	tests string   // the owning tests, as the commit and the card's note name them
	run   []string // the update run, in the clone, with NOVA_CI_UPDATE=1
}

// landLedgers are the generated ledgers land regenerates at a merge. The generality
// ledgers are one family: the text scan reads the Go scan's shards and lists both in its
// fixtures allowlist, so its update run (which drops the fixtures rows that went stale)
// regenerates them together.
var landLedgers = []landLedger{{
	globs: []string{
		"internal/ci/testdata/generality-text/**",
		"internal/ci/testdata/generality/**",
		"internal/ci/testdata/generality_text_fixtures_allowlist.txt",
	},
	tests: "TestGeneralityGuardrail, TestGeneralityText",
	run:   []string{"go", "test", "-count=1", "-timeout", "600s", "-run", "^(TestGeneralityGuardrail|TestGeneralityText)$", "./internal/ci"},
}}

// landRegenPasses bounds the update runs of one resolution: an update that writes
// fails once with "updated, rerun", and a ledger that reads another (the text scan
// reads the Go scan's shards) settles on the next pass.
const landRegenPasses = 4

// landRegenBudget bounds one update run: a build and two tests of one package.
const landRegenBudget = 15 * time.Minute

// ledgerOwners is the ledgers that own the paths, in the list's order, and the paths
// none owns: a resolution needs every path owned.
func ledgerOwners(paths []string, ledgers []landLedger) (owners []landLedger, outside []string) {
	used := make([]bool, len(ledgers))
	for _, p := range paths {
		i := slices.IndexFunc(ledgers, func(l landLedger) bool {
			return slices.ContainsFunc(l.globs, func(g string) bool { return hygiene.MatchGlob(g, p) })
		})
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
	return "resumed after a conflict: landed the branch " + c.branch + " at " + tip + ", which descends from the head " + c.head + " it stopped on"
}

// tipRefused is the one line a resumed tip that does not descend is refused with.
func tipRefused(c landCard, tip string) string {
	return "the branch " + c.branch + " of " + c.id + " is at " + tip + ", which does not descend from its recorded head " + c.head +
		"; merge the base into the branch and push it (never a rebase), or rework the card; run: nova-sprint card " + c.id
}

// resolveLedgers resolves a merge stopped on unmerged paths that are all generated
// ledgers (paths, with ours the ones the tip holds): the tip's side of each taken, the
// owners' update run to a fixed point, the merge committed. note is the card's note
// when it is resolved; card is why it is not, the merge still in progress for the
// caller to abort; env is a git failure that is not the card's.
func (l *lander) resolveLedgers(ctx context.Context, dir, stream string, c landCard, paths []string, ours map[string]bool, owners []landLedger) (note, card, env string) {
	for _, p := range paths {
		args := []string{"rm", "-q", "--", p} // the tip deleted it: it stays deleted
		if ours[p] {
			args = []string{"checkout", "--ours", "--", p}
		}
		if _, err := l.git(ctx, dir, args...); err != nil {
			return "", "", "the tip's side of " + p + " could not be taken: " + firstLine("", err)
		}
	}
	if _, err := l.git(ctx, dir, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return "", "", "the ledgers could not be staged: " + firstLine("", err)
	}
	tests := testsOf(owners)
	for pass := 1; ; pass++ {
		again := false
		for _, o := range owners {
			out, err := l.regen(ctx, dir, o.run)
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
		if pass == landRegenPasses {
			return "", "its generated ledgers conflict and " + tests + " still rewrote them after " + strconv.Itoa(landRegenPasses) + " update runs", ""
		}
	}
	changed, err := l.git(ctx, dir, "diff", "--name-only")
	if err != nil {
		return "", "", "the regenerated ledgers could not be listed: " + firstLine("", err)
	}
	var written []string
	for _, p := range strings.Fields(changed) {
		if _, out := ledgerOwners([]string{p}, owners); len(out) > 0 {
			return "", "its generated ledgers conflict and the update run changed " + p + ", which is no ledger of " + tests, ""
		}
		written = append(written, p)
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
// its recorded head: its branch's tip on origin when that descends from the head, ""
// (the head lands) when the branch is gone or still at the head. card is why the card
// is refused (a tip that does not descend); env a git failure that is not the card's.
func (l *lander) resumeTip(ctx context.Context, dir string, c landCard) (tip, card, env string) {
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
		return "", tipRefused(c, tip), ""
	}
	if _, err := l.git(ctx, dir, "merge-base", "--is-ancestor", c.head, tip); err != nil {
		if x := (*exec.ExitError)(nil); errors.As(err, &x) && x.ExitCode() == 1 {
			return "", tipRefused(c, tip), ""
		}
		return "", "", "git could not say whether " + tip + " descends from " + c.head + ": " + firstLine("", err)
	}
	return tip, "", ""
}
