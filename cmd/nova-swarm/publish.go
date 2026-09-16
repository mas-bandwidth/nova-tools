package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// PUBLISH (slice 7, lesson 6). A job's clone is pushed to the shared remote and a draft pull
// request is opened, both by this tool, never by a bare `git push`. The only push it makes is
// by explicit refspec -- `git push origin HEAD:refs/heads/<branch>` -- so the branch moved is
// the one the caller named and origin/main never is. Before anything is moved the tool
// refuses a clone that is on main or on the base, a diff that touches a file the caller did
// not admit with --touched, and a clone with nothing committed ahead of the base.

func cmdPublish(args []string, stdout, stderr io.Writer) int {
	f := newFlags("publish")
	job := f.fs.String("job", "", "")
	branch := f.fs.String("branch", "", "")
	base := f.fs.String("base", "", "")
	title := f.fs.String("title", "", "")
	bodyFile := f.fs.String("body-file", "", "")
	touched := f.fs.String("touched", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*job, "job", "the clone directory to push, which is a git working tree")
	f.want(*branch, "branch", "the name of the remote branch to push to (a topic branch, never main)")
	f.want(*base, "base", "the branch the pull request is against, e.g. main")
	f.want(*title, "title", "the pull request title")
	f.want(*bodyFile, "body-file", "a file holding the pull request body")
	if f.refused(stderr) {
		return 2
	}
	var admitted map[string]bool
	if strings.TrimSpace(*touched) != "" {
		admitted = map[string]bool{}
		for _, path := range strings.Split(*touched, ",") {
			if p := strings.TrimSpace(path); p != "" {
				admitted[p] = true
			}
		}
	}

	// (1) THE CLONE IS NOT ON MAIN OR ON THE BASE. A clone sitting on main has nothing
	// about it that is safely a topic; a clone sitting on the base is the same branch the
	// pull request is against.
	head, err := gitOut(*job, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return refusePublish(stderr, fmt.Sprintf("the clone at %s is not a git working tree: %s", oneline.Field(*job), oneline.Err(err)))
	}
	here := strings.TrimSpace(head)
	if here == "main" || here == *base || here == "refs/heads/"+*base {
		return refusePublish(stderr, fmt.Sprintf("the clone is on %s, which is main or the --base %s; publish wants a topic branch ahead of the base", oneline.Field(here), oneline.Field(*base)))
	}

	// (2) A COMMIT AHEAD OF THE BASE EXISTS. Nothing committed ahead of origin/<base> is a
	// clone that asked to be pushed with no work to carry.
	behind, err := gitOut(*job, "rev-list", "--count", "origin/"+*base+"..HEAD")
	if err != nil {
		return refusePublish(stderr, fmt.Sprintf("origin/%s is not a ref this clone knows: %s", oneline.Field(*base), oneline.Err(err)))
	}
	if strings.TrimSpace(behind) == "0" {
		return refusePublish(stderr, fmt.Sprintf("nothing is committed ahead of origin/%s, so there is nothing to publish", oneline.Field(*base)))
	}

	if admitted != nil {
		// (3) --touched ADMITS EVERY TOUCHED FILE. The diff ahead of the base may touch only
		// files the caller named, so a change to a file nobody admitted is caught before the
		// push rather than surfaced in someone's review inbox. The diff is three-dot
		// (origin/<base>...HEAD), read from the merge base, so it is the topic's own work:
		// the two-dot tip-to-tip form counts every commit main gained since the fork as a
		// change the topic made, refusing files the topic never touched.
		diff, err := gitOut(*job, "diff", "--name-only", "origin/"+*base+"...HEAD")
		if err != nil {
			return refusePublish(stderr, fmt.Sprintf("the diff ahead of origin/%s could not be read: %s", oneline.Field(*base), oneline.Err(err)))
		}
		for _, line := range strings.Split(diff, "\n") {
			path := strings.TrimSpace(line)
			if path == "" {
				continue
			}
			if !admitted[path] {
				return refusePublish(stderr, fmt.Sprintf("the diff ahead of origin/%s touches %s, which --touched does not name", oneline.Field(*base), oneline.Field(path)))
			}
		}
	}

	// (4) THE PUSH, BY REFSPEC, NEVER BARE. The only branch moved is the one the caller
	// named, spelled HEAD:refs/heads/<branch>, and origin/main is never in the refspec.
	if err := gitRun(*job, "push", "origin", "HEAD:refs/heads/"+*branch); err != nil {
		return refusePublish(stderr, fmt.Sprintf("the push to refs/heads/%s failed: %s", oneline.Field(*branch), oneline.Err(err)))
	}
	sha, err := gitOut(*job, "rev-parse", "HEAD")
	if err != nil {
		return refusePublish(stderr, fmt.Sprintf("HEAD could not be read after the push: %s", oneline.Err(err)))
	}

	// (5) THE DRAFT PULL REQUEST. gh is run in the clone, and it is asked for a draft, so
	// nothing published here is open for merge without a person asking.
	url, err := ghOut(*job, "pr", "create", "--draft", "--base", *base, "--head", *branch, "--title", *title, "--body-file", *bodyFile)
	if err != nil {
		return refusePublish(stderr, fmt.Sprintf("gh pr create --draft failed: %s", oneline.Err(err)))
	}

	fmt.Fprintf(stdout, "PUBLISH OK branch=%s head=%s pr=%s\n",
		oneline.Field(*branch), oneline.Field(strings.TrimSpace(sha)), oneline.Field(strings.TrimSpace(url)))
	return 0
}

// refusePublish writes the one REFUSED line the publish verb owes its caller.
func refusePublish(w io.Writer, reason string) int {
	fmt.Fprintf(w, "PUBLISH REFUSED: %s\n", oneline.Escape(reason))
	return 2
}

// gitOut runs git in the job's clone and returns its trimmed standard output, or the error
// when git could not run or exited non-zero.
func gitOut(job string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", job}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(errb.String()), err)
	}
	return out.String(), nil
}

// gitRun runs git in the job's clone, discarding output, and returns any error.
func gitRun(job string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", job}, args...)...)
	var errb bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(errb.String()), err)
	}
	return nil
}

// ghOut runs the gh CLI in the job's clone and returns its trimmed standard output, or the
// error when gh could not run or exited non-zero.
func ghOut(job string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = job
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(errb.String()), err)
	}
	return out.String(), nil
}
