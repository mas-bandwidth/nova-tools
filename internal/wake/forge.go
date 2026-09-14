package wake

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// The amendment of 2026-09-13 adds four sources, and three of them read a forge
// through `gh`. What they share is here rather than spelled three times: one
// process under one timeout, the exit-code and stderr reading that turns a dead
// call into an `unreadable:<reason>` VALUE, and the counter that puts the spend
// on the record as `calls=` beside the news.
//
// The limit is the FORGE's, reported by gh, and not a number this spec owns.
// What this tool promises is that the spend is visible: every WAKE SOURCE line
// for a forge source carries calls=, so a rate limit arrives as `unreadable:`
// rather than as silence.

// ForgeBatch is how many gh calls may be outstanding at once, for the sources
// that poll several things on one tick. It is EntryBatch's 8, because the pool
// the batch spends is the same pool.
const ForgeBatch = EntryBatch

// calls counts the gh invocations one source made this run.
type calls struct{ n int64 }

func (c *calls) mark()      { atomic.AddInt64(&c.n, 1) }
func (c *calls) Calls() int { return int(atomic.LoadInt64(&c.n)) }

// gh runs one gh invocation under a budget and answers its stdout. A call that
// could not be made at all is an error carrying the program's own words, which
// becomes the source's `unreadable:<reason>` value: a state value like any
// other, a change once and standing thereafter.
func gh(ctx context.Context, timeout time.Duration, c *calls, args ...string) ([]byte, error) {
	if c != nil {
		c.mark()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	raw, err := cmd.Output()
	if err == nil {
		return raw, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("gh timed out after %s", Dur(timeout))
	}
	var ee *exec.ExitError
	detail := err.Error()
	if ok := asExit(err, &ee); ok && len(ee.Stderr) > 0 {
		detail = strings.TrimSpace(string(ee.Stderr))
	}
	return nil, fmt.Errorf("gh: %s", oneLineOf(detail))
}

// notFound reads a 404 out of gh's own complaint. It is the one forge answer
// this tool reads as a STATE rather than as a failure: a branch that is not
// there is `absent`, which is the answer and not the absence of one.
func notFound(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "404") || strings.Contains(strings.ToLower(text), "not found")
}

// withWas composes a two-ended value: what the thing reads now and what it read
// before. Both ends are on the line because the caller of --lock is waiting for
// one transition and the caller of --ref wants to see a force-push.
//
// The stored form is what is COMPARED, so it has to be stable while nothing
// moves: a poll that finds the same reading returns the stored value byte for
// byte, and only a poll that finds a different one writes a new pair. Composing
// <now>|<the stored now> instead would make every second poll of an unchanged
// lock a change, which is the false wake of 2026-09-11 in new clothes.
func withWas(prev func(key string) (string, bool), key, now string) string {
	stored, ok := prev(key)
	if !ok || stored == "" {
		return Compose(now, "")
	}
	p := Decompose(stored)
	was := p[0]
	if len(p) == 2 && p[0] == "unreadable" {
		was = "unreadable"
	}
	if was == now {
		return stored
	}
	return Compose(now, was)
}

// SplitRef reads --ref's <owner/repo>:<name>, and says what the flag WANTS when
// it cannot. The colon is the separator because # names an entry and @ names a
// head, and a reader of a transcript should be able to tell the three apart at
// a glance.
func SplitRef(name string) (repo, branch string, err error) {
	repo, branch, ok := strings.Cut(name, ":")
	if !ok || repo == "" || branch == "" || strings.Count(repo, "/") != 1 {
		return "", "", fmt.Errorf("a branch is <owner>/<repo>:<name>, the repository included, as in mas-bandwidth/nova-tools:main")
	}
	return repo, branch, nil
}

// SplitHead reads --run's <owner/repo>@<sha>.
func SplitHead(name string) (repo, sha string, err error) {
	repo, sha, ok := strings.Cut(name, "@")
	if !ok || repo == "" || sha == "" || strings.Count(repo, "/") != 1 {
		return "", "", fmt.Errorf("a head is <owner>/<repo>@<sha>, the repository included, as in mas-bandwidth/nova-tools@2c164b00")
	}
	return repo, sha, nil
}

// SplitPR reads --pr's <owner/repo>#<n>. It is SplitEntry's shape for
// SplitEntry's reason: the repository is part of the name.
func SplitPR(name string) (repo, number string, err error) {
	repo, number, err = SplitEntry(name)
	if err != nil {
		return "", "", fmt.Errorf("a pull request is <owner>/<repo>#<number>, the repository included, as in mas-bandwidth/nova-tools#239")
	}
	return repo, number, nil
}

// ownerRepo splits <owner>/<repo> into its halves for a REST path.
func ownerRepo(repo string) (owner, name string) {
	owner, name, _ = strings.Cut(repo, "/")
	return owner, name
}
