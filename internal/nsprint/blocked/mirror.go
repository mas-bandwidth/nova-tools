package blocked

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
)

// Mirror is the Git seam over the bench mirrors: <Root>/<repo>.git, a bare
// `git clone --mirror` (refs/heads/<base>). It runs local git only; the
// mirror is fetched by whoever keeps it fresh, never by this package.
type Mirror struct {
	Root string // default $HOME/nova-bench/mirror
	Git  string // default "git"
}

// DefaultRoot is the bench mirror root: $NOVA_MIRROR_ROOT, else
// $HOME/nova-bench/mirror.
func DefaultRoot() string {
	if v := os.Getenv("NOVA_MIRROR_ROOT"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "nova-bench", "mirror")
}

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (m Mirror) dir(repo string) (string, error) {
	if !repoNameRE.MatchString(repo) || strings.HasPrefix(repo, ".") {
		return "", fmt.Errorf("repo %q is not a bare repository name", repo)
	}
	root := m.Root
	if root == "" {
		root = DefaultRoot()
	}
	d := filepath.Join(root, repo+".git")
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no mirror %s", d)
	}
	return d, nil
}

func (m Mirror) git(ctx context.Context, dir string, args ...string) *exec.Cmd {
	prog := m.Git
	if prog == "" {
		prog = "git"
	}
	cmd := exec.CommandContext(ctx, prog, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// Tip implements Git: base "" is dev when the mirror has it, else main.
func (m Mirror) Tip(ctx context.Context, repo, base string) (string, string, error) {
	dir, err := m.dir(repo)
	if err != nil {
		return "", "", err
	}
	bases := []string{base}
	if base == "" {
		bases = []string{"dev", "main"}
	}
	for _, b := range bases {
		if strings.HasPrefix(b, "-") || strings.ContainsAny(b, " \t:~^") {
			return "", "", fmt.Errorf("base %q is not a branch", b)
		}
		out, err := m.git(ctx, dir, "rev-parse", "--verify", "-q", "refs/heads/"+b+"^{commit}").Output()
		if err == nil {
			return b, strings.TrimSpace(string(out)), nil
		}
	}
	return "", "", fmt.Errorf("mirror %s has no branch %s", dir, strings.Join(bases, " or "))
}

// Landed implements Git: head an ancestor of base (one merge-base call), else
// the newest commit on base whose message merges, closes or squashes #n.
func (m Mirror) Landed(ctx context.Context, repo, base string, n int, head string) (string, error) {
	dir, err := m.dir(repo)
	if err != nil {
		return "", err
	}
	ref := "refs/heads/" + base
	if head != "" && isSHA(head) {
		err := m.git(ctx, dir, "merge-base", "--is-ancestor", head, ref).Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return head, nil
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			// not an ancestor: a squash or rebase may still have landed it
		case errors.As(err, &exit):
			// the head is not in the mirror: fall through to the message
		default:
			return "", err
		}
	}
	out, err := m.git(ctx, dir, "log", "-1", "--format=%H", "-E", "--regexp-ignore-case",
		"--grep="+LandingPattern(repo, n), ref).Output()
	if err != nil {
		return "", fmt.Errorf("git log %s: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func isSHA(s string) bool { return shaRE.MatchString(s) }

// LandingPattern is the POSIX extended regex for a commit message that lands
// #n of repo: "Merge #n", "Merge <repo>#n", "Merge pull request #n",
// "Closes|Fixes|Resolves [owner/]<repo>#n" or a squash title's "(#n)". A
// mention in passing ("part of #n", "amends #n") is not a landing.
func LandingPattern(repo string, n int) string {
	num := strconv.Itoa(n)
	name := regexp.QuoteMeta(repo)
	ref := `([A-Za-z0-9_.-]+/)?(` + name + `)?#` + num + `([^0-9]|$)`
	verbs := `(merge( pull request)?|close[sd]?|fix(e[sd])?|resolve[sd]?):? +`
	return `(^|[^A-Za-z0-9_])(` + verbs + ref + `|\(#` + num + `\))`
}
