package member

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardtree"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// branchRE is a branch a step commit is resolved on: a ref name with no
// refspec character, so the name can only be itself.
var branchRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

func branchOK(branch string) bool {
	if !branchRE.MatchString(branch) {
		return false
	}
	return !strings.Contains(branch, "..") && !strings.HasSuffix(branch, "/") && !strings.HasSuffix(branch, ".lock") && !strings.Contains(branch, "@{")
}

func isStepHex(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// acceptFullSha is the resolver when this member has no clone to look in
// (docs/SPEC-SPRINT.md, the verdict per step): a full sha is its own, and a
// shorter prefix is on no commit of the branch, never stored unexpanded.
func acceptFullSha(branch string) cardtree.ShaResolve {
	if branch == "" {
		branch = "the branch"
	}
	return func(prefix string) (string, error) {
		if len(prefix) == 40 {
			return prefix, nil
		}
		return "", cardtree.UnknownCommit(prefix, branch)
	}
}

// stepResolve is the resolver a card's finish uses. A test's ResolveStep
// stands in for git. Otherwise the clone is StepClone, or the launch checkout
// the member verb's arguments name.
func (m *Member) stepResolve(p Packet) cardtree.ShaResolve {
	branch := p.Branch
	if branch == "" {
		branch = "the branch"
	}
	if m.cfg.ResolveStep != nil {
		return func(prefix string) (string, error) {
			return m.cfg.ResolveStep(branch, prefix)
		}
	}
	dir := ""
	if m.cfg.StepClone != nil {
		dir = m.cfg.StepClone(p)
	} else {
		dir = cloneDir(p, os.Args)
	}
	if dir == "" {
		return acceptFullSha(branch)
	}
	return func(prefix string) (string, error) {
		return ResolveStepCommit(context.Background(), dir, branch, prefix)
	}
}

// cloneDir is the launch checkout <slots>/<card>.g<gen>.e<epoch>/jobs/<card>/repo
// (a read: .a<attempt>), when args are the member verb's and that checkout
// exists. args without the member subcommand name nothing, so a test process
// does not resolve against a checkout it did not stage.
func cloneDir(p Packet, args []string) string {
	slots := slotsFromArgs(args)
	if slots == "" || !safepath.NameOK(p.Card) {
		return ""
	}
	dir := filepath.Join(slots, launchDir(p), "jobs", p.Card, "repo")
	// a checkout is a repository directory; a .git file is a worktree the push also refuses
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}

func launchDir(p Packet) string {
	e := ".e" + strconv.FormatUint(p.Epoch, 10)
	if p.Kind == "read" {
		return p.Card + ".a" + strconv.Itoa(p.Attempt) + e
	}
	return p.Card + ".g" + strconv.Itoa(p.Gen) + e
}

func slotsFromArgs(args []string) string {
	member := false
	var root, slots string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "member" {
			member = true
		}
		switch {
		case a == "--slots" && i+1 < len(args):
			i++
			slots = args[i]
		case strings.HasPrefix(a, "--slots="):
			slots = strings.TrimPrefix(a, "--slots=")
		case a == "--root" && i+1 < len(args):
			i++
			root = args[i]
		case strings.HasPrefix(a, "--root="):
			root = strings.TrimPrefix(a, "--root=")
		}
	}
	if !member {
		return ""
	}
	if slots == "" && root != "" {
		slots = filepath.Join(root, "slots")
	}
	return expandHome(slots)
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

type gitCall func(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error)

// ResolveStepCommit resolves prefix, 7 to 40 lowercase hex, to the one full
// sha of branch in dir (docs/SPEC-SPRINT.md, the verdict per step). The
// commits are git rev-list of that branch. Two that share the prefix, or a
// prefix git calls ambiguous, are refused as ambiguous; none is unknown. A
// full sha is checked the same way. The walk is local: a fetch would run
// under the checkout's own config, which the child wrote.
func ResolveStepCommit(ctx context.Context, dir, branch, prefix string) (string, error) {
	return resolveStepCommit(ctx, dir, branch, prefix, runGit)
}

func resolveStepCommit(ctx context.Context, dir, branch, prefix string, run gitCall) (string, error) {
	if !isStepHex(prefix) {
		return "", fmt.Errorf("the step line's commit %s is no sha (7 to 40 hex, or -)", prefix)
	}
	if !branchOK(branch) {
		return "", cardtree.UnknownCommit(prefix, "the branch")
	}
	stdout, stderr, err := run(ctx, dir, gitPlain("rev-list", "--end-of-options", branch)...)
	switch gitKind(err, stderr) {
	case "fail":
		return "", fmt.Errorf("the step line's commit %s could not be resolved on %s: %s", prefix, branch, gitLine(stderr, err))
	case "ambiguous":
		return "", cardtree.AmbiguousCommit(prefix, branch)
	case "no":
		return "", cardtree.UnknownCommit(prefix, branch)
	}
	hits := shasWithPrefix(stdout, prefix)
	switch {
	case len(hits) == 0:
		return "", cardtree.UnknownCommit(prefix, branch)
	case len(hits) > 1:
		return "", cardtree.AmbiguousCommit(prefix, branch)
	}
	if gitSaysAmbiguous(ctx, dir, prefix, run) {
		return "", cardtree.AmbiguousCommit(prefix, branch)
	}
	return hits[0], nil
}

// gitSaysAmbiguous asks git rev-parse whether the prefix names one object.
// --quiet drops the ambiguous line, so a refusal is read again without it.
func gitSaysAmbiguous(ctx context.Context, dir, prefix string, run gitCall) bool {
	spec := prefix + "^{commit}"
	_, stderr, err := run(ctx, dir, gitPlain("rev-parse", "--verify", "--quiet", "--end-of-options", spec)...)
	if err == nil {
		return false
	}
	if strings.Contains(strings.ToLower(stderr), "is ambiguous") {
		return true
	}
	_, stderr, _ = run(ctx, dir, gitPlain("rev-parse", "--verify", "--end-of-options", spec)...)
	return strings.Contains(strings.ToLower(stderr), "is ambiguous")
}

func gitPlain(args ...string) []string {
	// the checkout's config is the child's: drop the alias and the hook and
	// fsmonitor keys, which are the ones that run a command
	head := []string{
		"-c", "alias.rev-list=", "-c", "alias.rev-parse=", "-c", "core.pager=cat",
		"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
	}
	return append(head, args...)
}

func gitKind(err error, stderr string) string {
	if err == nil {
		return "ok"
	}
	low := strings.ToLower(stderr)
	// "short object ID <prefix> is ambiguous". A missing ref is
	// "fatal: ambiguous argument", which is not that.
	if strings.Contains(low, "is ambiguous") {
		return "ambiguous"
	}
	var to *subproc.TimeoutError
	if errors.As(err, &to) {
		return "fail"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) || strings.Contains(low, "fatal:") {
		return "no"
	}
	return "fail"
}

func gitLine(stderr string, err error) string {
	msg := strings.TrimSpace(stderr)
	if err != nil {
		if msg != "" {
			msg += ": "
		}
		msg += err.Error()
	}
	return oneLine(msg)
}

func shasWithPrefix(out, prefix string) []string {
	var hits []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		sha := strings.ToLower(strings.TrimSpace(line))
		if !isStepHex(sha) || len(sha) != 40 || !strings.HasPrefix(sha, prefix) || seen[sha] {
			continue
		}
		seen[sha] = true
		hits = append(hits, sha)
	}
	return hits
}

func runGit(ctx context.Context, dir string, args ...string) (string, string, error) {
	env := append(gitrun.WithoutRepoVars(os.Environ()),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
	)
	res, err := gitrun.Run(ctx, gitrun.Options{C: dir, OwnRepo: true, Env: env}, args...)
	return string(res.Stdout), string(res.Stderr), err
}
