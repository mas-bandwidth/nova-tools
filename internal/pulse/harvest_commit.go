package pulse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// CardScratchFiles are the scratch files set aside from the repo into scratch-from-repo/.
var CardScratchFiles = []string{
	"RESULT.md",
	"notes.txt",
	"REPORT.md",
	"usage.tsv",
	"harness-output.log",
	"repo.bundle",
}

// MaxChangedFileBytes is the 1 MB limit (1048576 bytes) past which a dirty tree is refused.
const MaxChangedFileBytes = 1048576

// MaxReportFoldBytes is the maximum number of bytes from REPORT.md folded into RESULT.md.
const MaxReportFoldBytes = 1500

var (
	reDoneVerdict = regexp.MustCompile(`^DONE\b`)
	reScratchDrop = regexp.MustCompile(`(^|/)notes\.txt$|^notes/|^\.nova-sandbox-tmp/|\.(class|o|obj|pyc|exe)$|(^|/)a\.out$`)
	reBaseSHA     = regexp.MustCompile(`\bsha=([0-9a-f]{7,40})\b`)
	reRecutLabel  = regexp.MustCompile(`^recut-([a-zA-Z0-9_-]+)-([0-9]+)-.*`)
	rePriorLine   = regexp.MustCompile(`(?i)^prior[: ]`)
	reClaimLine   = regexp.MustCompile(`(?i)^claim[: ]`)
)

var draftOnlyPrefixes = []string{
	"fix-nova-tools-2454-",
	"fix-nova-tools-2416-",
	"fix-nova-tools-2414-",
	"fix-nova-tools-2379-",
	"fix-nova-tools-2425-",
	"fix-nova-tools-2426-",
	"fix-nova-tools-2415-",
	"fix-nova-tools-2459-",
	"fix-nova-tools-2417-",
}

// GitRunner executes git commands in a target directory.
type GitRunner interface {
	Run(dir string, args ...string) (string, error)
}

type defaultGitRunner struct{}

func (defaultGitRunner) Run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		errStr := strings.TrimSpace(stderr.String())
		if errStr != "" {
			return out, fmt.Errorf("%w: %s", err, errStr)
		}
		return out, err
	}
	return out, nil
}

// CommitJobInput configures the commit step for a single job.
type CommitJobInput struct {
	JobDir       string
	CardDir      string
	CardsDir     string
	MirrorDir    string
	BranchPrefix string
	DefaultBase  string
	GitUser      string
	GitEmail     string
	MaxFileSize  int64
	Clones       []string
	Runner       GitRunner
	Stdout       io.Writer
}

// CommitStepInput configures the commit step across multiple jobs or a root directory.
type CommitStepInput struct {
	Dir          string
	JobDirs      []string
	CardDir      string
	CardsDir     string
	MirrorDir    string
	BranchPrefix string
	DefaultBase  string
	GitUser      string
	GitEmail     string
	MaxFileSize  int64
	Clones       []string
	Runner       GitRunner
	Stdout       io.Writer
	Stderr       io.Writer
}

// IsDraftOnly reports whether a card label is marked draft-only per standing conventions.
func IsDraftOnly(label string) bool {
	for _, p := range draftOnlyPrefixes {
		if strings.HasPrefix(label, p) {
			return true
		}
	}
	return false
}

// IsDoneLine reports whether line 2 of RESULT.md matches prefix 'DONE' with word boundary.
// For example, 'DONE (both runs green)' and 'DONE: green' pass; 'DONEish' and 'ABSTAIN' do not.
func IsDoneLine(line string) bool {
	return reDoneVerdict.MatchString(strings.TrimSpace(line))
}

// DiscoverCommitJobs finds all job directories with a RESULT.md under root.
func DiscoverCommitJobs(root string) []string {
	var jobs []string
	if fi, err := os.Stat(filepath.Join(root, "RESULT.md")); err == nil && !fi.IsDir() {
		return []string{root}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name())
		if fi, err := os.Stat(filepath.Join(p, "RESULT.md")); err == nil && !fi.IsDir() {
			jobs = append(jobs, p)
			continue
		}
		jobsSub := filepath.Join(p, "jobs")
		if subEntries, err := os.ReadDir(jobsSub); err == nil {
			for _, se := range subEntries {
				if se.IsDir() {
					jp := filepath.Join(jobsSub, se.Name())
					if fi, err := os.Stat(filepath.Join(jp, "RESULT.md")); err == nil && !fi.IsDir() {
						jobs = append(jobs, jp)
					}
				}
			}
		}
	}
	sort.Strings(jobs)
	return jobs
}

// CommitStep runs the commit step on all discovered or provided jobs.
func CommitStep(in CommitStepInput) ([]string, error) {
	jobDirs := in.JobDirs
	if len(jobDirs) == 0 && in.Dir != "" {
		jobDirs = DiscoverCommitJobs(in.Dir)
	}
	var allLines []string
	var firstErr error
	for _, jd := range jobDirs {
		lines, err := CommitJob(CommitJobInput{
			JobDir:       jd,
			CardDir:      in.CardDir,
			CardsDir:     in.CardsDir,
			MirrorDir:    in.MirrorDir,
			BranchPrefix: in.BranchPrefix,
			DefaultBase:  in.DefaultBase,
			GitUser:      in.GitUser,
			GitEmail:     in.GitEmail,
			MaxFileSize:  in.MaxFileSize,
			Clones:       in.Clones,
			Runner:       in.Runner,
			Stdout:       in.Stdout,
		})
		if err != nil {
			if in.Stderr != nil {
				fmt.Fprintln(in.Stderr, err)
			}
			if firstErr == nil {
				firstErr = err
			}
		}
		allLines = append(allLines, lines...)
	}
	return allLines, firstErr
}

// CommitJob executes the harvest commit step for a single job directory.
// It verifies RESULT.md line 2 matches prefix 'DONE', verifies the git repository,
// refuses changed files > 1 MB, moves card scratch aside, checks out the declared branch,
// commits with line 1 of RESULT.md as message, drops scratch files added in base..HEAD,
// rebases onto moved target if needed, folds REPORT.md up to 1500 bytes into RESULT.md,
// and ensures BASE/BRANCH/prior lines are present and up to date in RESULT.md.
// Every verdict line is printed to in.Stdout (if set) and returned in the returned slice.
func CommitJob(in CommitJobInput) ([]string, error) {
	prefix := in.BranchPrefix
	if prefix == "" {
		prefix = "rowan/"
	}
	defaultBase := in.DefaultBase
	if defaultBase == "" {
		defaultBase = "dev"
	}
	user := in.GitUser
	if user == "" {
		user = "Rowan"
	}
	email := in.GitEmail
	if email == "" {
		email = "rowan@mas-bandwidth.com"
	}
	maxSize := in.MaxFileSize
	if maxSize <= 0 {
		maxSize = MaxChangedFileBytes
	}
	runner := in.Runner
	if runner == nil {
		runner = defaultGitRunner{}
	}

	var lines []string
	var committedVerdict string
	emit := func(l string) {
		lines = append(lines, l)
		if in.Stdout != nil {
			fmt.Fprintln(in.Stdout, l)
		}
	}

	rawLabel := filepath.Base(in.JobDir)
	label := strings.TrimPrefix(rawLabel, "card-00-")

	if strings.HasPrefix(label, "read-") {
		emit(fmt.Sprintf("SKIP %s reason=read-card", label))
		return lines, nil
	}

	if IsDraftOnly(label) {
		emit(fmt.Sprintf("DRAFT-ONLY %s (Glenn 2026-09-21: friends build the tooling; the swarm card is a draft, not committed or harvested)", label))
		return lines, nil
	}

	rPath := filepath.Join(in.JobDir, "RESULT.md")
	rawResult, err := os.ReadFile(rPath)
	if err != nil {
		emit(fmt.Sprintf("SKIP %s reason=no-result", label))
		return lines, nil
	}

	norm := strings.ReplaceAll(string(rawResult), "\r\n", "\n")
	resLines := strings.Split(norm, "\n")
	if len(resLines) < 2 {
		emit(fmt.Sprintf("SKIP %s reason=not-done line2=\"\"", label))
		return lines, nil
	}

	line1 := strings.TrimSpace(firstNonEmpty(resLines))
	line2 := strings.TrimSpace(resLines[1])
	if !IsDoneLine(line2) {
		emit(fmt.Sprintf("SKIP %s reason=not-done line2=%q", label, line2))
		return lines, nil
	}

	repoSub := filepath.Join(in.JobDir, "repo")
	repoDir := in.JobDir
	if isGitRepo(repoSub, runner) {
		repoDir = repoSub
	} else if !isGitRepo(in.JobDir, runner) {
		emit(fmt.Sprintf("SKIP %s reason=not-git-repo", label))
		return lines, nil
	}

	declaredBranch := strings.TrimSpace(resultToken(resLines, "BRANCH "))
	if declaredBranch == "" {
		declaredBranch = strings.TrimSpace(resultToken(resLines, "BRANCH:"))
	}
	if declaredBranch == "main" || declaredBranch == "master" {
		emit(fmt.Sprintf("BRANCH REFUSED %s: branch %s is a trunk; commit never commits to trunk", label, oneline.Quote(declaredBranch)))
		return lines, nil
	}
	if strings.Contains(declaredBranch, "/") && !strings.HasPrefix(declaredBranch, prefix) {
		emit(fmt.Sprintf("BRANCH REFUSED %s: branch %s is not under %s -- every branch this line pushes is; refusing to branch", label, oneline.Quote(declaredBranch), field(prefix)))
		return lines, nil
	}
	switch {
	case strings.HasPrefix(declaredBranch, prefix):
		// already has prefix
	case declaredBranch == "":
		declaredBranch = prefix + label
	default:
		declaredBranch = prefix + declaredBranch
	}

	if err := mustBranchPrefix(declaredBranch); err != nil {
		emit(fmt.Sprintf("BRANCH REFUSED %s: %s", label, err.Error()))
		return lines, nil
	}

	cardPath := findCardFile(in.CardDir, in.CardsDir, in.JobDir, label)
	claimedRepo := resultToken(resLines, "REPO ")
	if claimedRepo == "" {
		claimedRepo = resultToken(resLines, "REPO:")
	}
	claimedRepo = strings.TrimPrefix(strings.TrimSpace(claimedRepo), "github.com/")
	if cardPath != "" {
		if cRepo := cardRepo(cardPath); cRepo != "" && claimedRepo != "" && claimedRepo != cRepo {
			emit(fmt.Sprintf("REPO REFUSED %s: claim %s does not match card repo %s", label, claimedRepo, cRepo))
			return lines, nil
		}
	}
	if len(in.Clones) > 0 && claimedRepo != "" {
		if cloneFor(in.Clones, claimedRepo) == "" {
			emit(fmt.Sprintf("REPO REFUSED %s: claim %s is not in coordinator clone list", label, claimedRepo))
			return lines, nil
		}
	}

	curBranch, _ := runner.Run(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	curBranch = strings.TrimSpace(curBranch)

	statusShort, _ := runner.Run(repoDir, "status", "--short")
	statusShort = strings.TrimSpace(statusShort)

	if statusShort != "" {
		junkMoved := moveScratchFiles(in.JobDir, repoDir)
		if len(junkMoved) > 0 {
			emit(fmt.Sprintf("SCRATCH-MOVED %s %s(card scratch set aside in the job dir, the rest is committed; 2026-09-21: a refusal here left 88 DONE cells unharvested)", label, strings.Join(junkMoved, " ")+" "))
		}

		porcelain, err := runner.Run(repoDir, "status", "--porcelain", "-uall")
		if err != nil {
			return lines, fmt.Errorf("git status --porcelain for %s: %w", label, err)
		}
		porcelain = strings.TrimSpace(porcelain)

		if porcelain != "" {
			big := findBigFile(repoDir, porcelain, maxSize)
			if big != "" {
				emit(fmt.Sprintf("CLEAN-TREE REFUSED %s: %s (a file over 1 MB; attribution 2026-09-21); left uncommitted", label, big))
				return lines, nil
			}

			// Validate declared scope (PATHS)
			globs, declared := harvestDeclaredPaths(cardPath, resLines)
			if declared {
				for _, l := range strings.Split(porcelain, "\n") {
					f := parsePorcelainPath(l)
					if f == "" || isScratchFile(f) {
						continue
					}
					if !declaredCovers(globs, f) {
						emit(fmt.Sprintf("PATHS REFUSED %s: %s (outside declared PATHS; refusing to stage outside declared scope)", label, f))
						return lines, nil
					}
				}
			}

			// Validate branch reset precondition
			if curBranch != declaredBranch {
				// Refuse if declaredBranch already exists locally
				if _, err := runner.Run(repoDir, "rev-parse", "--verify", "refs/heads/"+declaredBranch); err == nil {
					emit(fmt.Sprintf("BRANCH-RESET REFUSED %s: branch %s already exists; refusing to reset unexpected branch", label, declaredBranch))
					return lines, nil
				}
				// Refuse if curBranch is an unexpected worker branch
				if strings.HasPrefix(curBranch, prefix) && curBranch != declaredBranch {
					emit(fmt.Sprintf("BRANCH REFUSED %s: current branch %s is an unexpected branch; refusing to branch from it", label, curBranch))
					return lines, nil
				}
				// Safely create and checkout new branch with -b
				if _, err := runner.Run(repoDir, "checkout", "-q", "-b", declaredBranch); err != nil {
					return lines, fmt.Errorf("checkout -b %s for %s: %w", declaredBranch, label, err)
				}
			}

			if _, err := runner.Run(repoDir, "add", "-A"); err != nil {
				return lines, fmt.Errorf("git add -A for %s: %w", label, err)
			}
			commitMsg := line1
			if len(commitMsg) > 200 {
				commitMsg = commitMsg[:200]
			}
			if _, err := runner.Run(repoDir, "-c", "user.name="+user, "-c", "user.email="+email, "commit", "-q", "-m", commitMsg); err != nil {
				return lines, fmt.Errorf("git commit for %s: %w", label, err)
			}
			shortHead, err := runner.Run(repoDir, "rev-parse", "--short", "HEAD")
			if err != nil {
				return lines, fmt.Errorf("git rev-parse HEAD for %s: %w", label, err)
			}
			committedVerdict = fmt.Sprintf("COMMITTED %s %s %s", label, declaredBranch, strings.TrimSpace(shortHead))
		}
	} else if curBranch != declaredBranch && curBranch != "HEAD" && curBranch != "" && curBranch != "main" && curBranch != "master" {
		if _, err := runner.Run(repoDir, "rev-parse", "--verify", "refs/heads/"+declaredBranch); err == nil {
			emit(fmt.Sprintf("BRANCH-RESET REFUSED %s: branch %s already exists; refusing to rename to existing branch", label, declaredBranch))
			return lines, nil
		}
		if _, err := runner.Run(repoDir, "branch", "-q", "-m", curBranch, declaredBranch); err == nil {
			emit(fmt.Sprintf("RENAMED %s %s -> %s", label, curBranch, declaredBranch))
		}
	}

	// Scratch drop outside PATHS: base..HEAD
	bs := extractBaseSHA(line1)
	if bs != "" {
		if _, err := runner.Run(repoDir, "cat-file", "-e", bs+"^{commit}"); err == nil {
			if s, _ := runner.Run(repoDir, "status", "--short"); strings.TrimSpace(s) == "" {
				diffOut, _ := runner.Run(repoDir, "diff", "--name-only", "--diff-filter=A", bs, "HEAD")
				var toDrop []string
				for _, f := range strings.Split(diffOut, "\n") {
					f = strings.TrimSpace(f)
					if f != "" && reScratchDrop.MatchString(f) {
						toDrop = append(toDrop, f)
					}
				}
				if len(toDrop) > 0 {
					for _, f := range toDrop {
						runner.Run(repoDir, "rm", "-q", "-f", f)
					}
					runner.Run(repoDir, "-c", "user.name="+user, "-c", "user.email="+email, "commit", "-q", "-m", "harvest: drop card scratch outside PATHS")
					emit(fmt.Sprintf("SCRATCH-DROPPED %s %s", label, strings.Join(toDrop, " ")))
				}
			}
		}
	}

	// Rebase onto moved target
	isReportOrRead := strings.HasPrefix(label, "report-") || strings.HasPrefix(label, "read-")
	rr2 := "nova-tools"
	isSchema := false
	for _, l := range resLines {
		t := strings.TrimSpace(l)
		if (strings.HasPrefix(t, "REPO") || strings.HasPrefix(t, "repo")) && strings.Contains(t, "mas-bandwidth/schema") {
			isSchema = true
			rr2 = "schema"
		}
	}
	base := defaultBase
	if cb := findCardBase(in.CardDir, in.CardsDir, label); cb != "" {
		base = cb
		rr2 = "card"
	}
	if isSchema {
		base = "main"
		mm3 := resolveTargetRemote(repoDir, in.MirrorDir, "schema")
		if _, err := runner.Run(repoDir, "fetch", "-q", mm3, "refs/heads/fixed-table-form"); err == nil {
			cfOut, _ := runner.Run(repoDir, "rev-list", "--count", "FETCH_HEAD..HEAD")
			runner.Run(repoDir, "fetch", "-q", mm3, "refs/heads/main")
			cmOut, _ := runner.Run(repoDir, "rev-list", "--count", "FETCH_HEAD..HEAD")
			cf, cfErr := strconv.Atoi(strings.TrimSpace(cfOut))
			cm, cmErr := strconv.Atoi(strings.TrimSpace(cmOut))
			if cfErr == nil && cmErr == nil && cf < cm {
				base = "fixed-table-form"
			}
		}
	}
	if rb := resultToken(resLines, "BASE "); rb != "" {
		base = rb
	} else if rb := resultToken(resLines, "BASE:"); rb != "" {
		base = rb
	}

	if !isReportOrRead {
		if s, _ := runner.Run(repoDir, "status", "--short"); strings.TrimSpace(s) == "" {
			mm2 := resolveTargetRemote(repoDir, in.MirrorDir, rr2)
			if !branchExistsInRemote(repoDir, mm2, declaredBranch, runner) {
				if _, err := runner.Run(repoDir, "fetch", "-q", mm2, "refs/heads/"+base); err == nil {
					if _, err := runner.Run(repoDir, "merge-base", "--is-ancestor", "FETCH_HEAD", "HEAD"); err != nil {
						// target moved!
						if _, err := runner.Run(repoDir, "-c", "user.name="+user, "-c", "user.email="+email, "rebase", "-q", "FETCH_HEAD"); err == nil {
							shortFetch, _ := runner.Run(repoDir, "rev-parse", "--short", "FETCH_HEAD")
							emit(fmt.Sprintf("REBASED %s onto %s@%s (harvest 2026-09-21: base moved under the card; stale-base refusals)", label, base, strings.TrimSpace(shortFetch)))
						} else {
							runner.Run(repoDir, "rebase", "--abort")
							emit(fmt.Sprintf("REBASE-CONFLICT %s onto %s (author work: recut)", label, base))
						}
					}
				}
			}
		}
	}

	// Fold REPORT.md into RESULT.md
	rptPath := findReportFile(in.JobDir, in.CardDir)
	if rptPath != "" {
		if rptBytes, err := os.ReadFile(rptPath); err == nil {
			curRes, _ := os.ReadFile(rPath)
			curStr := string(curRes)
			if !strings.Contains(curStr, "--- REPORT") && !containsClaimLine(curStr) {
				if len(rptBytes) > MaxReportFoldBytes {
					rptBytes = rptBytes[:MaxReportFoldBytes]
				}
				folded := strings.TrimRight(curStr, "\n") + "\n\n--- REPORT (the card, at most 1500 bytes) ---\n" + string(rptBytes) + "\n"
				if err := os.WriteFile(rPath, []byte(folded), 0o644); err != nil {
					return lines, fmt.Errorf("writing folded report to RESULT.md for %s: %w", label, err)
				}
				emit(fmt.Sprintf("REPORT-FOLDED %s", label))
			}
		}
	}

	// Write prior line for recut-* cards
	if strings.HasPrefix(label, "recut-") {
		if m := reRecutLabel.FindStringSubmatch(label); len(m) >= 3 {
			recutRepo := m[1]
			prNum := m[2]
			curRes, _ := os.ReadFile(rPath)
			curStr := string(curRes)
			if !containsPriorLine(curStr) {
				ph := resolvePRHead(repoDir, in.MirrorDir, recutRepo, prNum, runner)
				curStr = strings.TrimRight(curStr, "\n") + fmt.Sprintf("\nprior: #%s @ %s\n", prNum, ph)
				if err := os.WriteFile(rPath, []byte(curStr), 0o644); err != nil {
					return lines, fmt.Errorf("writing prior line to RESULT.md for %s: %w", label, err)
				}
				emit(fmt.Sprintf("PRIOR-LINE %s #%s @ %s", label, prNum, ph))
			}
		}
	}

	// Write BASE line into RESULT.md
	curRes, _ := os.ReadFile(rPath)
	curStr := string(curRes)
	if (rr2 == "card" || rr2 == "schema" || isSchema) && base != "" {
		updated, changed, isNew := updateBaseLine(curStr, base)
		if changed {
			if err := os.WriteFile(rPath, []byte(updated), 0o644); err != nil {
				return lines, fmt.Errorf("writing BASE line to RESULT.md for %s: %w", label, err)
			}
			curStr = updated
			if isNew {
				emit(fmt.Sprintf("BASE-LINE %s %s", label, base))
			} else {
				emit(fmt.Sprintf("BASE-LINE %s -> %s (the branch the card sits on)", label, base))
			}
		}
	} else if isSchema && !hasBaseLine(curStr) {
		curStr = strings.TrimRight(curStr, "\n") + "\nBASE main\n"
		if err := os.WriteFile(rPath, []byte(curStr), 0o644); err != nil {
			return lines, fmt.Errorf("writing BASE main to RESULT.md for %s: %w", label, err)
		}
		emit(fmt.Sprintf("BASE-LINE %s main", label))
	}

	// Write BRANCH line into RESULT.md
	updated, changed, isNew := updateBranchLine(curStr, declaredBranch)
	if changed {
		if err := os.WriteFile(rPath, []byte(updated), 0o644); err != nil {
			return lines, fmt.Errorf("writing BRANCH line to RESULT.md for %s: %w", label, err)
		}
		if isNew {
			emit(fmt.Sprintf("BRANCH-LINE %s %s", label, declaredBranch))
		} else {
			emit(fmt.Sprintf("BRANCH-LINE %s -> %s", label, declaredBranch))
		}
	}

	if committedVerdict != "" {
		emit(committedVerdict)
	}

	return lines, nil
}

func isGitRepo(dir string, runner GitRunner) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	if out, err := runner.Run(dir, "rev-parse", "--git-dir"); err == nil && len(strings.TrimSpace(out)) > 0 {
		return true
	}
	return false
}

func parsePorcelainPath(line string) string {
	if len(line) < 4 {
		return ""
	}
	rawPath := strings.TrimSpace(line[3:])
	if idx := strings.Index(rawPath, " -> "); idx != -1 {
		rawPath = strings.TrimSpace(rawPath[idx+4:])
	}
	if strings.HasPrefix(rawPath, "\"") && strings.HasSuffix(rawPath, "\"") {
		if unquoted, err := strconv.Unquote(rawPath); err == nil {
			rawPath = unquoted
		}
	}
	return rawPath
}

func findBigFile(repoDir, porcelain string, maxSize int64) string {
	for _, l := range strings.Split(porcelain, "\n") {
		f := parsePorcelainPath(l)
		if f == "" {
			continue
		}
		p := filepath.Join(repoDir, f)
		fi, err := os.Stat(p)
		if err == nil && !fi.IsDir() && fi.Size() > maxSize {
			return f
		}
	}
	return ""
}

func moveScratchFiles(jobDir, repoDir string) []string {
	var moved []string
	scratchDir := filepath.Join(jobDir, "scratch-from-repo")
	for _, name := range CardScratchFiles {
		p := filepath.Join(repoDir, name)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		if err := os.MkdirAll(scratchDir, 0o755); err != nil {
			continue
		}
		dest := filepath.Join(scratchDir, name)
		if err := os.Rename(p, dest); err == nil {
			moved = append(moved, name)
		}
	}
	return moved
}

func extractBaseSHA(line string) string {
	m := reBaseSHA.FindStringSubmatch(line)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func findReportFile(jobDir, cardDir string) string {
	candidates := []string{
		filepath.Join(jobDir, "REPORT.md"),
	}
	if cardDir != "" {
		candidates = append(candidates, filepath.Join(cardDir, "REPORT.md"))
	}
	candidates = append(candidates,
		filepath.Join(jobDir, "..", "REPORT.md"),
		filepath.Join(jobDir, "..", "..", "REPORT.md"),
	)
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func findCardBase(cardDir, cardsDir, label string) string {
	var candidates []string
	if cardDir != "" {
		candidates = append(candidates,
			filepath.Join(cardDir, "card-00-"+label+".md"),
			filepath.Join(cardDir, "card-"+label+".md"),
			filepath.Join(cardDir, label+".md"),
		)
	}
	if cardsDir != "" {
		candidates = append(candidates,
			filepath.Join(cardsDir, "card-00-"+label+".md"),
			filepath.Join(cardsDir, "card-"+label+".md"),
			filepath.Join(cardsDir, label+".md"),
		)
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(raw), "\n") {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "BASE:") {
				return strings.TrimSpace(strings.TrimPrefix(t, "BASE:"))
			}
			if strings.HasPrefix(t, "BASE ") {
				return strings.TrimSpace(strings.TrimPrefix(t, "BASE"))
			}
		}
	}
	return ""
}

func findCardFile(cardDir, cardsDir, jobDir, label string) string {
	var candidates []string
	if cardDir != "" {
		candidates = append(candidates,
			filepath.Join(cardDir, "card-00-"+label+".md"),
			filepath.Join(cardDir, "card-"+label+".md"),
			filepath.Join(cardDir, label+".md"),
		)
	}
	if cardsDir != "" {
		candidates = append(candidates,
			filepath.Join(cardsDir, "card-00-"+label+".md"),
			filepath.Join(cardsDir, "card-"+label+".md"),
			filepath.Join(cardsDir, label+".md"),
		)
	}
	if jobDir != "" {
		candidates = append(candidates,
			filepath.Join(jobDir, "card.md"),
			filepath.Join(jobDir, "card-00-"+label+".md"),
			filepath.Join(jobDir, "card-"+label+".md"),
			filepath.Join(jobDir, label+".md"),
			filepath.Join(jobDir, "..", "card.md"),
		)
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func isScratchFile(p string) bool {
	base := filepath.Base(p)
	for _, sf := range CardScratchFiles {
		if base == sf {
			return true
		}
	}
	return false
}

func resolveTargetRemote(repoDir, mirrorDir, repoName string) string {
	if mirrorDir != "" {
		mPath := filepath.Join(mirrorDir, repoName+".git")
		if fi, err := os.Stat(mPath); err == nil && fi.IsDir() {
			return mPath
		}
	}
	return "origin"
}

func branchExistsInRemote(repoDir, remote, branch string, runner GitRunner) bool {
	_, err := runner.Run(remote, "rev-parse", "-q", "--verify", "refs/heads/"+branch)
	return err == nil
}

func resolvePRHead(repoDir, mirrorDir, repoName, prNum string, runner GitRunner) string {
	remote := resolveTargetRemote(repoDir, mirrorDir, repoName)
	out, err := runner.Run(remote, "rev-parse", "--short=12", "refs/pull/"+prNum+"/head")
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	out, err = runner.Run(repoDir, "rev-parse", "--short=12", "refs/pull/"+prNum+"/head")
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	return "unknown"
}

func containsClaimLine(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		if reClaimLine.MatchString(strings.TrimSpace(l)) {
			return true
		}
	}
	return false
}

func containsPriorLine(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		if rePriorLine.MatchString(strings.TrimSpace(l)) {
			return true
		}
	}
	return false
}

func hasBaseLine(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "BASE ") || strings.HasPrefix(t, "BASE:") {
			return true
		}
	}
	return false
}

func updateBaseLine(content, base string) (updated string, changed bool, isNew bool) {
	lines := strings.Split(content, "\n")
	found := false
	exact := "BASE " + base
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "BASE ") || strings.HasPrefix(t, "BASE:") {
			found = true
			if t == exact {
				return content, false, false
			}
			lines[i] = exact
			return strings.Join(lines, "\n"), true, false
		}
	}
	if !found {
		trimmed := strings.TrimRight(content, "\n")
		return trimmed + "\n" + exact + "\n", true, true
	}
	return content, false, false
}

func updateBranchLine(content, branch string) (updated string, changed bool, isNew bool) {
	lines := strings.Split(content, "\n")
	found := false
	exact := "BRANCH " + branch
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "BRANCH ") || strings.HasPrefix(t, "BRANCH:") {
			found = true
			if t == exact {
				return content, false, false
			}
			lines[i] = exact
			return strings.Join(lines, "\n"), true, false
		}
	}
	if !found {
		trimmed := strings.TrimRight(content, "\n")
		return trimmed + "\n" + exact + "\n", true, true
	}
	return content, false, false
}
