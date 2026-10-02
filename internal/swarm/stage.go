package swarm

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultStageTimeout is the hard timeout for card staging (120 s).
const DefaultStageTimeout = 120 * time.Second

// stageFetchRetryDelay is the pause before the one retry of the fetch of a head the stage lacks.
const stageFetchRetryDelay = 2 * time.Second

// ErrStageTimeout is returned when card staging exceeds the hard timeout.
var ErrStageTimeout = errors.New("stage-timeout")

// CardBase is what a card's header says to stage into <job>/repo (nova-tools#3711).
type CardBase struct {
	Repo  string   // the clone URL (or a local path) to stage; "" when the card names none that can be read
	Sha   string   // base-sha: (or the sha of BASE: <ref>@<sha40>); "" when absent
	Ref   string   // BASE: <ref> without @; the ref checked out when there is no sha
	Named string   // the raw value of the card's base-repo: or REPO: line, even one Repo could not be read from
	Stage []string // the recipe files the header's `Stage:` lines name, in order (docs/SPEC-CARD-CONTRACT.md)
}

// headerLineRE is a card header line: a key, a colon, then a value or nothing.
var headerLineRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*:(\s|$)`)

// cardHeader is a card's header: line 1, then every line after it while it is a
// `key: value` line; the first blank line or line of prose ends it.
func cardHeader(card string) []string {
	lines := strings.Split(card, "\n")
	n := 1
	for n < len(lines) && headerLineRE.MatchString(strings.TrimSpace(lines[n])) {
		n++
	}
	return lines[:min(n, len(lines))]
}

// cardRepoNameRE is the owner/name a REPO: line carries.
var cardRepoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// cardLabelRE is the card id line 1 carries (RESULT: <label> sha=<sha12>).
var cardLabelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// defaultProbeBase is github's web host, the host a card's repository names live under.
const defaultProbeBase = "https://github.com"

// CardRepoURL is the clone URL a REPO: value names: a URL or a local path as written,
// an owner/name as the forge's https URL ending .git (the shape base-repo: lines and the
// URL fallback carry, so FindBenchMirror resolves all three to ~/nova-bench/mirror/<name>.git).
// "" when the value is none of those.
func CardRepoURL(value string) string {
	v := strings.TrimSpace(value)
	switch {
	case v == "" || v == "-" || strings.EqualFold(v, "none"):
		return ""
	case isRemoteRepo(v) || strings.HasPrefix(v, "/") || strings.Contains(v, "://"):
		return v
	case cardRepoNameRE.MatchString(v):
		return defaultProbeBase + "/" + strings.TrimSuffix(v, ".git") + ".git"
	}
	return ""
}

// ReadCardBase is THE reader of which repository a card works in, at which sha: staging
// (StageCard) and the member's frame call it, so the repo a card is staged from is the repo it
// names. It reads the card's HEADER only (docs/SPEC-CARD-CONTRACT.md: the frame is the
// card's data, never its prose): line 1 and the `key: value` lines that follow it, up to the
// first line of another form; a line in the body that looks like a header names nothing.
// Precedence: `base-repo: <url>`, then `REPO: <owner>/<name>` (the header every pushed card
// carries). The sha is `base-sha:`, else the sha of `BASE: <ref>@<sha40>`; the ref is BASE:'s
// value before any @.
func ReadCardBase(card []byte) CardBase {
	lines := cardHeader(string(card))
	var b CardBase
	var baseRepo, repoLine string
	var sawRepoLine bool
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "base-repo:"):
			baseRepo = strings.TrimSpace(strings.TrimPrefix(trimmed, "base-repo:"))
		case strings.HasPrefix(trimmed, "Stage:"):
			b.Stage = append(b.Stage, strings.Fields(strings.TrimPrefix(trimmed, "Stage:"))...)
		case strings.HasPrefix(trimmed, "base-sha:"):
			b.Sha = strings.TrimSpace(strings.TrimPrefix(trimmed, "base-sha:"))
		case strings.HasPrefix(trimmed, "REPO:") && !sawRepoLine:
			sawRepoLine = true
			repoLine = strings.TrimSpace(strings.TrimPrefix(trimmed, "REPO:"))
		case strings.HasPrefix(trimmed, "BASE:") && b.Ref == "":
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "BASE:"))
			ref, sha, hasAt := strings.Cut(val, "@")
			b.Ref = strings.TrimSpace(ref)
			if hasAt && b.Sha == "" {
				if s := strings.TrimSpace(sha); len(s) >= 40 {
					b.Sha = s[:40]
				}
			}
		}
	}
	switch {
	case baseRepo != "":
		b.Repo, b.Named = baseRepo, baseRepo
	case CardRepoURL(repoLine) != "":
		b.Repo, b.Named = CardRepoURL(repoLine), repoLine
	default:
		if repoLine != "-" && !strings.EqualFold(repoLine, "none") {
			// A REPO: line no reader can resolve still NAMES a repo: staging must refuse
			// it (no-repo-staged), never launch the model into an empty job dir.
			b.Named = repoLine
		}
	}
	return b
}

// ParseCardBase extracts the base repo and base sha from the card text (ReadCardBase).
// ok is false when the card names no repo that can be staged.
func ParseCardBase(card []byte) (baseRepo, baseSha string, ok bool) {
	b := ReadCardBase(card)
	if b.Repo == "" {
		return "", "", false
	}
	return b.Repo, b.Sha, true
}

// CardNamesRepo reports whether the card names a repository at all, readable or not: such
// a card that ends with nothing staged is a staging failure (STAGE FAIL reason=no-repo-staged).
func CardNamesRepo(card []byte) bool {
	return ReadCardBase(card).Named != ""
}

// CardStageBranch is the branch the staged checkout is on, so the card commits on a named
// branch rather than a detached HEAD: rowan/<label> from line 1 (RESULT: <label> sha=...),
// rowan/card when line 1 names no label.
func CardStageBranch(card []byte) string {
	first, _, _ := strings.Cut(string(card), "\n")
	first = strings.TrimSpace(first)
	first = strings.TrimPrefix(first, "RESULT:")
	first = strings.TrimPrefix(first, "RESULT")
	if f := strings.Fields(first); len(f) > 0 && cardLabelRE.MatchString(f[0]) {
		return "rowan/" + f[0]
	}
	return "rowan/card"
}

// FindBenchMirror finds the path to the bench's local mirror for baseRepo.
// Candidate locations:
// - baseRepo itself, if it is a directory on disk that exists
// - $HOME/nova-bench/mirror/<repo>.git
// - $HOME/nova-bench/mirror/<repo>
// - /tmp/<repo>-mirror.git
// - /tmp/<repo>.git
func FindBenchMirror(benchHome, baseRepo string) string {
	if baseRepo == "" {
		return ""
	}
	if fi, err := os.Stat(baseRepo); err == nil && fi.IsDir() {
		if isGitDir(baseRepo) {
			return baseRepo
		}
	}
	if benchHome == "" {
		benchHome = os.Getenv("HOME")
		if benchHome == "" {
			benchHome, _ = os.UserHomeDir()
		}
	}
	repoName := filepath.Base(baseRepo)
	repoName = strings.TrimSuffix(repoName, ".git")
	repoName = strings.TrimSuffix(repoName, "-mirror")

	var candidates []string
	if benchHome != "" {
		candidates = append(candidates,
			filepath.Join(benchHome, "nova-bench", "mirror", repoName+".git"),
			filepath.Join(benchHome, "nova-bench", "mirror", repoName),
		)
	}
	candidates = append(candidates,
		filepath.Join("/tmp", repoName+"-mirror.git"),
		filepath.Join("/tmp", repoName+".git"),
	)
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			if isGitDir(c) {
				return c
			}
		}
	}
	return ""
}

// MirrorCloneArgs is the git argv of the one staging convention, for cards (StageCard) and
// for ci run: a local clone of the bench mirror that borrows its
// objects and dissociates, so staging never reads the network and a later gc of the
// mirror cannot take objects from under the clone. noCheckout leaves the worktree empty
// for a caller that checks out an exact sha next. The mirror and the target follow `--`:
// a card names the repository, and a name that starts with `-` is an operand, never an option.
func MirrorCloneArgs(mirror, target string, noCheckout bool) []string {
	args := []string{"clone", "-q"}
	if noCheckout {
		args = append(args, "--no-checkout")
	}
	return append(args, "--reference", mirror, "--dissociate", "--", mirror, target)
}

// refuseOptionLike is the refusal of a card value git would read as an option. Every git
// call below also puts its card-derived operands behind `--` or `--end-of-options`
// (internal/ci TestCardDerivedGitOperandsFollowTheSeparator holds that; the branch is
// switched to with `git switch -C`, because `git checkout` 2.43 reads a rev after
// `--end-of-options` as a path), so a value that starts with `-` is never a
// flag; no repository, sha or ref a card can mean starts with one, so staging refuses it
// by name instead of handing git a string it cannot use.
func refuseOptionLike(what, value string) error {
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("staging refused: %s %q starts with '-'; a repository, sha or ref never does", what, value)
	}
	return nil
}

func isGitDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	return false
}

func isRemoteRepo(repo string) bool {
	return strings.HasPrefix(repo, "https://") ||
		strings.HasPrefix(repo, "http://") ||
		strings.HasPrefix(repo, "git@") ||
		strings.HasPrefix(repo, "ssh://")
}

// WriteStageTimeoutResult writes the RESULT.md for a stage timeout:
// RESULT: BLOCKED stage-timeout <bench> <secs>
func WriteStageTimeoutResult(jobDir, bench string, secs int) (string, error) {
	if jobDir == "" {
		return "", nil
	}
	dest := filepath.Join(jobDir, "RESULT.md")
	body := fmt.Sprintf("RESULT: BLOCKED stage-timeout %s %d\nblocked: staging timed out after %ds\nwritten-by: nova-swarm native (the card published no report of its own)\n",
		bench, secs, secs)
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// stageWaitDelay bounds how long a staging git call waits for its pipes after the deadline
// kill. git clone runs helpers (git-remote-https, index-pack) that inherit the output pipe;
// killing git alone left them holding it, which is how hulk had 193 clones stuck 43-65
// minutes (#2882). Each call leads its own process group, the deadline kills the group, and
// WaitDelay closes the pipes a bounded time after that, so the 120 s timeout is hard.
const stageWaitDelay = 2 * time.Second

// stageGit builds one staging git call under ctx: its own process group, killed as a group
// when ctx ends, no terminal prompt.
func stageGit(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	ownGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			KillGroup(cmd.Process.Pid, "")
		}
		return nil
	}
	cmd.WaitDelay = stageWaitDelay
	return cmd
}

// stageTimedOut reports whether a staging call ended because the deadline passed.
func stageTimedOut(ctx context.Context, err error) bool {
	return ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, exec.ErrWaitDelay)
}

// NoStageIdentity is StageCard's refusal of a stage given no commit identity: the setting
// it wants and where a native run takes it from.
const NoStageIdentity = "staging refused: no commit identity: the staged checkout commits under the pool identity, and none was given; give nova-swarm native (or the member loop's nova-config argv) --identity <owner>,<name>,<email>, or write the pool's <root>/identity.tsv"

// StageOptions describes a card staging request.
type StageOptions struct {
	Card      []byte
	TargetDir string // e.g. <jobDir>/repo
	JobDir    string // <jobDir>, where RESULT.md is written on timeout
	BenchHome string
	BenchName string
	Timeout   time.Duration
	// Base and Branch, when set, are the frame's (internal/cardcontract): the repository,
	// ref and sha the member's packet names and the branch it pushes, staged in place of
	// what the card's header lines say (docs/SPEC-CARD-CONTRACT.md layer 2).
	Base   *CardBase
	Branch string
	// Identity is the name and email the staged checkout's local git config carries, the
	// caller's (native's pool identity: --identity, else <root>/identity.tsv). A stage
	// given no name or no email is refused before any git runs: this tool has no identity
	// of its own to fall back to.
	Identity StagingIdentity

	// git, when set, builds every staging git call in place of stageGit: a test's seam for
	// a step git itself would not fail.
	git func(ctx context.Context, args ...string) *exec.Cmd

	// fetchRetryDelay, when set, is the pause before the one retry of a head fetch, in place
	// of stageFetchRetryDelay: a test's seam for the clock.
	fetchRetryDelay time.Duration
}

// StageResult is the outcome of a staging operation.
type StageResult struct {
	BaseRepo string
	BaseSha  string // the card's base sha; once staged, the full sha <job>/repo's HEAD is at
	Ref      string // the card's BASE: ref, checked out when it names no sha
	Branch   string // the branch the staged checkout is on (CardStageBranch)
	Mirror   string
	Staged   bool
	TimedOut bool
	Wall     time.Duration
	// Clone, Fetch and Checkout sum their Git command times; Clone includes
	// the initial checkout, and Fetch excludes probes and the retry wait.
	Clone, Fetch, Checkout time.Duration
}

// StageCard stages the repository for a card into TargetDir using the bench mirror.
// The repo, sha and ref are ReadCardBase's (base-repo:, REPO:, or a clone URL; base-sha:
// or BASE:). If the card names no repo that can be read, staging is skipped and Staged is
// false; the caller refuses such a card when CardNamesRepo says it named one (#3711).
// The checkout is at the sha (else the ref, else the clone's default head) on the branch
// CardStageBranch names, so the card commits on a branch, not a detached HEAD.
// If base-repo is remote and no bench mirror is found, staging fails without contacting GitHub.
// Staging runs git clone --reference <mirror> and git fetch/checkout <base-sha> with a hard timeout (default 120s).
// If the timeout expires, it writes RESULT.md:
//
//	RESULT: BLOCKED stage-timeout <bench> <secs>
//
// and returns ErrStageTimeout.
func StageCard(opts StageOptions) (StageResult, error) {
	if len(opts.Card) == 0 && opts.Base == nil {
		return StageResult{}, nil
	}
	cb := ReadCardBase(opts.Card)
	if opts.Base != nil {
		cb = *opts.Base
	}
	baseRepo, baseSha := cb.Repo, cb.Sha
	if baseRepo == "" {
		return StageResult{}, nil
	}
	if isGitDir(opts.TargetDir) {
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Staged: true}, nil
	}
	for _, v := range []struct{ what, value string }{
		{"base-repo", baseRepo}, {"base-sha", baseSha}, {"BASE ref", cb.Ref},
	} {
		if err := refuseOptionLike(v.what, v.value); err != nil {
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref}, err
		}
	}

	bench := opts.BenchName
	if bench == "" {
		if h, err := os.Hostname(); err == nil {
			if idx := strings.Index(h, "."); idx != -1 {
				h = h[:idx]
			}
			bench = h
		}
	}
	if bench == "" {
		bench = "bench"
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultStageTimeout
	}
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = 1
	}

	mirror := FindBenchMirror(opts.BenchHome, baseRepo)
	if mirror == "" && isRemoteRepo(baseRepo) {
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref}, fmt.Errorf("staging refused: no bench mirror for %s: a card may not clone directly from github without a bench mirror", baseRepo)
	}

	// The checkout commits under the caller's identity: one that names no one is refused
	// here, before the clone, never filled in from anybody's.
	if strings.TrimSpace(opts.Identity.Name) == "" || strings.TrimSpace(opts.Identity.Email) == "" {
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref, Mirror: mirror}, errors.New(NoStageIdentity)
	}

	cloneSource := mirror
	if cloneSource == "" {
		cloneSource = baseRepo
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	stageCmd := stageGit
	if opts.git != nil {
		stageCmd = opts.git
	}

	start := time.Now()
	var cloneTime, fetchTime, checkoutTime time.Duration
	cloneArgs := []string{"clone", "-q", "--", cloneSource, opts.TargetDir}
	if mirror != "" {
		cloneArgs = MirrorCloneArgs(mirror, opts.TargetDir, false)
	}

	cloneCmd := stageCmd(ctx, cloneArgs...)
	if out, err := stageTimedOutput(cloneCmd, &cloneTime); err != nil {
		if stageTimedOut(ctx, err) {
			// ignored: the timeout is returned as ErrStageTimeout on the next line; the result file is a courtesy for the reader of the job directory
			_, _ = WriteStageTimeoutResult(opts.JobDir, bench, secs)
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, TimedOut: true, Wall: time.Since(start)}, ErrStageTimeout
		}
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, Wall: time.Since(start)}, fmt.Errorf("git clone failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Update remote origin to baseRepo if cloned from mirror
	if cloneSource != baseRepo {
		remCmd := stageCmd(ctx, "-C", opts.TargetDir, "remote", "set-url", "origin", "--", baseRepo)
		if out, err := remCmd.CombinedOutput(); err != nil {
			// A checkout whose origin still names the mirror pushes to the mirror:
			// refuse the stage rather than hand the card that checkout.
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, Wall: time.Since(start)},
				fmt.Errorf("git remote set-url origin %s failed in %s: %s (%w)", baseRepo, opts.TargetDir, strings.TrimSpace(string(out)), err)
		}
	}

	fail := func(what string, out []byte, err error) (StageResult, error) {
		if stageTimedOut(ctx, err) {
			// ignored: the timeout is returned as ErrStageTimeout on the next line; the result file is a courtesy for the reader of the job directory
			_, _ = WriteStageTimeoutResult(opts.JobDir, bench, secs)
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref, Mirror: mirror, TimedOut: true, Wall: time.Since(start)}, ErrStageTimeout
		}
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref, Mirror: mirror, Wall: time.Since(start)}, fmt.Errorf("git %s failed: %s (%w)", what, strings.TrimSpace(string(out)), err)
	}

	// Fetch and checkout baseSha (else the BASE: ref) on the card's branch.
	branch := CardStageBranch(opts.Card)
	if opts.Branch != "" {
		if err := refuseOptionLike("branch", opts.Branch); err != nil {
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref, Mirror: mirror, Wall: time.Since(start)}, err
		}
		branch = opts.Branch
	}
	switch {
	case baseSha != "":
		// RULE (docs/SPEC-CARD-CONTRACT.md, staging): the head is checked out only when its
		// tree is in the stage, the mirror's own refresh no dependency of a stage: else the head
		// is fetched from origin by sha, and fetched once more after stageFetchRetryDelay; a head
		// still absent is refused in one line. The commit alone is no answer: a mirror caught
		// between a commit and its tree hands the clone the commit, and the checkout of a head
		// whose tree is unread fails (or, in git 2.43, reports done with an empty worktree). The
		// fetch is --refetch: a plain fetch of a sha does nothing when a stage ref (a clone's
		// refs/remotes/origin/*) already reaches the commit, whatever its tree.
		haveHead := func() bool {
			return stageCmd(ctx, "-C", opts.TargetDir, "cat-file", "-e", "--end-of-options", baseSha+"^{tree}").Run() == nil
		}
		var fetched []byte
		for try := 0; try < 2 && !haveHead(); try++ {
			if try > 0 {
				select {
				case <-time.After(cmp.Or(opts.fetchRetryDelay, stageFetchRetryDelay)):
				case <-ctx.Done():
				}
			}
			var ferr error
			if fetched, ferr = stageTimedOutput(stageCmd(ctx, "-C", opts.TargetDir, "fetch", "-q", "--refetch", "--", "origin", baseSha), &fetchTime); stageTimedOut(ctx, ferr) {
				return fail("fetch", fetched, ferr)
			}
		}
		if !haveHead() {
			why := strings.Join(strings.Fields(string(fetched)), " ")
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref, Mirror: mirror, Wall: time.Since(start)},
				fmt.Errorf("staging refused: head %s is in neither the mirror %s nor origin %s: %s", baseSha, cmp.Or(mirror, "(none)"), baseRepo, why)
		}
		coCmd := stageCmd(ctx, "-C", opts.TargetDir, "switch", "-q", "-C", branch, "--end-of-options", baseSha)
		if out, cerr := stageTimedOutput(coCmd, &checkoutTime); cerr != nil {
			return fail("checkout", out, cerr)
		}
	case cb.Ref != "":
		// The clone's remote-tracking ref first (the mirror's branch), then the ref as
		// written (a tag or a sha the clone holds).
		coCmd := stageCmd(ctx, "-C", opts.TargetDir, "switch", "-q", "-C", branch, "--end-of-options", "origin/"+cb.Ref)
		if out, cerr := stageTimedOutput(coCmd, &checkoutTime); cerr != nil {
			if stageTimedOut(ctx, cerr) {
				return fail("checkout", out, cerr)
			}
			coCmd = stageCmd(ctx, "-C", opts.TargetDir, "switch", "-q", "-C", branch, "--end-of-options", cb.Ref)
			if out, cerr := stageTimedOutput(coCmd, &checkoutTime); cerr != nil {
				return fail("checkout", out, cerr)
			}
		}
	default:
		coCmd := stageCmd(ctx, "-C", opts.TargetDir, "switch", "-q", "-C", branch)
		if out, cerr := stageTimedOutput(coCmd, &checkoutTime); cerr != nil {
			return fail("checkout", out, cerr)
		}
	}
	headCmd := stageCmd(ctx, "-C", opts.TargetDir, "rev-parse", "HEAD")
	headOut, herr := headCmd.Output()
	if herr != nil {
		return fail("rev-parse", headOut, herr)
	}
	head := strings.TrimSpace(string(headOut))

	// A checkout whose identity could not be set would take the model's commits under no
	// name, found only when it commits: the stage fails here instead, saying which.
	if out, err := stageCmd(ctx, "-C", opts.TargetDir, "config", "--", "user.name", opts.Identity.Name).CombinedOutput(); err != nil {
		return fail("config user.name", out, err)
	}
	if out, err := stageCmd(ctx, "-C", opts.TargetDir, "config", "--", "user.email", opts.Identity.Email).CombinedOutput(); err != nil {
		return fail("config user.email", out, err)
	}

	return StageResult{
		BaseRepo: baseRepo,
		BaseSha:  head,
		Ref:      cb.Ref,
		Branch:   branch,
		Mirror:   mirror,
		Staged:   true,
		Wall:     time.Since(start),
		Clone:    cloneTime,
		Fetch:    fetchTime,
		Checkout: checkoutTime,
	}, nil
}

// stageTimedOutput accumulates the Git command time of a staging phase,
// including failed attempts (docs/SPEC-CARD-CONTRACT.md, staging).
func stageTimedOutput(cmd *exec.Cmd, elapsed *time.Duration) ([]byte, error) {
	started := time.Now()
	out, err := cmd.CombinedOutput()
	*elapsed += time.Since(started)
	return out, err
}
