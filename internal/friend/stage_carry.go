package friend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// A card's CARRY: line names the head of an earlier attempt (member.CarryLine: `CARRY: <card>
// attempt <n> head=<sha40>`), the work the stage carries onto the base tip before the lane
// starts. The finding of 2026-10-07: every rework's model fetched the base, merged the CARRY
// head onto it, resolved the merge and re-ran the gates before its own work began; the strong
// models compensated and spent the tokens, the flash models failed. The stage does the merge
// instead (docs/SPEC-FRIEND.md, the daemon stages every job it writes).

// carryHead is the carried head's full sha as the brief's CARRY value names it (its trailing
// `head=<sha40>`); "" when the value names none (a CARRY line that is no full sha is no carry,
// as member.Carried reads it).
func carryHead(v string) string {
	if i := strings.LastIndex(v, "head="); i >= 0 {
		if sha := v[i+len("head="):]; shaRE.MatchString(sha) {
			return sha
		}
	}
	return ""
}

// carryCommitMsg is the message of the commit a clean carry makes: `carry <head> onto <base
// tip>`, both full shas.
func carryCommitMsg(carried, baseTip string) string {
	return fmt.Sprintf("carry %s onto %s", carried, baseTip)
}

// carryStep merges the card's carried head onto the checkout's base tip, before the lane
// starts (Stager.Stage calls it after the worktree is moved in and before JOB.md is written).
// It answers the checkout's head after the merge: the merge commit for a clean merge, the base
// tip for one with conflicts, whose files it also answers. A carried head the mirror does not
// hold (its branch was pruned from the remote) is a stage fault: the card's REPORT.md is
// written Verdict FAIL naming the head, and the error names it too.
func (s *Stager) carryStep(ctx context.Context, checkout, mirror string, p Packet, baseTip string) (head string, conflicts []string, err error) {
	if p.Carry == "" {
		return baseTip, nil, nil
	}
	// A previously fetched object survives `fetch --prune`, and a prior job's local branch
	// can keep it forever. Only a freshly fetched remote ref proves the head is still held.
	if _, err := s.git(ctx, MirrorCloneBudget, "-C", mirror, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return "", nil, fmt.Errorf("fetching carry refs: %w", err)
	}
	_, objectErr := s.git(ctx, 0, "-C", mirror, "rev-parse", "--verify", "--quiet", "--end-of-options", p.Carry+"^{commit}")
	var refs string
	if objectErr == nil {
		refs, err = s.git(ctx, 0, "-C", mirror, "for-each-ref", "--format=%(refname)", "--contains", p.Carry, "refs/remotes/origin/", "refs/tags/")
		if err != nil {
			return "", nil, fmt.Errorf("checking carry refs: %w", err)
		}
	}
	if objectErr != nil || strings.TrimSpace(refs) == "" {
		if werr := s.writeCarryFail(p); werr != nil {
			return "", nil, fmt.Errorf("carry head %s is gone, and its FAIL report cannot be written: %s", p.Carry, oneLine(werr.Error(), 300))
		}
		return "", nil, &NotStageable{Repo: p.Repo, Card: p.Card, Job: p.Job,
			Why:    fmt.Sprintf("its CARRY head %s is gone (the cleanup pruned the branch that held it from %s)", p.Carry, s.url(p.Repo)),
			Remedy: fmt.Sprintf("cut the card with a CARRY head %s still holds, or onto a base that already has the work", s.url(p.Repo))}
	}
	if _, err = s.git(ctx, 0, "-C", checkout, "merge", "--no-ff", "-m", carryCommitMsg(p.Carry, baseTip), p.Carry); err != nil {
		files, ferr := s.conflictedFiles(ctx, checkout)
		if ferr != nil {
			return "", nil, ferr
		}
		if len(files) == 0 {
			return "", nil, fmt.Errorf("carrying %s onto %s: %s", p.Carry, baseTip, oneLine(err.Error(), 300))
		}
		return baseTip, files, nil
	}
	head, err = s.git(ctx, 0, "-C", checkout, "rev-parse", "HEAD")
	if err != nil {
		return "", nil, err
	}
	return head, nil, nil
}

// resumeCarry completes a stage interrupted after moving the worktree but before writing
// JOB.md. A merge already committed or left conflicted is preserved; an untouched checkout
// still runs the carry step before the lane can see JOB.md.
func (s *Stager) resumeCarry(ctx context.Context, checkout string, p Packet) (string, []string, error) {
	sha, err := s.git(ctx, 0, "-C", checkout, "rev-parse", "HEAD")
	if err != nil {
		return "", nil, err
	}
	conflicts, err := s.conflictedFiles(ctx, checkout)
	if err != nil || len(conflicts) > 0 || p.Carry == "" {
		return sha, conflicts, err
	}
	if _, err := s.git(ctx, 0, "-C", checkout, "merge-base", "--is-ancestor", p.Carry, "HEAD"); err == nil {
		return sha, nil, nil // the clean carry committed before the interruption
	}
	lock := s.repoLock(p.Repo)
	lock.Lock()
	defer lock.Unlock()
	mirror, err := s.mirror(ctx, p.Repo)
	if err != nil {
		return "", nil, err
	}
	return s.carryStep(ctx, checkout, mirror, p, sha)
}

// conflictedFiles is the paths of the checkout's conflicted files, in git's order: the merge a
// carry left unmerged.
func (s *Stager) conflictedFiles(ctx context.Context, checkout string) ([]string, error) {
	out, err := s.git(ctx, 0, "-C", checkout, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// carryFailReport is the REPORT.md of a card whose carried head is gone: Verdict FAIL and the
// exact line, so the outbox pass finishes it --failed and the card is dealt again.
func carryFailReport(head string) string {
	return fmt.Sprintf("Verdict: FAIL\n\ncarry head %s is gone\n", head)
}

// writeCarryFail writes the FAIL report of a card whose carried head is gone to its outbox, so
// the daemon's outbox pass finishes the card --failed; it never overwrites a report already
// there.
func (s *Stager) writeCarryFail(p Packet) error {
	dir := filepath.Join(s.Dir, "outbox", p.Job)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	err := atomicfile.WriteFile(filepath.Join(dir, "REPORT.md"), []byte(carryFailReport(p.Carry)), 0o644, atomicfile.NoReplace())
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	return err
}

// dropCarried removes the checkout a carry fault left behind: its worktree from the mirror, the
// branch it created, and the stale worktree entries, so a later stage of the job starts from
// nothing, never from a checkout that looks staged but carries no JOB.md.
func (s *Stager) dropCarried(ctx context.Context, mirror, checkout, branch string, created bool) {
	_, _ = s.git(ctx, 0, "-C", mirror, "worktree", "remove", "--force", checkout) // ignored: the checkout is best-effort removed; the next stage removes it first
	if created {
		_, _ = s.git(ctx, 0, "-C", mirror, "branch", "--quiet", "-D", branch) // ignored: at the base, nothing on it; the next stage creates it again or takes it as it stands
	}
	_, _ = s.git(ctx, 0, "-C", mirror, "worktree", "prune") // ignored: a stale entry is pruned by the next stage
}

// conflictsSection is JOB.md's CONFLICTS part for a carry merge with conflicts: the two sides'
// shas (ours, the base tip, and theirs, the carried head) and the conflicted files, one a
// line, the shape readCarryConflicts reads back.
func conflictsSection(baseTip, carried string, files []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nCONFLICTS: ours=%s theirs=%s\n", baseTip, carried)
	for _, f := range files {
		fmt.Fprintf(&b, "%s\n", f)
	}
	return b.String()
}

// CarryPrompt is the text a lane's prompt opens with for a carry merge with conflicts: the two
// sides and the conflicted files, for the model to resolve first, nothing else about the base.
func CarryPrompt(baseTip, carried string, files []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nova-friend: the carry merge of %s onto %s conflicted; resolve these files first, then do the card:\n", carried, baseTip)
	for _, f := range files {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	return b.String()
}

// readCarryConflicts reads a job's JOB.md for its CONFLICTS section: the two sides' shas and
// the conflicted files. ok is false when the job has none.
func readCarryConflicts(jobDir string) (baseTip, carried string, files []string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(jobDir, JobFile))
	if err != nil {
		return "", "", nil, false
	}
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		rest, found := strings.CutPrefix(l, "CONFLICTS: ")
		if !found {
			continue
		}
		for _, field := range strings.Fields(rest) {
			if v, o := strings.CutPrefix(field, "ours="); o {
				baseTip = v
			}
			if v, o := strings.CutPrefix(field, "theirs="); o {
				carried = v
			}
		}
		for _, f := range lines[i+1:] {
			f = strings.TrimSpace(f)
			if f == "" {
				break
			}
			files = append(files, f)
		}
		return baseTip, carried, files, true
	}
	return "", "", nil, false
}

// CarryPromptOf is the opening of a lane's prompt for a job whose carry merge conflicted: its
// JOB.md's CONFLICTS section as CarryPrompt; "" when the job has none.
func CarryPromptOf(jobDir string) string {
	baseTip, carried, files, ok := readCarryConflicts(jobDir)
	if !ok {
		return ""
	}
	return CarryPrompt(baseTip, carried, files)
}
