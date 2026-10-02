package main

import (
	"flag"
	"fmt"
	"strings"
)

func init() {
	register(verb{
		name:    "fetch-ancestry",
		summary: "complete a branch's ancestry in a shallow checkout",
		help: `ci fetch-ancestry [--promotion] <branch>

Fetches <branch> into refs/remotes/origin/<branch> with its whole ancestry: commits and
trees only (--filter=blob:none), completed with --unshallow when the checkout is
shallow. A complete workspace (a repeat run on a persistent runner) takes a plain fetch:
--unshallow on a complete repository is fatal.

The classtests rule reads a landing from git. On a main run at a merge commit, the
landed promotion, it excuses a deletion only when the second parent's ancestry shows dev
deleting it: fetch dev. On the promotion pull request (sprint/foundation into dev) dev's
tip must be an ancestor of the head, and on a dev run at a merge commit (a merge-queue
group, a push to dev) the second parent must be sprint/foundation's and its history
excuses what it deleted: fetch sprint/foundation. A shallow ancestry is a red run naming
this fetch.

--promotion: a commit with one parent on an event that is not a pull_request has no
promotion to read, so nothing is fetched ("a one-parent commit: no promotion to read").
GITHUB_EVENT_NAME names the event. The promotion branch is optional, too: dev is the
integration branch, and sprint/foundation exists only while a promotion is in flight. When
origin has no <branch> (git ls-remote --exit-code answers 2) there is no promotion to
read, so nothing is fetched ("origin has no branch <branch>: no promotion to read") and
the classtests rule excuses nothing, the safe side. Any other ls-remote failure is exit 1.

Exit 0 fetched or nothing to read, 1 a git command failed, 2 bad usage.

example:
  go run ./tools/ci fetch-ancestry dev
  go run ./tools/ci fetch-ancestry --promotion sprint/foundation
`,
		do: func(e env, args []string) int { return fetchAncestryVerb(e, args, selRealHost()) },
	})
}

func fetchAncestryVerb(e env, args []string, h selHost) int {
	const name = "fetch-ancestry"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	promotion := fs.Bool("promotion", false, "skip a one-parent commit on a run that is not a pull request")
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return selRefuse(e, name, "want exactly one branch")
	}
	branch := fs.Arg(0)
	root := selRoot(e)
	if *promotion && e.getenv("GITHUB_EVENT_NAME") != "pull_request" {
		res, err := h.run(root, nil, "git", "rev-list", "--parents", "-n", "1", "HEAD")
		if err != nil || res.Code != 0 {
			fmt.Fprintf(e.stderr, "fetch-ancestry: git rev-list --parents failed: %s\n", selWhy(res.Stderr, res.Code, err))
			return 1
		}
		if len(strings.Fields(res.Stdout)) < 3 {
			fmt.Fprintln(e.stdout, "a one-parent commit: no promotion to read")
			return 0
		}
	}
	if *promotion {
		// The promotion branch is optional: dev is the integration branch and
		// sprint/foundation exists only while a promotion is in flight.
		res, err := h.run(root, nil, "git", "ls-remote", "--exit-code", "--heads", "origin", branch)
		switch {
		case err == nil && res.Code == 2:
			fmt.Fprintf(e.stdout, "origin has no branch %s: no promotion to read\n", branch)
			return 0
		case err != nil || res.Code != 0:
			fmt.Fprintf(e.stderr, "fetch-ancestry: git ls-remote %s failed: %s\n", branch, selWhy(res.Stderr, res.Code, err))
			return 1
		}
	}
	shallow, err := h.run(root, nil, "git", "rev-parse", "--is-shallow-repository")
	if err != nil || shallow.Code != 0 {
		fmt.Fprintf(e.stderr, "fetch-ancestry: git rev-parse --is-shallow-repository failed: %s\n", selWhy(shallow.Stderr, shallow.Code, err))
		return 1
	}
	argv := []string{"git", "fetch", "--no-tags", "--filter=blob:none"}
	if strings.TrimSpace(shallow.Stdout) == "true" {
		argv = append(argv, "--unshallow")
	}
	argv = append(argv, "origin", "+"+branch+":refs/remotes/origin/"+branch)
	code, err := h.stream(root, nil, e.stdout, e.stderr, argv...)
	if err != nil {
		fmt.Fprintf(e.stderr, "fetch-ancestry: cannot run git: %v\n", err)
		return 1
	}
	if code != 0 {
		fmt.Fprintf(e.stderr, "fetch-ancestry: %s exited %d\n", strings.Join(argv, " "), code)
		return 1
	}
	return 0
}

// selWhy says why a captured command failed: its start error, else its stderr,
// else its status.
func selWhy(stderr string, code int, err error) string {
	switch {
	case err != nil:
		return err.Error()
	case strings.TrimSpace(stderr) != "":
		return fmt.Sprintf("exit %d: %s", code, strings.TrimSpace(stderr))
	}
	return fmt.Sprintf("exit %d", code)
}
