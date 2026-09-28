package fleetbuild

// SelfUpdate (#4337) is `nova-sprint self update [--sha <sha>] [--from
// <checkout>] [--allow-branch]`: rebuild.sh as a verb. The coordinator ran it
// by hand after every landing on the Studio: go build into a temp path, mv
// over ~/.local/bin/nova-sprint, print `nova-sprint version`. The fleet play
// does the benches; this verb does the coordinator's own machine, one receipt
// line per step:
//
//	SOURCE    the release clone (~/nova-bench/release-src/nova-tools) fetches
//	          dev and checks out the sha (dev's tip when none is given); with
//	          --from the checkout is built as it stands, never checked out
//	TOOLCHAIN the module's pinned Go: go.mod's toolchain line, else its go
//	          line, handed to the build as GOTOOLCHAIN
//	BUILT     go build -trimpath -ldflags "-X main.version=<v>" into a temp
//	          file beside the live binary, which must answer <v> before it
//	          is installed
//	MOVED     the temp file renamed over the live binary; the binary then
//	          answers <v>
//
// The install is os.Rename and nothing else: a rename swaps the directory
// entry, so a process running the old binary keeps its own file, and the
// verb holds no code path that writes into the live binary (cp over a running
// binary got it SIGKILLed on 2026-09-24, memory mac-binary-replace-by-mv).
// A commit that is not on origin/dev, or a --from tree with edits, is refused
// unless AllowBranch; a live binary already answering <v> is SKIPPED.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SelfUpdate is one rebuild of this machine's nova-sprint.
type SelfUpdate struct {
	Runner      ExecRunner
	Home        string // this machine's home: <Home>/.local/bin/nova-sprint
	From        string // a checkout built as it stands; "" is the release clone
	Sha         string // the commit to build; "" is dev's tip (release clone) or HEAD (--from)
	AllowBranch bool   // install a commit off dev, or an edited --from tree
	Train       string // "" is DefaultTrain
	RepoURL     string // the release clone's remote; "" is ReleaseRepoURL
	PID         int    // names the temp file, so two runs never share one
	Out         io.Writer
}

// SelfUpdateResult is what a run did.
type SelfUpdateResult struct {
	Bin       string
	Old, New  string // the version lines' build identities; Old "none" when no binary answered
	Commit    string
	Toolchain string
	Skipped   bool
}

func (s *SelfUpdate) printf(format string, a ...any) {
	if s.Out != nil {
		fmt.Fprintf(s.Out, format, a...)
	}
}

func (s *SelfUpdate) run(ctx context.Context, dir string, env []string, argv ...string) (string, error) {
	return s.Runner.Run(ctx, dir, env, argv)
}

// Bin is the live binary this verb replaces.
func (s *SelfUpdate) Bin() string {
	return filepath.Join(s.Home, filepath.FromSlash(BinDir), "nova-sprint")
}

// Run does the steps; an error is a refusal (ErrRefused) naming the remedy.
// Nothing is installed unless every step before MOVED answered.
func (s *SelfUpdate) Run(ctx context.Context) (SelfUpdateResult, error) {
	res := SelfUpdateResult{Bin: s.Bin(), Old: "none"}
	sha := strings.ToLower(strings.TrimSpace(s.Sha))
	if sha != "" && !shaRe.MatchString(sha) {
		return res, refused("--sha %q is not a commit sha of 8 to 40 hex digits", s.Sha)
	}
	dir, commit, dirty, err := s.source(ctx, sha)
	if err != nil {
		return res, err
	}
	res.Commit = commit
	onDev := s.onDev(ctx, dir, commit)
	if !onDev && !s.AllowBranch {
		return res, refused("%s is not on origin/%s (land it first, or pass --allow-branch to install a branch build)", commit[:12], ReleaseBase)
	}
	if dirty && !s.AllowBranch {
		return res, refused("%s has uncommitted edits, so the build is not %s (commit or stash them, or pass --allow-branch)", dir, commit[:12])
	}
	tc, err := PinnedToolchain(filepath.Join(dir, "go.mod"))
	if err != nil {
		return res, err
	}
	res.Toolchain = tc
	s.printf("TOOLCHAIN %s from %s\n", tc, filepath.Join(dir, "go.mod"))
	res.New = SelfVersion(s.train(), commit, onDev, dirty)

	bin := res.Bin
	if out, err := s.run(ctx, "", nil, bin, "version"); err == nil {
		if v := versionOf(out); v != "" {
			res.Old = v
		}
	}
	if res.Old == res.New && !dirty {
		s.printf("SKIPPED %s already answers %s\n", bin, res.New)
		res.Skipped = true
		return res, nil
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return res, refused("bin directory: %v", err)
	}
	// The temp file sits beside the live binary so the rename stays on one
	// filesystem, where it is atomic.
	tmp := filepath.Join(filepath.Dir(bin), fmt.Sprintf(".nova-sprint.self-update.%d", s.PID))
	defer os.Remove(tmp)
	out, err := s.run(ctx, dir, []string{"GOTOOLCHAIN=" + tc}, "go", "build", "-trimpath", "-ldflags", "-X main.version="+res.New, "-o", tmp, "./cmd/nova-sprint")
	if err != nil {
		return res, refused("go build %s with %s in %s: %v: %s (nothing installed)", res.New, tc, dir, err, lastLine(out))
	}
	out, err = s.run(ctx, "", nil, tmp, "version")
	if got := versionOf(out); err != nil || got != res.New {
		return res, refused("the build answers %q, not %s: %v (nothing installed)", lastLine(out), res.New, err)
	}
	s.printf("BUILT %s %s\n", res.New, tmp)
	if err := install(tmp, bin); err != nil {
		return res, err
	}
	out, err = s.run(ctx, "", nil, bin, "version")
	if got := versionOf(out); err != nil || got != res.New {
		return res, refused("%s answers %q after the move, not %s: %v (rerun; the loops still run their old binary)", bin, lastLine(out), res.New, err)
	}
	s.printf("MOVED %s %s\n", bin, res.New)
	return res, nil
}

// install is the only way this verb writes the live binary: a rename of the
// finished temp file over it. Never a copy, never an open for writing.
func install(tmp, bin string) error {
	if err := os.Rename(tmp, bin); err != nil {
		return refused("mv %s %s: %v (the live binary is untouched)", tmp, bin, err)
	}
	return nil
}

func (s *SelfUpdate) train() string {
	if s.Train != "" {
		return s.Train
	}
	return DefaultTrain
}

// source names the directory to build and the commit it holds, and whether
// that tree carries edits (only a --from checkout can).
func (s *SelfUpdate) source(ctx context.Context, sha string) (dir, commit string, dirty bool, err error) {
	if s.From == "" {
		rev := sha
		if rev == "" {
			rev = "origin/" + ReleaseBase
		}
		rel := &Release{Runner: s.Runner, Home: s.Home, RepoURL: s.RepoURL, Out: s.Out}
		commit, err = rel.source(ctx, rev)
		return rel.srcDir(), commit, false, err
	}
	dir = s.From
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", "", false, refused("--from %s is not a git checkout (name a nova-tools clone or worktree)", dir)
	}
	// No --depth: a depth on a full clone would make it shallow.
	if out, err := s.run(ctx, dir, nil, "git", "fetch", "-q", "origin", ReleaseBase); err != nil {
		return "", "", false, refused("git fetch origin %s in %s: %v: %s", ReleaseBase, dir, err, lastLine(out))
	}
	out, err := s.run(ctx, dir, nil, "git", "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	commit = lastLine(out)
	if err != nil || !commitRe.MatchString(commit) {
		return "", "", false, refused("%s has no HEAD commit: %v: %s", dir, err, commit)
	}
	if sha != "" && !strings.HasPrefix(commit, sha) {
		return "", "", false, refused("--from %s is at %s, not %s (--from builds the checkout as it stands: check out %s there, or drop --from to build it in the release clone)", dir, commit[:12], sha, sha)
	}
	out, err = s.run(ctx, dir, nil, "git", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", "", false, refused("git status in %s: %v: %s", dir, err, lastLine(out))
	}
	dirty = strings.TrimSpace(out) != ""
	s.printf("SOURCE %s %s dirty=%t\n", dir, commit, dirty)
	return dir, commit, dirty, nil
}

// onDev is true when commit is origin/dev or an ancestor of it.
func (s *SelfUpdate) onDev(ctx context.Context, dir, commit string) bool {
	_, err := s.run(ctx, dir, nil, "git", "merge-base", "--is-ancestor", commit, "origin/"+ReleaseBase)
	return err == nil
}

// SelfVersion is the build identity self update stamps: the fleet release's
// <train>-dev.<sha8> for a dev commit (so fleet release sees the Studio
// already on it), -branch.<sha8> off dev, and -dirty for an edited tree.
func SelfVersion(train, commit string, onDev, dirty bool) string {
	kind := "dev"
	if !onDev {
		kind = "branch"
	}
	v := train + "-" + kind + "." + commit[:8]
	if dirty {
		v += "-dirty"
	}
	return v
}

// PinnedToolchain reads the Go a module pins: its toolchain line, else its go
// line as go<version>. A go.mod with neither, or a name GOTOOLCHAIN would not
// take, is refused.
func PinnedToolchain(gomod string) (string, error) {
	b, err := os.ReadFile(gomod)
	if err != nil {
		return "", refused("cannot read %s: %v (is this a module checkout?)", gomod, err)
	}
	var goLine, toolchain string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "go":
			goLine = "go" + f[1]
		case "toolchain":
			toolchain = f[1]
		}
	}
	tc := toolchain
	if tc == "" {
		tc = goLine
	}
	if tc == "" {
		return "", refused("%s pins no Go (no toolchain or go line)", gomod)
	}
	if !toolchainRe.MatchString(tc) {
		return "", refused("%s pins %q, which is not a go<x>.<y>[.<z>] toolchain", gomod, tc)
	}
	return tc, nil
}
