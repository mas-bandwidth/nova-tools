package release

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

// childCap is the ceiling on one child's captured output, the same 64 KiB the
// rest of this repository holds a child to (internal/update.ChildCap,
// internal/merge's execOutputCap). A gh or ssh that writes without end must not
// be a way to exhaust this process's memory.
const childCap = 64 * 1024

// forgeCap is the ceiling on ONE forge read, and it is far above childCap for a
// measured reason: `cut` asks the compare endpoint for every commit between the
// previous tag and the head, and this repository's own v0.15.2...dev range
// answers with well over 64 KiB of JSON. Under the 64 KiB cap the read was
// cancelled mid-stream and the verb reported `signal: killed` -- a refusal that
// named the symptom and no cause at all (found by the first real dry run, before
// this verb ever cut anything). 4 MiB is roughly twenty thousand commits of
// subject and body, which is more than any two tags of this estate have ever
// been apart, and it is still a ceiling: a runaway gh cannot fill memory, and a
// read that reaches it REFUSES by name below rather than returning a prefix.
const forgeCap = 4 * 1024 * 1024

// runCommand runs one argv directly -- no shell, no pipe, no glob, no
// environment expansion -- and returns its output, stdout and stderr together,
// bounded. SPEC-UPDATE rule 3 is the same rule and this is the same reason: a
// command built as a string is a command whose quoting nobody can check.
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	out, _, err := runCommandCapped(ctx, childCap, nil, "", name, args...)
	return out, err
}

func runCommandInput(ctx context.Context, stdin io.Reader, dir, name string, args ...string) (string, error) {
	out, _, err := runCommandCapped(ctx, childCap, stdin, dir, name, args...)
	return out, err
}

// runCommandCapped is the one exec site. It reports whether the capture reached
// the ceiling separately from the error, so a caller can tell "the child said
// too much" from "the child failed": the first has a remedy about the range
// being read, the second has a remedy about the child.
func runCommandCapped(ctx context.Context, cap int, stdin io.Reader, dir, name string, args ...string) (string, bool, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := bounded.NewCapture(cap, cancel)
	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	captured := string(out.Bytes())
	if out.Hit() {
		return captured, true, err
	}
	return captured, false, err
}

// GH is the production forge: one `gh` invocation per question, each under the
// run's deadline. gh carries its own credential; nothing here reads, logs or
// passes one.
type GH struct{ Timeout time.Duration }

// NewGH returns the production forge.
func NewGH(timeout time.Duration) *GH { return &GH{Timeout: timeout} }

func (g *GH) api(ctx context.Context, args ...string) (string, error) {
	out, hit, err := runCommandCapped(ctx, forgeCap, nil, "", "gh", args...)
	return out, apiError(args, hit, err)
}

// apiError is the decision the ceiling exists for, kept apart from the exec so a
// test can make it without a subprocess: THE CEILING IS REPORTED BEFORE THE
// ERROR. Reaching it cancels the child, so the error is whatever signal that
// cancellation produced -- `signal: killed` -- and a refusal that prints that
// has told the reader nothing about what happened or what to do about it.
func apiError(args []string, hit bool, err error) error {
	if hit {
		return refuse("cut from a nearer tag, or raise forgeCap",
			"gh %s answered more than the %d byte ceiling", strings.Join(args, " "), forgeCap)
	}
	if err != nil {
		return fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// HeadSHA asks the forge, not a local checkout: the commit a release is cut from
// is the one the forge believes main is at, and a local clone can be behind it
// by exactly the merge somebody is about to release.
func (g *GH) HeadSHA(ctx context.Context, repo, branch string) (string, error) {
	out, err := g.api(ctx, "api", "repos/"+repo+"/commits/"+branch, "--jq", ".sha")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", fmt.Errorf("gh answered no sha for %s of %s", branch, repo)
	}
	return sha, nil
}

// CheckRuns reads every check run recorded against a commit, paginated, because
// this repository runs more than a page of them on a self-hosted fleet and a
// first page read as the whole set is a green nobody checked.
func (g *GH) CheckRuns(ctx context.Context, repo, sha string) ([]CheckRun, error) {
	out, err := g.api(ctx, "api", "--paginate", "repos/"+repo+"/commits/"+sha+"/check-runs",
		"--jq", ".check_runs[] | {Name:.name, Status:.status, Conclusion:.conclusion}")
	if err != nil {
		return nil, err
	}
	var runs []CheckRun
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var r CheckRun
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("cannot read gh's check runs: %w", err)
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// Tags lists tag names. The caller picks the highest by semantic order; this
// returns them in whatever order the forge gave them, because a lexical order
// from the API is exactly the order that puts v0.15.10 before v0.15.3.
func (g *GH) Tags(ctx context.Context, repo string) ([]string, error) {
	out, err := g.api(ctx, "api", "--paginate", "repos/"+repo+"/tags", "--jq", ".[].name")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			tags = append(tags, line)
		}
	}
	return tags, nil
}

// Compare lists the commits between two revisions. ONE call answers the whole
// changelog: a squash merge's message carries the pull request's title in its
// subject and its body underneath, so the alternative -- list the merged pull
// requests, then read each one -- is a hundred calls for the same text.
func (g *GH) Compare(ctx context.Context, repo, base, head string) ([]Commit, error) {
	out, err := g.api(ctx, "api", "--paginate", "repos/"+repo+"/compare/"+base+"..."+head,
		"--jq", ".commits[] | {SHA:.sha, Message:.commit.message}")
	if err != nil {
		return nil, err
	}
	var commits []Commit
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var c Commit
		if err := dec.Decode(&c); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("cannot read gh's compare: %w", err)
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// Tag creates the tag ref. It is a create, never a force-move: a tag that can be
// moved is a tag whose binaries and whose source can disagree, which is the one
// state release.yml spends thirty lines refusing.
func (g *GH) Tag(ctx context.Context, repo, tag, sha string) error {
	_, err := g.api(ctx, "api", "--method", "POST", "repos/"+repo+"/git/refs",
		"-f", "ref=refs/tags/"+tag, "-f", "sha="+sha)
	return err
}

// GoBuild is the production toolchain. CGO is off so the artifact runs on a
// bench whose libc is not this one's, and the arguments the caller composed --
// -trimpath and the -ldflags stamp -- are passed through untouched.
type GoBuild struct{}

// Build compiles one package to one output path.
func (GoBuild) Build(ctx context.Context, source, pkg, out, goos, goarch string, args []string) (string, error) {
	argv := append([]string{"build"}, args...)
	argv = append(argv, "-o", out, pkg)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	capture := bounded.NewCapture(childCap, cancel)
	cmd := exec.CommandContext(runCtx, "go", argv...)
	cmd.Dir = source
	cmd.Stdout = capture
	cmd.Stderr = capture
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	err := cmd.Run()
	return string(capture.Bytes()), err
}

// ExecSSH is the production remote. The ssh binary is named by --ssh rather than
// found on PATH, because "no cwd dependence, every path a flag" applies to the
// program as much as to the directories: a bench with two ssh binaries should
// not be a coin toss.
type ExecSSH struct{ Path string }

// sshArgs are the options every invocation carries. BatchMode so a missing key
// is a refusal now rather than a password prompt nobody is at the keyboard for,
// and a connect timeout so a sleeping bench costs seconds rather than the run.
func (s ExecSSH) sshArgs(machine string) []string {
	return []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", machine}
}

// Run executes argv on the machine. The arguments are handed to ssh as separate
// argv entries; the remote's own shell still reassembles them, so every value
// that reaches here has been checked by its caller (the machine name against
// machineName, the version against ValidVersion, the paths by the flags that
// named them).
func (s ExecSSH) Run(ctx context.Context, machine string, argv []string) (string, error) {
	args := append(s.sshArgs(machine), argv...)
	return runCommand(ctx, s.Path, args...)
}

// Send copies a directory to the machine as a tar stream on ssh's stdin. The tar
// is written HERE, in Go, rather than by a local `tar` piped into a remote one:
// the thing this replaces was two ssh invocations joined by a shell pipe, where
// a failure in the first was invisible to the second.
func (s ExecSSH) Send(ctx context.Context, machine, dir, dest string) (string, error) {
	base := filepath.Base(dir)
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(writeTar(pw, dir, base))
	}()
	defer pr.Close()
	args := append(s.sshArgs(machine), "mkdir", "-p", dest, "&&", "tar", "-C", dest, "-xf", "-")
	return runCommandInput(ctx, pr, "", s.Path, args...)
}

// writeTar streams dir into w under the single top-level name prefix, with modes
// preserved so that an executable arrives executable.
func writeTar(w io.Writer, dir, prefix string) error {
	tw := tar.NewWriter(w)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		header := &tar.Header{
			Name:    path.Join(prefix, e.Name()),
			Mode:    int64(info.Mode().Perm()),
			Size:    int64(len(body)),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tw.Write(body); err != nil {
			return err
		}
	}
	return tw.Close()
}
