package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Build is one stream branch build in a scratch clone: a shallow
// single-branch clone of the base, referenced to the bench mirror when there
// is one, the members merged --no-ff oldest first, one batch test, and on red
// a re-merge one member at a time that parks the first member that turns it
// red. Nothing is pushed here; Push does that.
type Build struct {
	Repo        string // owner/name
	Remote      string // clone and push URL
	Mirror      string // --reference-if-able; "" for none
	Base        string
	Branch      string
	Workdir     string // must not exist
	Test        string // bash -c command; "" picks make check or go test ./...
	TestTimeout time.Duration
	Author      string // "Name <email>" for the merge commits
	Log         io.Writer
	// NoTest runs no batch test here: the batch is tested by the CI request
	// a bench claims (nova-tools#3899), so every merged member is kept and
	// nothing is bisected on this seat.
	NoTest bool
	// ParkConflicts parks a member whose batch merge conflicts (Parked, why
	// conflict:<files>) and merges the rest without it, where the default
	// stops the build at it (nova-tools #3898: the land duty never stops a
	// stream for one member; the member's author gets a rebase task).
	ParkConflicts bool
}

// Conflict is a member whose merge conflicted: the build stops there.
type Conflict struct {
	Member Member
	Files  []string
}

// Result is what the build did.
type Result struct {
	BaseSHA  string
	Head     string
	Kept     []Member
	Parked   []Parked
	Moved    []Parked // head on GitHub differs from the record: not merged
	Conflict *Conflict
	BaseRed  bool
	Tests    int
	TestCmd  string
	RedLine  string
}

func (b Build) logf(format string, a ...any) {
	if b.Log != nil {
		fmt.Fprintf(b.Log, format+"\n", a...)
	}
}

func (b Build) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = b.Workdir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Run builds the branch.
func (b Build) Run(ctx context.Context, members []Member) (Result, error) {
	var res Result
	if b.Workdir == "" || !filepath.IsAbs(b.Workdir) {
		return res, fmt.Errorf("workdir %q must be an absolute path", b.Workdir)
	}
	if _, err := os.Stat(b.Workdir); err == nil {
		return res, fmt.Errorf("workdir %s already exists", b.Workdir)
	}
	if err := os.MkdirAll(filepath.Dir(b.Workdir), 0o755); err != nil {
		return res, err
	}
	args := []string{"clone", "-q", "--depth", "50", "--single-branch", "-b", b.Base}
	if b.Mirror != "" {
		args = append(args, "--reference-if-able", b.Mirror)
	}
	args = append(args, b.Remote, b.Workdir)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return res, fmt.Errorf("clone %s: %v: %s", b.Remote, err, strings.TrimSpace(string(out)))
	}
	name, email := "Rowan", "rowan@mas-bandwidth.com"
	if b.Author != "" {
		if i := strings.Index(b.Author, "<"); i > 0 {
			name, email = strings.TrimSpace(b.Author[:i]), strings.Trim(strings.TrimSpace(b.Author[i:]), "<>")
		}
	}
	for _, kv := range [][2]string{{"user.name", name}, {"user.email", email}, {"commit.gpgsign", "false"}} {
		if _, err := b.git(ctx, "config", kv[0], kv[1]); err != nil {
			return res, err
		}
	}
	sha, err := b.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return res, err
	}
	res.BaseSHA = sha
	if _, err := b.git(ctx, "checkout", "-q", "-b", b.Branch); err != nil {
		return res, err
	}
	res.TestCmd = b.testCmd()
	if b.NoTest {
		res.TestCmd = CIBatch
	}

	// Fetch every member's PR head in one call and check it is the head the
	// record (and so the read) names.
	if len(members) > 0 {
		fetch := []string{"fetch", "-q", "--depth", "50", "origin"}
		for _, m := range members {
			fetch = append(fetch, fmt.Sprintf("+refs/pull/%d/head:refs/land/pr/%d", m.N, m.N))
		}
		if _, err := b.git(ctx, fetch...); err != nil {
			return res, err
		}
	}
	var todo []Member
	for _, m := range members {
		got, err := b.git(ctx, "rev-parse", fmt.Sprintf("refs/land/pr/%d", m.N))
		if err != nil {
			return res, err
		}
		if !strings.HasPrefix(got, strings.ToLower(m.Head)) {
			res.Moved = append(res.Moved, Parked{Member: m, Why: "moved:" + short(got)})
			b.logf("SKIP #%d head moved %s -> %s (the read is at the record head)", m.N, short(m.Head), short(got))
			continue
		}
		todo = append(todo, m)
	}
	if err := b.deepen(ctx, todo); err != nil {
		return res, err
	}

	// Batch merge, oldest first. A conflict stops the build (Rowan
	// resolves), or with ParkConflicts parks that member and goes on.
	merged := todo[:0:0]
	for _, m := range todo {
		files, err := b.merge(ctx, m)
		if err != nil {
			return res, err
		}
		if files != nil {
			if !b.ParkConflicts {
				res.Conflict = &Conflict{Member: m, Files: files}
				return res, nil
			}
			res.Parked = append(res.Parked, Parked{Member: m, Why: "conflict:" + strings.Join(files, ",")})
			b.logf("PARKED #%d conflict: %s", m.N, strings.Join(files, ","))
			continue
		}
		merged = append(merged, m)
		b.logf("MERGED #%d at %s", m.N, short(m.Head))
	}
	todo = merged
	if len(todo) == 0 {
		res.Head = res.BaseSHA
		return res, nil
	}
	if b.NoTest {
		b.logf("TEST ci batch=%d: the batch test is the CI request a bench claims, none runs here", len(todo))
		res.Kept = todo
		res.Head, err = b.git(ctx, "rev-parse", "HEAD")
		return res, err
	}
	res.Tests++
	green, line, err := b.test(ctx)
	if err != nil {
		return res, err
	}
	if green {
		b.logf("TEST green batch=%d", len(todo))
		res.Kept = todo
		res.Head, err = b.git(ctx, "rev-parse", "HEAD")
		return res, err
	}
	res.RedLine = line
	b.logf("TEST red batch=%d: %s; bisecting one member at a time", len(todo), line)

	// Red: the base alone first (a red base is Rowan's to fix on the branch),
	// then each member on top of the green ones kept so far.
	if _, err := b.git(ctx, "reset", "-q", "--hard", res.BaseSHA); err != nil {
		return res, err
	}
	res.Tests++
	green, line, err = b.test(ctx)
	if err != nil {
		return res, err
	}
	if !green {
		res.BaseRed = true
		res.RedLine = line
		b.logf("TEST red on the base alone: %s", line)
		return res, nil
	}
	for _, m := range todo {
		files, err := b.merge(ctx, m)
		if err != nil {
			return res, err
		}
		if files != nil {
			// A member that merged in the batch conflicts without a parked
			// one before it: park it as a conflict, it needs a rebase.
			res.Parked = append(res.Parked, Parked{Member: m, Why: "conflict:" + strings.Join(files, ",")})
			continue
		}
		res.Tests++
		green, line, err := b.test(ctx)
		if err != nil {
			return res, err
		}
		if green {
			res.Kept = append(res.Kept, m)
			continue
		}
		if _, err := b.git(ctx, "reset", "-q", "--hard", "HEAD~1"); err != nil {
			return res, err
		}
		res.Parked = append(res.Parked, Parked{Member: m, Why: "red:" + redWord(line)})
		b.logf("PARKED #%d red: %s", m.N, line)
	}
	res.Head, err = b.git(ctx, "rev-parse", "HEAD")
	return res, err
}

// deepen fetches more history once when a member has no merge base with the
// shallow base (a member cut far behind the tip). Never an unshallow.
func (b Build) deepen(ctx context.Context, members []Member) error {
	for _, m := range members {
		if _, err := b.git(ctx, "merge-base", "HEAD", fmt.Sprintf("refs/land/pr/%d", m.N)); err != nil {
			b.logf("DEEPEN #%d has no merge base in depth 50; fetching 500 more", m.N)
			_, err := b.git(ctx, "fetch", "-q", "--deepen", "500", "origin")
			return err
		}
	}
	return nil
}

// merge merges one member --no-ff; files is non-nil on a conflict (the merge
// is aborted, the tree is as before).
func (b Build) merge(ctx context.Context, m Member) ([]string, error) {
	msg := fmt.Sprintf("Merge %s#%d at %s into %s (read %s %d)", b.Repo, m.N, short(m.Head), b.Branch, m.Who, m.Score)
	if _, err := b.git(ctx, "merge", "--no-ff", "-q", "-m", msg, fmt.Sprintf("refs/land/pr/%d", m.N)); err == nil {
		return nil, nil
	}
	out, _ := b.git(ctx, "diff", "--name-only", "--diff-filter=U")
	files := strings.Fields(out)
	if _, err := b.git(ctx, "merge", "--abort"); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("merge #%d failed with no conflicting file", m.N)
	}
	return files, nil
}

// CIBatch is the test word of a build that ran no batch test: the stream
// head is tested by our own CI on a bench (nova-sprint ci request), and the
// lander waits on ci:<repo>:<head>.
const CIBatch = "ci"

// testCmd is the declared batch test: cfg:land:test:<repo>, else make check
// when the Makefile has a check target, else go test ./...
func (b Build) testCmd() string {
	if strings.TrimSpace(b.Test) != "" {
		return b.Test
	}
	if mk, err := os.ReadFile(filepath.Join(b.Workdir, "Makefile")); err == nil {
		if regexp.MustCompile(`(?m)^check\s*:`).Match(mk) {
			return "make check"
		}
	}
	return "go test ./..."
}

// test runs the batch test in its own process group under the timeout; the
// whole group is killed when it runs out. line is the first failing test
// line, or the last output line.
func (b Build) test(ctx context.Context) (bool, string, error) {
	to := b.TestTimeout
	if to <= 0 {
		to = 20 * time.Minute
	}
	tctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	cmd := exec.CommandContext(tctx, "bash", "-c", b.testCmd())
	cmd.Dir = b.Workdir
	ownGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return true, "", nil
	}
	if tctx.Err() != nil && ctx.Err() == nil {
		return false, "timeout after " + to.String(), nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false, "", fmt.Errorf("batch test %q: %w", b.testCmd(), err)
	}
	return false, failLine(out.String(), exit.ExitCode()), nil
}

var failRE = regexp.MustCompile(`--- FAIL: (\S+)`)

func failLine(out string, code int) string {
	if m := failRE.FindStringSubmatch(out); m != nil {
		return "--- FAIL: " + m[1]
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		last = "exit " + strconv.Itoa(code)
	}
	if len(last) > 160 {
		last = last[:160]
	}
	return last
}

// redWord is a park reason word: the failing test, else "batch-test".
func redWord(line string) string {
	if m := failRE.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return "batch-test"
}

// Push pushes HEAD to the stream branch. The branch is the lander's own, so
// a re-run replaces it (and the open stream PR follows it).
func (b Build) Push(ctx context.Context) error {
	_, err := b.git(ctx, "push", "-q", "--force", "origin", "HEAD:refs/heads/"+b.Branch)
	return err
}

// CommitCloses is, per member, the issues its commit messages close
// (base..member, GitHub's closing keywords; ParseCloses's form, "-" none).
// Run leaves the clone and every member's refs/land/pr/<n> for it.
func (b Build) CommitCloses(ctx context.Context, base string, members []Member) map[int]string {
	out := map[int]string{}
	for _, m := range members {
		msgs, err := b.git(ctx, "log", "--format=%B", base+"..refs/land/pr/"+strconv.Itoa(m.N))
		if err != nil {
			continue
		}
		out[m.N] = ParseCloses(msgs)
	}
	return out
}
