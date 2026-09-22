package pulse

// THE HARVEST COMMIT STEP'S EFFECT HALF (nova-tools #2549).
//
// harvestcommit.go decides; this file acts, and it NEVER re-decides. It reads one job
// directory into a CommitJob (ReadCommitJob), hands it to CommitDecision, and applies the
// plan that comes back: set the card's own files aside, stage the card's declared paths,
// commit line 1 of the RESULT.md, drop the scratch the card added, rebase onto the target
// at its live tip when it moved, and write the BASE/BRANCH/prior lines the RESULT.md is
// missing.
//
// EVERY STEP PRINTS ONE LINE. CommitResult.Lines carries them in order, and the verb
// prints them; a step that did nothing produces no line and a step that failed produces a
// line saying so. The bash printed `BRANCH-LINE ... -> ...` 94 times for a line that was
// never written, because `sed "s#^BRANCH[: ].*#...#"` substitutes nothing when the line is
// absent and exits 0. Here the line is WRITTEN -- appended when absent, rewritten in place
// when present and different -- and the verdict is printed from what the write returned.
//
// git is a child process; a bench is not. Nothing in this file opens a connection: the
// rebase target is fetched from a LOCAL mirror path the caller names, which is what the
// bash did (`$HOME/nova-bench/mirror/<repo>.git`) and what lets the whole thing be tested
// against real repositories under t.TempDir().

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/keyshape"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// commitAuthor is who the step commits as. The card's own work, recorded by the harvest.
var commitAuthor = []string{"-c", "user.name=Rowan", "-c", "user.email=rowan@mas-bandwidth.com"}

// CommitResult is what one job's apply did: the verdict lines in the order they happened,
// the branch and the sha when there is one, and whether anything was actually committed.
type CommitResult struct {
	Label     string
	Branch    string
	SHA       string
	Committed bool
	Rebased   bool
	Lines     []string
}

func (r *CommitResult) line(format string, a ...any) {
	r.Lines = append(r.Lines, fmt.Sprintf(format, a...))
}

// CommitEnv is the little the effect needs that is not a decision: where the mirror of each
// repository is (the rebase target is fetched from it), and whether this is a rehearsal.
type CommitEnv struct {
	// Mirrors maps `<owner>/<name>` or a bare repo name to a local mirror directory; a
	// "" key is the mirror for any repository. An empty map means no rebase is
	// attempted and the step says so rather than rebasing onto a guess.
	Mirrors map[string]string
	// DryRun prints what would happen and changes nothing.
	DryRun bool
}

func (e CommitEnv) mirror(repo string) string {
	if e.Mirrors == nil {
		return ""
	}
	name := strings.TrimSpace(repo)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	for _, k := range []string{strings.TrimSpace(repo), name, ""} {
		if d, ok := e.Mirrors[k]; ok && strings.TrimSpace(d) != "" {
			return d
		}
	}
	return ""
}

// ApplyCommitPlan carries out one plan against one job directory. A skip and a refusal do
// nothing at all but name themselves; only CommitCommit and CommitBranchOnly touch git.
func ApplyCommitPlan(jobDir string, p CommitPlan, env CommitEnv) (CommitResult, error) {
	r := CommitResult{Label: p.Label, Branch: p.Branch}
	repo := filepath.Join(jobDir, "repo")
	result := filepath.Join(jobDir, "RESULT.md")

	switch p.Action {
	case CommitSkip:
		r.line("SKIP label=%s reason=%s %s", field(p.Label), p.Why, p.Detail)
		return r, nil
	case CommitRefuse:
		r.line("REFUSED label=%s reason=%s path=%s shape=%s %s",
			field(p.Label), p.Why, field(p.Path), field(p.Shape), p.Detail)
		return r, nil
	}

	if env.DryRun {
		r.line("DRY-RUN label=%s branch=%s action=%s stage=%s set-aside=%s",
			field(p.Label), field(p.Branch), p.Action, field(stageSummary(p)), field(strings.Join(p.SetAside, ",")))
		return r, nil
	}

	// The card's own files leave the working tree before anything is staged. They are
	// MOVED into the job directory, never deleted: a refusal here left 88 DONE cells
	// unharvested on 2026-09-21, and a delete would destroy the only copy of a report.
	if len(p.SetAside) > 0 {
		moved, err := setAsideCardScratch(jobDir, repo, p.SetAside)
		if len(moved) > 0 {
			r.line("SCRATCH-MOVED label=%s files=%s (the card's own files set aside in the job directory; the rest is committed)",
				field(p.Label), field(strings.Join(moved, ",")))
		}
		if err != nil {
			r.line("NOTE label=%s set-aside: %s", field(p.Label), oneline.Err(err))
		}
	}

	if out, err := gitIn(repo, append(append([]string{}, commitAuthor...), "checkout", "-q", "-B", p.Branch)...); err != nil {
		r.line("FAILED label=%s step=checkout branch=%s: %s", field(p.Label), field(p.Branch), oneline.Cap(out, 160))
		return r, fmt.Errorf("checkout -B %s: %w", p.Branch, err)
	}

	if p.Action == CommitCommit {
		if err := stageAndCommit(&r, repo, p); err != nil {
			return r, err
		}
	} else {
		r.line("BRANCH-ONLY label=%s branch=%s (the work was already committed; the branch now names it)",
			field(p.Label), field(p.Branch))
	}

	// The scratch the CARD ADDED, dropped in a follow-up commit so the branch carries
	// the work and nothing else.
	dropCardScratch(&r, repo, p)

	// The rebase onto the target at its LIVE tip. A target that moved under a finished
	// card is the whole of the stale-base refusal pile (#2547); rebasing here is what
	// stops those PRs being opened at all.
	rebaseOntoTarget(&r, repo, p, env)

	if sha, err := gitIn(repo, "rev-parse", "--short", "HEAD"); err == nil {
		r.SHA = strings.TrimSpace(sha)
	}

	// The RESULT.md lines, WRITTEN. This is the half the bash could not do.
	writeResultLines(&r, result, p)
	foldReport(&r, jobDir, result, p)
	return r, nil
}

func stageSummary(p CommitPlan) string {
	if p.StageAll {
		return "-A"
	}
	return strings.Join(p.Stage, ",")
}

// stageAndCommit stages exactly what the plan named, runs the CONTENT half of the
// exfiltration fence over what is staged, and commits.
func stageAndCommit(r *CommitResult, repo string, p CommitPlan) error {
	args := []string{"add", "--"}
	if p.StageAll {
		args = []string{"add", "-A"}
		r.line("WARN label=%s %s", field(p.Label), p.Warn)
	} else {
		args = append(args, p.Stage...)
	}
	if out, err := gitIn(repo, args...); err != nil {
		// A declared path the card did not in fact change is not a reason to lose
		// the rest of the work: git names it, the line says so, and the commit is
		// attempted with what did stage.
		r.line("NOTE label=%s step=add: %s", field(p.Label), oneline.Cap(out, 160))
	}
	staged, err := stagedPaths(repo)
	if err != nil {
		r.line("FAILED label=%s step=staged-list: %s", field(p.Label), oneline.Err(err))
		return err
	}
	if len(staged) == 0 {
		r.line("SKIP label=%s reason=nothing-staged (the plan's paths matched nothing in the working tree)", field(p.Label))
		return nil
	}
	// THE CONTENT FENCE (#2609). The path fence is the decision's; this is the bytes.
	// It runs BEFORE the commit and on what is STAGED, which is exactly what a push
	// would carry. A hit resets the index and commits nothing, and prints the path and
	// the shape and not one byte of what it matched.
	if f, ok := stagedSecret(repo, staged); ok {
		_, _ = gitIn(repo, "reset", "-q")
		r.line("REFUSED label=%s reason=secret-shape path=%s shape=%s line=%d (a staged file carries the form of a credential; nothing was committed and nothing was printed of it)",
			field(p.Label), field(f.Path), field(f.Shape), f.Line)
		return nil
	}
	args = append(append([]string{}, commitAuthor...), "commit", "-q", "-m", p.Message)
	if out, err := gitIn(repo, args...); err != nil {
		r.line("FAILED label=%s step=commit: %s", field(p.Label), oneline.Cap(out, 160))
		return fmt.Errorf("commit: %w", err)
	}
	sha, _ := gitIn(repo, "rev-parse", "--short", "HEAD")
	r.Committed, r.SHA = true, strings.TrimSpace(sha)
	r.line("COMMITTED label=%s branch=%s sha=%s files=%d", field(p.Label), field(p.Branch), field(r.SHA), len(staged))
	return nil
}

func stagedPaths(repo string) ([]string, error) {
	out, err := gitIn(repo, "diff", "--cached", "--name-only", "--no-renames")
	if err != nil {
		return nil, fmt.Errorf("%s: %s", oneline.Err(err), oneline.Cap(out, 160))
	}
	var paths []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	return paths, nil
}

// stagedSecret scans each staged file that is still on disk. A file that is not there is a
// deletion and carries nothing.
func stagedSecret(repo string, staged []string) (keyshape.Finding, bool) {
	env := os.Environ()
	for _, p := range staged {
		findings, err := keyshape.ScanFile(filepath.Join(repo, p), env)
		if err != nil {
			// A file the scan could not read is NOT a clean file. The fence fails
			// closed, the same way the harvest's does (#1838).
			return keyshape.Finding{Shape: "unreadable", Path: p}, true
		}
		if len(findings) > 0 {
			f := findings[0]
			f.Path = p
			return f, true
		}
	}
	return keyshape.Finding{}, false
}

// setAsideCardScratch moves the card's own files out of the repository and into
// <jobDir>/scratch-from-repo/. It never overwrites and never deletes.
func setAsideCardScratch(jobDir, repo string, names []string) ([]string, error) {
	dir := filepath.Join(jobDir, "scratch-from-repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var moved []string
	var firstErr error
	for _, n := range names {
		src := filepath.Join(repo, n)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := filepath.Join(dir, n)
		for i := 1; ; i++ {
			if _, err := os.Lstat(dst); os.IsNotExist(err) {
				break
			}
			if i > 100 {
				break
			}
			dst = filepath.Join(dir, fmt.Sprintf("%s.%d", n, i))
		}
		if err := os.Rename(src, dst); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		moved = append(moved, n)
	}
	return moved, firstErr
}

// dropCardScratch removes, in a FOLLOW-UP commit, every path the card ADDED between its
// base and HEAD that is scratch rather than work.
func dropCardScratch(r *CommitResult, repo string, p CommitPlan) {
	base := p.BaseSHA
	if base == "" {
		return
	}
	if _, err := gitIn(repo, "cat-file", "-e", base+"^{commit}"); err != nil {
		return
	}
	out, err := gitIn(repo, "diff", "--name-only", "--diff-filter=A", "--no-renames", base, "HEAD")
	if err != nil {
		return
	}
	var scratch []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && cardAddedScratch.MatchString(l) {
			scratch = append(scratch, l)
		}
	}
	if len(scratch) == 0 {
		return
	}
	sort.Strings(scratch)
	for _, f := range scratch {
		_, _ = gitIn(repo, "rm", "-q", "-f", "--", f)
	}
	args := append(append([]string{}, commitAuthor...), "commit", "-q", "-m", "harvest: drop card scratch outside PATHS")
	if out, err := gitIn(repo, args...); err != nil {
		r.line("NOTE label=%s step=scratch-drop: %s", field(p.Label), oneline.Cap(out, 160))
		return
	}
	r.line("SCRATCH-DROPPED label=%s files=%s", field(p.Label), field(strings.Join(scratch, ",")))
}

// rebaseOntoTarget fetches the card's target from the local mirror and rebases onto it when
// the target has moved past the branch. A conflict aborts and prints REBASE-CONFLICT: that
// is author work, and the card is recut rather than resolved here.
func rebaseOntoTarget(r *CommitResult, repo string, p CommitPlan, env CommitEnv) {
	if p.Base == "" {
		return
	}
	mirror := env.mirror(p.Repo)
	if mirror == "" {
		r.line("NOTE label=%s rebase: no mirror for %s, so the target was not fetched and no rebase was attempted",
			field(p.Label), field(p.Repo))
		return
	}
	if out, err := gitIn(repo, "status", "--porcelain"); err == nil && strings.TrimSpace(out) != "" {
		r.line("NOTE label=%s rebase: the working tree is not clean, so no rebase was attempted", field(p.Label))
		return
	}
	if out, err := gitIn(repo, "fetch", "-q", "--no-tags", mirror, "refs/heads/"+p.Base); err != nil {
		r.line("NOTE label=%s rebase: fetching %s from %s: %s",
			field(p.Label), field(p.Base), field(mirror), oneline.Cap(out, 160))
		return
	}
	if _, err := gitIn(repo, "merge-base", "--is-ancestor", "FETCH_HEAD", "HEAD"); err == nil {
		return // the branch already sits on the target's tip
	}
	tip, _ := gitIn(repo, "rev-parse", "--short", "FETCH_HEAD")
	args := append(append([]string{}, commitAuthor...), "rebase", "-q", "FETCH_HEAD")
	if _, err := gitIn(repo, args...); err != nil {
		_, _ = gitIn(repo, "rebase", "--abort")
		r.line("REBASE-CONFLICT label=%s base=%s@%s (the target moved under the card and the replay conflicts; this is author work: recut)",
			field(p.Label), field(p.Base), field(strings.TrimSpace(tip)))
		return
	}
	r.Rebased = true
	r.line("REBASED label=%s base=%s@%s (the target moved under the card)",
		field(p.Label), field(p.Base), field(strings.TrimSpace(tip)))
}

// writeResultLines writes the BASE, BRANCH and prior lines into the RESULT.md. WRITE, not
// substitute: `sed "s#^BRANCH[: ].*#...#"` changes nothing when the line is absent and
// exits 0, so every report-* card was told its BRANCH line had been written, 94 times, and
// not one of them ever had one (#2549).
func writeResultLines(r *CommitResult, result string, p CommitPlan) {
	if p.WriteBase && p.Base != "" {
		switch changed, err := ensureResultLine(result, "BASE", p.Base); {
		case err != nil:
			r.line("NOTE label=%s BASE line: %s", field(p.Label), oneline.Err(err))
		case changed:
			r.line("BASE-LINE label=%s base=%s (the branch the card sits on)", field(p.Label), field(p.Base))
		}
	}
	if p.WriteBranch && p.Branch != "" {
		switch changed, err := ensureResultLine(result, "BRANCH", p.Branch); {
		case err != nil:
			r.line("NOTE label=%s BRANCH line: %s", field(p.Label), oneline.Err(err))
		case changed:
			r.line("BRANCH-LINE label=%s branch=%s", field(p.Label), field(p.Branch))
		}
	}
	if p.WritePrior && p.PriorPR > 0 {
		v := fmt.Sprintf("#%d", p.PriorPR)
		switch changed, err := ensureResultLine(result, "prior:", v); {
		case err != nil:
			r.line("NOTE label=%s prior line: %s", field(p.Label), oneline.Err(err))
		case changed:
			r.line("PRIOR-LINE label=%s prior=%s repo=%s", field(p.Label), field(v), field(p.PriorRepo))
		}
	}
}

// ensureResultLine is the write. It reports whether the FILE CHANGED, which is the fact the
// verdict line is printed from -- never whether a substitution command exited 0.
func ensureResultLine(path, name, value string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(raw)
	nl := "\n"
	lines := strings.Split(strings.TrimSuffix(text, nl), nl)
	want := name + " " + value
	if strings.HasSuffix(name, ":") {
		want = name + " " + value
	}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		rest, ok := strings.CutPrefix(t, name)
		if !ok {
			continue
		}
		if !strings.HasSuffix(name, ":") {
			if rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != ':' {
				continue
			}
		}
		if t == want {
			return false, nil
		}
		lines[i] = want
		return true, os.WriteFile(path, []byte(strings.Join(lines, nl)+nl), 0o644)
	}
	out := strings.TrimSuffix(text, nl)
	if out != "" {
		out += nl
	}
	out += want + nl
	return true, os.WriteFile(path, []byte(out), 0o644)
}

// foldReport appends at most commitReportFold bytes of the card's REPORT.md to its
// RESULT.md, so the PR body the harvest builds carries the card's own report.
func foldReport(r *CommitResult, jobDir, result string, p CommitPlan) {
	if !p.FoldReport {
		return
	}
	src := commitReportPath(jobDir)
	if src == "" {
		return
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if len(raw) > commitReportFold {
		raw = raw[:commitReportFold]
	}
	f, err := os.OpenFile(result, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		r.line("NOTE label=%s report fold: %s", field(p.Label), oneline.Err(err))
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n--- REPORT (the card, at most %d bytes) ---\n%s\n", commitReportFold, raw)
	r.line("REPORT-FOLDED label=%s bytes=%d", field(p.Label), len(raw))
}

// commitReportPath is where a card's REPORT.md may be: beside the RESULT.md, beside the
// `jobs/` directory, or in the slot above it. The first that exists.
func commitReportPath(jobDir string) string {
	for _, c := range []string{
		filepath.Join(jobDir, "REPORT.md"),
		filepath.Join(jobDir, "..", "REPORT.md"),
		filepath.Join(jobDir, "..", "..", "REPORT.md"),
	} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// ReadCommitJob reads ONE job directory into the value CommitDecision judges. It is the
// only place the filesystem is read, and it decides nothing: every rule is in
// harvestcommit.go, against what this returns.
func ReadCommitJob(jobDir string, cfg CommitScan) (CommitJob, error) {
	job := CommitJob{
		Label:        filepath.Base(jobDir),
		DraftOnly:    cfg.DraftOnly,
		FallbackBase: cfg.Base,
	}
	repo := filepath.Join(jobDir, "repo")
	if st, err := os.Stat(filepath.Join(repo, ".git")); err == nil && (st.IsDir() || st.Mode().IsRegular()) {
		job.HasRepo = true
	}
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err == nil {
		job.HasResult = true
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		if len(lines) > 0 {
			job.Line1 = lines[0]
		}
		if len(lines) > 1 {
			job.Line2 = lines[1]
		}
		job.ResultBranch = resultField(lines, "BRANCH")
		job.ResultBase = resultField(lines, "BASE")
		job.ResultRepo = strings.TrimPrefix(resultField(lines, "REPO"), "github.com/")
		job.HasBranchLine = job.ResultBranch != ""
		job.HasBaseLine = job.ResultBase != ""
		job.HasPriorLine = resultField(lines, "prior:") != "" || hasLinePrefix(lines, "prior:")
		job.HasClaimLine = hasLinePrefix(lines, "CLAIM:")
	} else if !os.IsNotExist(err) {
		return job, err
	}
	job.HasReportToFold = commitReportPath(jobDir) != ""
	if job.HasRepo {
		changed, err := readChangedFiles(repo)
		if err != nil {
			return job, err
		}
		job.Changed = changed
	}
	label := strings.TrimPrefix(job.Label, "card-00-")
	if card := commitCardPath(cfg.Cards, job.Label, label); card != "" {
		if raw, err := os.ReadFile(card); err == nil {
			job.CardFound = true
			if globs, declared := parsePATHS(string(raw)); declared {
				job.CardPaths = globs
			}
			// THE CARD'S BASE WINS over the RESULT.md's (the bash's `rr2=card`
			// arm): the card is what the pulse cut, the RESULT.md is what a
			// worker wrote, and only one of those two is evidence. The RESULT
			// line is then WRITTEN to match, which is what BASE-LINE reports.
			job.CardBase = harvestTargetName(cardField(string(raw), "BASE"))
		}
	}
	return job, nil
}

func hasLinePrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return true
		}
	}
	return false
}

func cardField(text, name string) string {
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if rest, ok := strings.CutPrefix(t, name+":"); ok {
			return strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(t, name+" "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// commitCardPath finds the card for a label under the cards directories the caller named.
// The bash looked in exactly one place under exactly one name; a label that had been cut
// with a different prefix simply had no card and was staged `-A` with nobody the wiser.
func commitCardPath(dirs []string, names ...string) string {
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" {
			continue
		}
		for _, n := range names {
			for _, c := range []string{n + ".md", "card-00-" + n + ".md"} {
				p := filepath.Join(d, c)
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					return p
				}
			}
		}
	}
	return ""
}

// readChangedFiles is `git status --porcelain -z` in the card's repository, with each
// path's size. `-z` is not a detail: a path with a space, a quote or a newline in it is
// exactly what broke the bash's `awk '{print $2}'`, and a rename prints two paths.
func readChangedFiles(repo string) ([]ChangedFile, error) {
	out, err := gitIn(repo, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("git status in %s: %s", field(repo), oneline.Err(err))
	}
	var files []ChangedFile
	seen := map[string]bool{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		e := fields[i]
		if len(e) < 4 {
			continue
		}
		xy, p := e[:2], e[3:]
		if xy[0] == 'R' || xy[1] == 'R' {
			// `R  <new>\0<old>`: the NEW name is on this entry and the old one is
			// the next field. Both are changed paths.
			if i+1 < len(fields) && fields[i+1] != "" {
				files = appendChanged(files, seen, repo, fields[i+1])
				i++
			}
		}
		files = appendChanged(files, seen, repo, p)
	}
	sort.Slice(files, func(a, b int) bool { return files[a].Path < files[b].Path })
	return files, nil
}

func appendChanged(files []ChangedFile, seen map[string]bool, repo, p string) []ChangedFile {
	p = strings.TrimSpace(p)
	p = strings.TrimSuffix(p, "/")
	if p == "" || seen[p] {
		return files
	}
	seen[p] = true
	var size int64
	if st, err := os.Lstat(filepath.Join(repo, p)); err == nil && st.Mode().IsRegular() {
		size = st.Size()
	}
	return append(files, ChangedFile{Path: p, Size: size})
}

// CommitScan is what a walk of a root needs: where the cards are, the fallback target, and
// the DRAFT-ONLY prefixes.
type CommitScan struct {
	Cards     []string
	Base      string
	DraftOnly []string
}

// CommitJobDirs is every job directory under a root, in a stable order:
// `<root>/<slot>/jobs/<label>`, which is the shape `harvest --bench`'s own listing walks.
//
// TWO NARROWINGS OF THE BASH ARE GONE, both of them silent. It globbed `*card-*` over the
// slot names, so a slot cut under any other name was not looked at and nothing said so;
// the slot is identified here by HAVING a `jobs/` directory, which is a fact rather than a
// naming convention. And it took `ls -d $d/jobs/* | head -1`, so a slot holding two jobs
// lost one of them with no line about it. Every job under every slot, or a reason.
func CommitJobDirs(root string) ([]string, error) {
	slots, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range slots {
		if !s.IsDir() {
			continue
		}
		jobs, err := os.ReadDir(filepath.Join(root, s.Name(), "jobs"))
		if err != nil {
			continue
		}
		for _, j := range jobs {
			if j.IsDir() {
				out = append(out, filepath.Join(root, s.Name(), "jobs", j.Name()))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadDraftOnly reads the DRAFT-ONLY label prefixes from a file, one per line, `#` a
// comment. The bash carried nine of them inline in a single-quoted shell string inside an
// ssh, which is a policy nobody could change without risking the whole step.
func ReadDraftOnly(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// CommitInput is the `nova-pulse commit` verb, held apart from flag parsing.
type CommitInput struct {
	Root      string
	Bench     string
	Cards     []string
	Mirrors   map[string]string
	DraftOnly string
	Base      string
	Max       int
	DryRun    bool
	Stdout    io.Writer
	Stderr    io.Writer
}

// Commit is the verb: walk the root, decide each job, apply each plan, print EVERY verdict
// one line at a time, and finish with the counts. It is what the coordinator's
// `printf '%s\n' "$commit_step" | ssh <bench> bash -s` did, in one binary with a test.
func Commit(in CommitInput) int {
	if strings.TrimSpace(in.Root) == "" {
		return refusal(in.Stderr, "COMMIT", fmt.Errorf("missing --root; name the swarm root whose job directories this folds"))
	}
	draft, err := ReadDraftOnly(in.DraftOnly)
	if err != nil {
		return refusal(in.Stderr, "COMMIT", fmt.Errorf("--draft-only %s: %s", field(in.DraftOnly), oneline.Err(err)))
	}
	dirs, err := CommitJobDirs(in.Root)
	if err != nil {
		return refusal(in.Stderr, "COMMIT", fmt.Errorf("--root %s could not be read: %s", field(in.Root), oneline.Err(err)))
	}
	cfg := CommitScan{Cards: in.Cards, Base: in.Base, DraftOnly: draft}
	env := CommitEnv{Mirrors: in.Mirrors, DryRun: in.DryRun}
	lines := bound(in.Stdout, in.Max)

	var committed, skipped, refused, rebased, failed int
	for _, dir := range dirs {
		job, err := ReadCommitJob(dir, cfg)
		if err != nil {
			failed++
			lines.Line(fmt.Sprintf("COMMIT FAILED bench=%s label=%s reason=unreadable-job %s",
				field(in.Bench), field(filepath.Base(dir)), oneline.Err(err)))
			continue
		}
		plan := CommitDecision(job)
		res, applyErr := ApplyCommitPlan(dir, plan, env)
		for _, l := range res.Lines {
			lines.Line("COMMIT " + insertBench(l, in.Bench))
		}
		switch {
		case applyErr != nil:
			failed++
		case plan.Action == CommitSkip:
			skipped++
		case plan.Action == CommitRefuse:
			refused++
		case res.Committed:
			committed++
		}
		if res.Rebased {
			rebased++
		}
	}
	lines.More()
	fmt.Fprintf(in.Stdout, "COMMIT BENCH %s bench=%s jobs=%d committed=%d skipped=%d refused=%d rebased=%d failed=%d dry-run=%s\n",
		okOrRed(failed), field(in.Bench), len(dirs), committed, skipped, refused, rebased, failed, yesNo(in.DryRun))
	if failed > 0 {
		return 1
	}
	return 0
}

// insertBench puts `bench=<name>` after the verdict token, so every line carries the bench
// the way the harvest's lines do.
func insertBench(line, bench string) string {
	tok, rest, ok := strings.Cut(line, " ")
	if !ok {
		return line
	}
	return tok + " bench=" + field(bench) + " " + rest
}
