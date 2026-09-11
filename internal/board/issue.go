/*
issue.go is the issue-comment backend: one event per comment, in comment order, read and
appended with `gh`. It is where today's board lives, and it is chosen for a good reason —
every line in this family can already read and write it, from any machine, with no clone
and no write access to a repo's branches.

`gh` stays, NAMED, under a timeout: a subprocess that never returns is a tool that has
stopped saying anything, which from the outside is indistinguishable from a tool that is
working. `jq` does not stay; the prototype had it as a second hard dependency and this is
encoding/json.

The read is the WHOLE thread, fully paginated. The prototype's `.[-40:]` is the window this
spec forbids.
*/
package board

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// THERE IS NO DEFAULT BUDGET. Rule 8 -- no default paths and no default durations -- names
// durations as well as paths, and this backend's budget is a duration: it comes from
// --gh-timeout or the caller does not get a backend. A minute nobody chose is a tool that
// hangs for a minute a reader never agreed to.

// killGrace is how long after the kill the pipes are closed. gh's children inherit the
// output pipe and hold it open after gh is gone, so a killed call still blocks until they
// let go; WaitDelay is what makes the budget real.
const killGrace = 2 * time.Second

// Issue is the issue-comment backend.
type Issue struct {
	owner, repo string
	number      int
	timeout     time.Duration
	// run is the subprocess, injected so the tests drive a recorded fixture rather than
	// the network.
	run func(ctx context.Context, stdin string, args ...string) (string, error)

	// cached holds the log for the rest of this RUN and nothing longer. It is invalidated
	// by this tool's own append, so a close's read-before-append is never answered from
	// bytes read before this run wrote. See backend.go for why a time-to-live cache is
	// the failure this tool exists to close rather than a performance trade.
	cached *Log
	Reads  int // how many times the thread was actually fetched, for the cache's test
}

// ParseIssue reads the `<owner>/<repo>#<n>` a caller names.
//
// The three parts are checked rather than pasted: they become gh's own argv, so a value
// beginning with a dash is an OPTION to gh and not a repository, and this tool would run
// it. The charset is deliberately narrow — the cost of being narrow is a refusal a person
// reads, and the cost of being wide is a shell.
func ParseIssue(spec string) (owner, repo string, number int, err error) {
	want := "--issue wants <owner>/<repo>#<number>, as in mas-bandwidth/schema#876"
	left, right, ok := strings.Cut(spec, "#")
	if !ok {
		return "", "", 0, fmt.Errorf("%s; %q has no #", want, spec)
	}
	owner, repo, ok = strings.Cut(left, "/")
	if !ok {
		return "", "", 0, fmt.Errorf("%s; %q has no /", want, spec)
	}
	number, err = strconv.Atoi(right)
	if err != nil || number < 1 {
		return "", "", 0, fmt.Errorf("%s; %q is not an issue number", want, right)
	}
	for _, part := range []string{owner, repo} {
		if part == "" || strings.HasPrefix(part, "-") || strings.Trim(part, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.") != "" {
			return "", "", 0, fmt.Errorf("%s; %q is not a repository name", want, part)
		}
	}
	return owner, repo, number, nil
}

// NewIssue returns the backend for one issue thread.
func NewIssue(spec string, timeout time.Duration) (*Issue, error) {
	owner, repo, number, err := ParseIssue(spec)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("--gh-timeout wants how many SECONDS one gh call may take and is at least 1, as in --gh-timeout 60; there is no default duration")
	}
	return &Issue{owner: owner, repo: repo, number: number, timeout: timeout, run: runGH}, nil
}

// Source is what the listing's source= field carries.
func (i *Issue) Source() string { return fmt.Sprintf("%s/%s#%d", i.owner, i.repo, i.number) }

// comment is the one field of a comment this tool reads.
type comment struct {
	Body string `json:"body"`
}

// Events reads the whole thread, fully paginated, one event per comment.
func (i *Issue) Events() (Log, error) {
	if i.cached != nil {
		return *i.cached, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), i.timeout)
	defer cancel()
	i.Reads++
	out, err := i.run(ctx, "", "api", "--paginate",
		fmt.Sprintf("repos/%s/%s/issues/%d/comments", i.owner, i.repo, i.number))
	if err != nil {
		return Log{}, err
	}
	log := Log{}
	// --paginate concatenates one JSON array per page, so the decoder is run in a loop
	// rather than over one document. A page that will not decode is an error and never a
	// short board: a truncated read is the failure this whole file is written against.
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var page []comment
		if err := dec.Decode(&page); err != nil {
			if err == io.EOF {
				break
			}
			return Log{}, fmt.Errorf("reading the comments of %s: %w", i.Source(), err)
		}
		for _, c := range page {
			for _, line := range strings.Split(strings.ReplaceAll(c.Body, "\r\n", "\n"), "\n") {
				if strings.TrimSpace(line) != "" {
					log.Lines = append(log.Lines, line)
				}
			}
		}
	}
	i.cached = &log
	return log, nil
}

// Append posts one comment carrying one event line. The body goes in on STDIN rather than
// on the command line, so nothing an event holds is ever an argument to gh.
//
// CREATION IS EXCLUSIVE HERE TOO. A card line is appended only after the thread is RE-READ
// — not from this run's cache — and the id looked for: the forge append is the only serial
// point this backend has, so this re-read is its half of the O_EXCL the directory backend
// gets from the filesystem. An id that already exists is a hand-made comment, a copied one,
// or a broken random source, and it is ErrExists with nothing posted, never a second card
// under one id folded silently into the first.
func (i *Issue) Append(line string) error {
	if e, ok := Parse(line); ok && e.Verb == "card" {
		i.cached = nil
		log, err := i.Events()
		if err != nil {
			return err
		}
		for _, held := range log.Lines {
			if h, ok := Parse(held); ok && h.Verb == "card" && h.ID == e.ID {
				return ErrExists
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), i.timeout)
	defer cancel()
	i.cached = nil
	_, err := i.run(ctx, line+"\n", "issue", "comment", strconv.Itoa(i.number),
		"--repo", i.owner+"/"+i.repo, "--body-file", "-")
	return err
}

// ghBinary is the program runGH executes. It is a value and not the bare word "gh"
// because a test must be able to name ITS OWN recorded gh by an absolute path.
//
// WHY THE PATH AND NOT PATH. Putting a fake first on $PATH is a LOOKUP, and a lookup has
// a platform in it: on Windows a program built without the executable suffix is not a
// program the lookup will find, so the fake was skipped, the search fell through to the
// runner's real gh, and a test reached the network — which CONTRIBUTING forbids, and
// which the tool cannot notice from the inside because a real gh fails like any other
// failing subprocess. Naming the path leaves nothing to the lookup, on any platform.
//
// The default is still the bare name, because for a PERSON gh is whatever their PATH
// says it is, and this tool has no business preferring one install over theirs.
var ghBinary atomic.Value // string

func init() { ghBinary.Store("gh") }

// SetGHBinary points this backend at one gh and returns the restore, for a test to hand
// to t.Cleanup. It is exported for the tests of the command that drives this backend
// in-process; nothing in the tool itself calls it.
func SetGHBinary(path string) (restore func()) {
	previous := ghBinary.Load().(string)
	ghBinary.Store(path)
	return func() { ghBinary.Store(previous) }
}

// runGH is the real subprocess.
func runGH(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, ghBinary.Load().(string), args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(cmd.Environ(), "GH_PAGER=cat", "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	cmd.WaitDelay = killGrace
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("gh %s did not finish in time and was killed; nothing was left half-done by this tool, and a longer budget is --gh-timeout <seconds>", strings.Join(args, " "))
	}
	if err != nil {
		return string(out), fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
