package main

// WHERE THE GATE RUNS, AS A SEAM.
//
// The gate has two halves and they belong on two machines. The CLONE, the MERGES and the
// SUITE want cores and a toolchain, which is a bench (hulk: 64 of them, the fleet's Go and
// sbcl under ~/sdk). Everything that touches the forge -- reading each member's checks,
// pushing the branch, opening the pull request, the one door, the queue, closing the members
// -- wants a credential, which is on the Studio and never on a bench, because secrets are
// sealed once in the store and never copied between machines.
//
// batchSite is that line drawn once. runBatch performs the same steps in the same order
// whichever site it holds; the site knows how a command gets to the machine. There are two:
// localSite, which is this machine and is what `batch` has always been, and remoteSite
// (batchon.go), which is `--on <machine>` and reaches a bench over ssh.
//
// EVERY FORGE CALL IS OUTSIDE THIS INTERFACE ON PURPOSE. There is no method here that could
// reach a pull request, and `admissible`, `runBatchLand` and the one door are called by
// runBatch directly, on this machine, whichever site the gate ran on. A seam that could do
// both halves is a seam somebody eventually asks a bench to do the forge half through.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// batchSite is the machine one batch is built and judged on.
type batchSite interface {
	// On is the machine's name, which every progress line carries: the registry name with
	// --on, and "local" without it. It is never empty, because a field a reader has to
	// interpret an absence of is a field that says nothing.
	On() string
	// Start makes the working directory, clones, and puts branch at origin/base. It
	// answers the base's sha, which is what the verdict line names.
	Start(base, branch string) (string, error)
	// Merge merges one pull request head. merged is false when the merge CONFLICTED and
	// was aborted, which is a member dropped by name rather than a failure of the run.
	Merge(n int, message string) (merged bool, err error)
	// Head is the branch's sha now.
	Head() (string, error)
	// Read is one file of the checkout, and whether it could be read at all.
	Read(rel string) (string, bool)
	// Probe runs one short command in the checkout and answers what it printed, and
	// whether it succeeded. It is for the toolchain check and nothing else.
	Probe(command string) (string, bool)
	// Unavailable is why a step cannot run on this machine, or "" when it can.
	Unavailable(step batchStep) string
	// Step runs one step of the suite and answers its combined output.
	Step(step batchStep) (string, error)
	// Bring carries a green batch back to THIS machine, as a git bundle, and answers where
	// it put it and how big it was. A local site has nothing to carry and answers "".
	//
	// It is called on every green gate and not only before a landing, because a batch that
	// cannot cross the seam is a batch nobody can push, and the run that learns that should
	// be the run that built it -- not the one an hour later that meant to land it.
	Bring(branch, baseSHA, headSHA string) (path string, size int64, err error)
	// Clone is the LOCAL checkout the landing pushes from. On a remote site that is a
	// clone made here and fed from the bundle Bring carried back, so it is called once,
	// after the gate is green, and only by the landing.
	Clone() (string, error)
}

// localSite is the gate on this machine: the verb as it was before --on existed.
type localSite struct {
	in    batchRun
	deps  Deps
	root  string
	work  string
	tmp   string
	clone string
	g     *merge.Git
	env   []string
	// bins is the directory a step's program was found in when it was found OFF PATH,
	// filled by Unavailable and read by Step -- so the lookup happens once.
	bins map[string]string
}

// newLocalSite makes the working directory, which is REBUILT EVERY RUN so that a batch never
// merges on top of a tree an earlier one left half-merged. It is a path this tool COMPUTED,
// so its removal is safepath's and nobody else's.
func newLocalSite(in batchRun, deps Deps) (*localSite, error) {
	rootAbs, err := filepath.Abs(in.root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return nil, err
	}
	work := filepath.Join(rootAbs, in.name)
	if err := safepath.RemoveUnder(rootAbs, work); err != nil {
		return nil, err
	}
	tmp := filepath.Join(work, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	return &localSite{
		in: in, deps: deps,
		root:  rootAbs,
		work:  work,
		tmp:   tmp,
		clone: filepath.Join(work, "repo"),
		env:   ciTestEnv(tmp, in.gomaxprocs),
		bins:  map[string]string{},
	}, nil
}

// localOn is what a gate on this machine calls itself on every line. It is a word rather
// than an empty field so that one grammar serves both sites: a reader greps `on=` and gets
// an answer either way.
const localOn = "local"

func (s *localSite) On() string { return localOn }

func (s *localSite) Start(base, branch string) (string, error) {
	cloneArgs := []string{"clone", "--quiet"}
	if strings.TrimSpace(s.in.reference) != "" {
		// A mirror on this bench makes the clone local rather than a download. It is an
		// optimisation and never a requirement: without it the clone is an ordinary one.
		cloneArgs = append(cloneArgs, "--reference", s.in.reference)
	}
	// `--` before the URL, so a URL beginning with a dash is a URL and not an option.
	cloneArgs = append(cloneArgs, "--", s.deps.RepoURL(s.in.repo), s.clone)
	if _, err := merge.NewGit(s.work, s.in.timeout, s.deps.Runner).Run(cloneArgs...); err != nil {
		return "", err
	}
	s.g = merge.NewGit(s.clone, s.in.timeout, s.deps.Runner)
	if _, err := s.g.Run("fetch", "--quiet", "origin", base); err != nil {
		return "", fmt.Errorf("could not fetch origin/%s: %w", base, err)
	}
	if _, err := s.g.Run("checkout", "--quiet", "-B", branch, "FETCH_HEAD"); err != nil {
		return "", fmt.Errorf("could not start %s at origin/%s: %w", branch, base, err)
	}
	return s.g.Out("rev-parse", "HEAD")
}

func (s *localSite) Merge(n int, message string) (bool, error) {
	if _, err := s.g.Run("fetch", "--quiet", "origin", "pull/"+strconv.Itoa(n)+"/head"); err != nil {
		return false, fmt.Errorf("could not fetch pull/%d/head: %w", n, err)
	}
	// The merge writes a commit object, so it carries nova-merge's own identity: a CI
	// runner has no git identity anywhere and `git merge --no-ff` there dies with
	// "Committer identity unknown" (the Ubuntu leg of #57).
	_, err := s.g.Run(merge.Identity("merge", "--no-ff", "--no-edit", "-m", message, "FETCH_HEAD")...)
	if err == nil {
		return true, nil
	}
	unmerged, cerr := hasConflicts(s.g)
	if cerr != nil {
		return false, cerr
	}
	if !unmerged {
		return false, fmt.Errorf("the merge of pull/%d failed and left no conflicting file: %w", n, err)
	}
	if _, aerr := s.g.Run("merge", "--abort"); aerr != nil {
		return false, aerr
	}
	return false, nil
}

func (s *localSite) Head() (string, error) { return s.g.Out("rev-parse", "HEAD") }

func (s *localSite) Read(rel string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(s.clone, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func (s *localSite) Probe(command string) (string, bool) {
	out, err := runCheck(s.clone, command, s.in.timeout, s.env)
	return out, err == nil
}

func (s *localSite) Unavailable(step batchStep) string {
	why, bin := stepUnavailable(step, s.clone)
	if why == "" {
		s.bins[step.name] = bin
	}
	return why
}

func (s *localSite) Step(step batchStep) (string, error) {
	return runCheck(s.clone, step.command, s.in.timeout, append(withBin(s.env, s.bins[step.name]), step.env...))
}

// Bring carries nothing: the batch is already on this machine, in s.clone.
func (s *localSite) Bring(string, string, string) (string, int64, error) { return "", 0, nil }

func (s *localSite) Clone() (string, error) { return s.clone, nil }
