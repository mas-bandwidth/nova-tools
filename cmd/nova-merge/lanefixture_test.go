package main

// lanefixture_test.go is how the tests make a lane now that init, quickstart, add and
// add-branch left the binary with the per-PR lander role (stream-is-the-unit). gate and
// read still write their records into a lane directory, so the tests that drive them
// build one with the code those verbs used to run, called from the lab rather than from
// the command line. The stream-lander spec moves those records to Redis; this file goes
// with the lane then.

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// laneRun is run() plus the three lane-making verbs the binary no longer carries.
func laneRun(args []string, stdout, stderr io.Writer, deps Deps) int {
	if len(args) > 0 {
		rest := args[1:]
		switch args[0] {
		case "init":
			return cmdInit(rest, stdout, stderr, deps)
		case "add":
			return cmdAdd(rest, stdout, stderr, deps, false)
		case "add-branch":
			return cmdAdd(rest, stdout, stderr, deps, true)
		}
	}
	return run(args, stdout, stderr, deps)
}

// selectorOf is how a remedy names the entry it is about, on a command line.
func selectorOf(isBranch bool, pr int, branch string) string {
	if isBranch {
		return "--branch " + branch
	}
	return "--pr " + strconv.Itoa(pr)
}

// cmdAdd queues an entry into a lane that EXISTS. It creates nothing: the lane was made
// by init, with its repository and its base, and a queueing verb that also created would
// be a creation with two arguments nothing asked for.
func cmdAdd(args []string, stdout, stderr io.Writer, deps Deps, isBranch bool) int {
	verb := "add"
	if isBranch {
		verb = "add-branch"
	}
	f := laneSet(verb)
	pr := f.fs.Int("pr", 0, "")
	branch := f.fs.String("branch", "", "")
	needsRead := f.fs.Bool("needs-read", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	if isBranch {
		f.require("branch", *branch, "the branch this lane is to land, as it is named at the host")
		// Lesson 48, as init's --base and --lane-branch already pay it: this branch is
		// stored once and handed to git on every pass afterwards, so it is checked HERE,
		// where a person can still see what they typed (security#30 finding 5).
		if strings.TrimSpace(*branch) != "" {
			if err := merge.ValidRefName(*branch); err != nil {
				f.problem(fmt.Sprintf("--branch: %s", oneline.Escape(err.Error())))
			}
		}
	} else if !given(f.fs, "pr") {
		f.problem("--pr is required and is the pull request's number; refusing to guess")
	} else if *pr < 1 {
		// REACHABLE, and it says a different thing: a flag nobody typed is a missing
		// argument, and `--pr 0` or `--pr -3` is a number typed and wrong. Both were
		// behind one `*pr < 1`, so the second sentence could never print, and the test
		// that asserted only "--pr" could not tell the two apart.
		f.problem(fmt.Sprintf("--pr is a pull request's number, which is positive, got %d; a number no host can answer for queues in this lane forever", *pr))
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane(verb, *f.lane, stderr)
	if st == nil {
		return code
	}
	id := *branch
	if !isBranch {
		id = strconv.Itoa(*pr)
	}
	if e := st.Find(id); e != nil {
		fmt.Fprintf(stdout, "ADD NOTE %s is already in the lane (needs_read=%s)\n", oneline.Field(id), oneline.Field(e.NeedsRead))
		return 0
	}
	yn := "no"
	if *needsRead {
		yn = "yes"
	}
	err := merge.Update(*f.lane, f.dur(), func(s *merge.State) error {
		e := &merge.Entry{NeedsRead: yn, Reads: []merge.Read{}, State: merge.StateNew}
		if isBranch {
			e.Branch = *branch
			s.Branches = append(s.Branches, e)
		} else {
			e.PR = *pr
			s.PRs = append(s.PRs, e)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "ADD REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	// The re-read feeds ADD OK's counts. Its error was assigned to `_`, so a re-read that
	// failed left st nil and the counts dereferenced it -- a panic -- for a state that was
	// just written. The entry IS queued; the refusal is about the counts that could not be
	// read, and exit 2 says so rather than printing ADD OK with no counts.
	st, err = reloadLane(*f.lane)
	if err != nil {
		fmt.Fprintf(stderr, "ADD REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	if yn == "no" {
		// THE DEFAULT IS THE DIRECTION THAT MERGES WITH ZERO READS, and it was a field on
		// a line rather than a sentence anybody read.
		fmt.Fprintf(stderr, "ADD NOTE %s is queued with needs_read=no: this lane will merge it on its checks and its gate with NOBODY having read it; nova-merge add --lane %s %s --needs-read asks for one\n",
			oneline.Field(id), oneline.Field(*f.lane), oneline.Escape(selectorOf(isBranch, *pr, *branch)))
	}
	merge.Appendf(*f.lane, deps.Now(), "ADD %s %s needs_read=%s", verb, id, yn)
	kind := "pr"
	if isBranch {
		kind = "branch"
	}
	fmt.Fprintf(stdout, "ADD OK kind=%s entry=%s needs_read=%s lane=%d/%d\n",
		kind, oneline.Field(id), yn, len(st.PRs), len(st.Branches))
	return 0
}

// checkout makes the lane directory a checkout of its own branch of the repository at url:
// the existing branch where the host has one (joined=true, which is how a reader on another
// machine gets a lane for the same records), and otherwise one commit holding .gitignore.
func checkout(lane, url, branch string, timeout time.Duration, deps Deps) (joined bool, err error) {
	if err := os.MkdirAll(lane, 0o755); err != nil {
		return false, err
	}
	g := merge.NewGit(lane, timeout, deps.Runner)
	if _, err := g.Run("init", "-q", "-b", branch, "."); err != nil {
		return false, err
	}
	if _, err := g.Run("remote", "add", "origin", url); err != nil {
		if _, err2 := g.Run("remote", "set-url", "origin", url); err2 != nil {
			return false, err
		}
	}
	if _, err := g.Run("fetch", "origin", branch); err == nil {
		if _, err := g.Run("checkout", "-B", branch, "FETCH_HEAD"); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := os.WriteFile(filepath.Join(lane, ".gitignore"), []byte(merge.GitIgnore), 0o644); err != nil {
		return false, err
	}
	if _, err := g.Run("add", "--", ".gitignore"); err != nil {
		return false, err
	}
	if _, err := g.Run(merge.Identity("commit", "-m", "nova-merge: the lane's record branch")...); err != nil {
		return false, err
	}
	if _, err := g.Run("push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		return false, err
	}
	return false, nil
}

// undoInit is what a refused init says about what it made. A directory init created is
// removed, so the retry meets the same cause; a directory that was already there is not
// removed and the two paths to delete are named instead, because a lane a person made is
// not this tool's to throw away.
func undoInit(lane string, existed bool) string {
	if !existed {
		// The lane is a flag, so the flag is what a mistake can aim at the wrong
		// directory. RemoveUnder refuses a lane that is its own parent's name, a
		// symlink, an empty path, or a root that is "/" or the user's home: the
		// removal only ever happens under the directory the lane sits in.
		if err := safepath.RemoveUnder(filepath.Dir(lane), lane); err == nil {
			return "; nothing was left behind, so running this again meets the same cause and not the wreckage"
		}
	}
	return fmt.Sprintf("; this left a checkout behind in a directory that was already here: remove %s and %s before running this again",
		oneline.Field(filepath.Join(lane, ".git")), oneline.Field(filepath.Join(lane, ".gitignore")))
}

// stripUserinfo removes the credential a URL carries so a diagnostic never prints a token
// or password: `https://user:token@host/repo.git` becomes `https://host/repo.git`. A value
// that does not parse as a URL, or that carries no userinfo, is returned unchanged.
func stripUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// cmdInit is the lane's ONE creation (rule 20), as the retired init verb ran it.
func cmdInit(args []string, stdout, stderr io.Writer, deps Deps) int {
	verb := "init"
	f := laneSet(verb)
	repo := f.fs.String("repo", "", "")
	base := f.fs.String("base", "", "")
	laneBranch := f.fs.String("lane-branch", "", "")
	// --remote is the URL this lane pushes to and clones from. Without it nothing could
	// be exercised without a live GitHub repository -- the URL was hardcoded, so a new
	// line could not rehearse, and git's own config was the only way in, which means the
	// ENVIRONMENT could move where a lane pushes and no flag said so.
	remote := f.fs.String("remote", "", "")
	// --default-branch and --hosted-red are rule 15's two knobs, and they exist because
	// the rule was the literal string "main": a repository whose default branch is master,
	// trunk or release took the WEAKER arm in silence and merged over its hosted reds.
	// Neither is required -- the default branch is discovered from the remote's own HEAD,
	// and an unknown one takes the stronger arm -- and neither bakes in a naming
	// convention: a team states its own topology here instead of adopting ours.
	defaultBranch := f.fs.String("default-branch", "", "")
	hostedRed := f.fs.String("hosted-red", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	f.require("repo", *repo, "the repository this lane lands into, as <owner>/<name>")
	f.require("base", *base, "the branch this lane's entries are merged onto")
	// Lesson 48: these two are stored once and handed to git on every pass afterwards, so
	// they are checked HERE, where a person can still see what they typed.
	if err := merge.ValidHostedRed(*hostedRed); err != nil {
		f.problem(fmt.Sprintf("--hosted-red is %q or %q, got %q: %s", "blocks", "names", *hostedRed, oneline.Escape(err.Error())))
	}
	for _, c := range []struct{ name, value string }{{"base", *base}, {"lane-branch", *laneBranch}, {"default-branch", *defaultBranch}} {
		if c.value == "" {
			continue
		}
		if err := merge.ValidRefName(c.value); err != nil {
			f.problem(fmt.Sprintf("--%s: %s", c.name, oneline.Escape(err.Error())))
		}
	}
	f.require("lane-branch", *laneBranch, "the branch of that repository this lane's read and gate records live in")
	// A repo name is handed to git and gh, so it is not enough that it holds a slash:
	// "a/b/c", "-x/y", "../y" and a name with a space all reached both. It is exactly one
	// slash, both halves non-empty, each half in GitHub's own character set, and neither
	// half beginning with a dash -- which git and gh would read as an option.
	if *repo != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
		}
	}
	if !f.done(stderr) {
		return 2
	}
	if _, err := os.Stat(merge.StatePath(*f.lane)); err == nil {
		fmt.Fprintf(stderr, "INIT REFUSED: %s is already a lane; init creates one and never rewrites one, so its repo, base and lane branch are what the first init wrote\n",
			oneline.Field(*f.lane))
		return 1
	}
	url := *remote
	if url == "" {
		url = deps.RepoURL(*repo)
	}
	// A REFUSED INIT LEAVES NOTHING BEHIND. checkout() runs git init, writes .gitignore,
	// commits and pushes before merge.Init writes state.json; when it failed it cleaned
	// nothing up, so the retry refused with "On branch <lane branch>" -- a refusal about
	// the wreckage rather than about the cause -- and the directory was permanently
	// un-initializable.
	_, existed := os.Stat(*f.lane)
	joined, err := checkout(*f.lane, url, *laneBranch, f.dur(), deps)
	if err != nil {
		fmt.Fprintf(stderr, "INIT REFUSED: %s%s\n",
			oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)), oneline.Escape(undoInit(*f.lane, existed == nil)))
		return 2
	}
	// THE DEFAULT BRANCH IS DISCOVERED FROM THE REMOTE'S OWN HEAD, not from gh and not
	// from a name we assume. A discovery that does not answer writes nothing, prints one
	// INIT NOTE, and leaves the lane on the STRONGER hosted-red rule.
	discovered := *defaultBranch
	if discovered == "" && *hostedRed == "" {
		discovered = merge.DefaultBranchOf(merge.NewGit(*f.lane, f.dur(), deps.Runner), url)
		if discovered == "" {
			fmt.Fprintf(stderr, "INIT NOTE the repository's default branch could not be read from %s, so this lane records none and takes the STRONGER hosted-red rule: a hosted red stops the entry (rule 15). nova-merge init --lane %s --default-branch <branch> records it, and --hosted-red names states the other arm outright\n",
				oneline.Field(stripUserinfo(url)), oneline.Field(*f.lane))
		}
	}
	if err := merge.Init(*f.lane, merge.LaneConfig{Repo: *repo, Base: *base, LaneBranch: *laneBranch,
		DefaultBranch: discovered, HostedRed: *hostedRed}); err != nil {
		fmt.Fprintf(stderr, "INIT REFUSED: %s\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 1
	}
	g := merge.NewGit(*f.lane, f.dur(), deps.Runner)
	// A lane whose state.json was LOST is re-made by init on the same branch, and its
	// checkout and its clone are still on the disk (rule 22: losing state.json loses the
	// order and nothing else). A clone that is already here is taken rather than refused.
	if _, err := os.Stat(filepath.Join(*f.lane, merge.RepoDir, ".git")); err == nil {
		merge.Appendf(*f.lane, deps.Now(), "INIT took the clone that was already at %s", filepath.Join(*f.lane, merge.RepoDir))
	} else if _, err := g.Run("clone", url, merge.RepoDir); err != nil {
		fmt.Fprintf(stderr, "INIT REFUSED: the lane's own clone could not be made: %s\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	merge.Appendf(*f.lane, deps.Now(), "INIT lane=%s repo=%s base=%s lane_branch=%s default_branch=%s hosted_red=%s joined=%t",
		*f.lane, *repo, *base, *laneBranch, discovered, *hostedRed, joined)
	fmt.Fprintf(stdout, "INIT OK lane=%s repo=%s base=%s lane_branch=%s joined=%t version=%d\n",
		oneline.Field(*f.lane), oneline.Field(*repo), oneline.Field(*base), oneline.Field(*laneBranch), joined, merge.Version)
	return 0
}

// githubRepoName is GitHub's repository-name charset: letters, digits, '-', '_' and '.'.
func githubRepoName(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// githubOwner is GitHub's username and organization charset: letters, digits and '-'.
func githubOwner(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}


// given reports whether the flag was typed at all, which is not the same question as
// what its value is: a default is a value nobody chose.
func given(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
