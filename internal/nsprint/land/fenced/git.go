package fenced

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Identity is a git author and committer.
type Identity struct{ Name, Email string }

// fenceIdentity and fenceDate make a fence commit a function of its inputs:
// the same repo, base, gen, runner (and parent, merge) give the same sha.
var fenceIdentity = Identity{Name: "nova-land", Email: "nova-land@invalid"}

const fenceDate = "1970-01-01T00:00:00Z"

// git runs one git command in dir with the host's global and system config
// switched off, so no url rewrite, credential helper or identity of the seat
// leaks into a landing. env adds variables (identity, askpass).
type git struct {
	bin string
	dir string
	env []string
}

func (g git) cmd(ctx context.Context, env []string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, g.bin, append([]string{"-C", g.dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0")
	c.Env = append(c.Env, g.env...)
	c.Env = append(c.Env, env...)
	return c
}

// run returns trimmed stdout, or an error naming the command and stderr's
// first line.
func (g git) run(ctx context.Context, env []string, stdin string, args ...string) (string, error) {
	c := g.cmd(ctx, env, args...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	if err := c.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %w: %s", args[0], err, firstLine(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// status runs git and returns its exit code with stdout and stderr (an exec
// failure that is not an exit is an error).
func (g git) status(ctx context.Context, args ...string) (int, string, string, error) {
	c := g.cmd(ctx, nil, args...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out.String(), errb.String(), nil
	}
	if err != nil {
		return -1, out.String(), errb.String(), err
	}
	return 0, out.String(), errb.String(), nil
}

func idEnv(id Identity, date string) []string {
	env := []string{"GIT_AUTHOR_NAME=" + id.Name, "GIT_AUTHOR_EMAIL=" + id.Email,
		"GIT_COMMITTER_NAME=" + id.Name, "GIT_COMMITTER_EMAIL=" + id.Email}
	if date != "" {
		env = append(env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	}
	return env
}

// revParse returns the sha a ref names, or "" when it does not exist.
func (g git) revParse(ctx context.Context, ref string) string {
	code, out, _, err := g.status(ctx, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

// isAncestor reports whether a is an ancestor of (or equal to) b.
func (g git) isAncestor(ctx context.Context, a, b string) bool {
	code, _, _, err := g.status(ctx, "merge-base", "--is-ancestor", a, b)
	return err == nil && code == 0
}

// mergeTree is fact (ii): the tree of T and H merged, or the conflicted
// files. clean false with files is a conflict.
func (g git) mergeTree(ctx context.Context, T, H string) (tree string, clean bool, files []string, err error) {
	code, out, errOut, err := g.status(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", T, H)
	if err != nil {
		return "", false, nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != 0 && code != 1 {
		return "", false, nil, fmt.Errorf("git merge-tree: exit %d: %s", code, firstLine(errOut))
	}
	if len(lines) == 0 || lines[0] == "" {
		return "", false, nil, fmt.Errorf("git merge-tree: no tree")
	}
	seen := map[string]bool{}
	for _, l := range lines[1:] {
		if l == "" {
			break
		}
		if !seen[l] {
			seen[l] = true
			files = append(files, l)
		}
	}
	return lines[0], code == 0, files, nil
}

// fenceCommit builds a fence commit: an empty tree, the nova-land identity
// at the epoch, so the same message and parent give the same sha.
func (g git) fenceCommit(ctx context.Context, msg, parent string) (string, error) {
	empty, err := g.run(ctx, nil, "", "mktree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", empty, "-m", msg}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return g.run(ctx, idEnv(fenceIdentity, fenceDate), "", args...)
}

// FenceMessage is the arm fence's message for gen.
func FenceMessage(repo, base string, gen int64, runner string) string {
	return fmt.Sprintf("nova-land fence repo=%s base=%s gen=%d runner=%s", repo, base, gen, runner)
}

var genRx = regexp.MustCompile(`(?:^|\s)gen=(\d+)(?:\s|$)`)

var runnerRx = regexp.MustCompile(`(?:^|\s)runner=(\S+)`)

// fenceGen reads gen=<g> and runner=<id> from a fence commit's message; ok
// false when the message has no readable gen.
func (g git) fenceGen(ctx context.Context, sha string) (gen int64, runner string, ok bool) {
	msg, err := g.run(ctx, nil, "", "log", "-1", "--format=%B", sha)
	if err != nil {
		return 0, "", false
	}
	m := genRx.FindStringSubmatch(msg)
	if m == nil {
		return 0, "", false
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if r := runnerRx.FindStringSubmatch(msg); r != nil {
		runner = r[1]
	}
	return v, runner, err == nil
}

// pushLine is one ref line of `git push --porcelain`.
type pushLine struct {
	flag    byte
	dst     string
	summary string
}

func parsePorcelain(out string) []pushLine {
	var ls []pushLine
	for _, l := range strings.Split(out, "\n") {
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) != 3 || len(parts[0]) != 1 {
			continue
		}
		_, dst, _ := strings.Cut(parts[1], ":")
		ls = append(ls, pushLine{flag: parts[0][0], dst: dst, summary: parts[2]})
	}
	return ls
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
