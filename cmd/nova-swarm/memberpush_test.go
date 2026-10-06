package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// pushBench is one member root with an origin (a bare repository with main
// at one commit), a work card c1 whose brief names that origin, and the
// card's checkout where native stages it: <slots>/<launch>/jobs/c1/repo, a
// clone of origin on the branch staging names.
type pushBench struct {
	root, slots, origin, checkout, base string
	p                                   member.Packet
}

// gitAs is a git with an identity, for the commits a test makes.
func gitAs(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...))
}

func newPushBench(t *testing.T) *pushBench {
	t.Helper()
	root := t.TempDir()
	b := &pushBench{root: root, slots: filepath.Join(root, "slots"), origin: filepath.Join(root, "origin.git")}
	seed := filepath.Join(root, "seed")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	require.NoError(t, os.WriteFile(filepath.Join(seed, "f"), []byte("base\n"), 0o644))
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, b.origin)
	b.base = gitAs(t, b.origin, "rev-parse", "main")
	b.p = member.Packet{Card: "c1", Kind: "work", As: "m1", Primary: "p1", Stream: "s1", Attempt: 1, Gen: 1, Epoch: 7,
		Brief: "RESULT: c1\nbase-repo: " + b.origin + "\nBASE: main\n\nDo the work.", Branch: "sprint/c1"}
	b.checkout = filepath.Join(b.slots, launchName(b.p), "jobs", "c1", swarm.JobRepo)
	require.NoError(t, os.MkdirAll(filepath.Dir(b.checkout), 0o755))
	runGit(t, "", "clone", "-q", "--", b.origin, b.checkout)
	gitAs(t, b.checkout, "switch", "-q", "-c", "rowan/c1")
	b.staged(t, b.base)
	return b
}

// staged records the commit native staged the launch at, in the slot, as
// native does (cardcontract.StagedName).
func (b *pushBench) staged(t *testing.T, sha string) {
	t.Helper()
	write(t, filepath.Join(b.slots, launchName(b.p), "staged"), sha+"\n")
}

// commit is a commit of the child's in the checkout; its sha.
func (b *pushBench) commit(t *testing.T, text string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(b.checkout, "f"), []byte(text), 0o644))
	gitAs(t, b.checkout, "commit", "-q", "-am", text)
	return gitAs(t, b.checkout, "rev-parse", "HEAD")
}

// secondLine is a second line of work in the checkout: a branch from the staged commit with a
// commit of its own; the checkout is left on rowan/c1. Its sha.
func (b *pushBench) secondLine(t *testing.T) string {
	t.Helper()
	gitAs(t, b.checkout, "switch", "-q", "-c", "other", b.base)
	other := b.commit(t, "another line\n")
	gitAs(t, b.checkout, "switch", "-q", "rowan/c1")
	return other
}

// originHas is the sha origin's branch holds, "" when it has none.
func (b *pushBench) originHas(t *testing.T, branch string) string {
	t.Helper()
	out := strings.TrimSpace(runGit(t, b.origin, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch))
	return out
}

func (b *pushBench) pusher() *gitPusher { return newGitPusher(b.root, b.slots) }

// recordGit is a git that records every argv and directory it is asked for
// and runs it, or refuses a push with the line given.
type recordGit struct {
	mu         sync.Mutex
	calls      []gitrun.Options
	argv       [][]string
	refusePush string
}

func (r *recordGit) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	r.mu.Lock()
	r.calls, r.argv = append(r.calls, o), append(r.argv, slices.Clone(args))
	r.mu.Unlock()
	if r.refusePush != "" && slices.Contains(args, "push") {
		return gitrun.Result{Stderr: []byte("To the origin\n" + r.refusePush + "\n")}, assert.AnError
	}
	return gitrun.Run(ctx, o, args...)
}

// rejectGit is a git whose first n pushes origin rejects on its own side, git's
// `[remote rejected]` line; every other git, and the pushes after, run.
type rejectGit struct {
	mu     sync.Mutex
	reject int
	pushes int
}

func (r *rejectGit) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	if slices.Contains(args, "push") {
		r.mu.Lock()
		r.pushes++
		rejected := r.pushes <= r.reject
		r.mu.Unlock()
		if rejected {
			return gitrun.Result{Stdout: []byte("To the origin\n!\t0123:refs/heads/sprint/c1\t[remote rejected] (failed)\nDone\n")}, assert.AnError
		}
	}
	return gitrun.Run(ctx, o, args...)
}

// badFetchGit is a git whose first n fetches into the push repository fail as one did on the
// 5000-card load test (2026-10-01), on another launch's ref, while the bench mirror the push
// repository borrows objects from was being repacked; every other git, and the fetches
// after, run. Failed tries first run the real fetch, then inject an error after refs
// exist. It records those refs and the refs held before each fetch and push.
type badFetchGit struct {
	mu               sync.Mutex
	fail             int
	fetches          int
	refsAtPush       []string
	refsAtFetch      []string
	refsAfterFailure [][]string
}

const badFetchLine = "fatal: bad object refs/member/c9.w1.g1.e7/HEAD"

func (r *badFetchGit) run(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
	held := func() []string {
		res, err := gitrun.Run(ctx, gitrun.Options{C: o.C}, "for-each-ref", "--format=%(refname)", "refs/member/")
		if err != nil {
			return []string{"for-each-ref: " + err.Error()}
		}
		return strings.Fields(string(res.Stdout))
	}
	switch {
	case slices.Contains(args, "fetch"):
		r.mu.Lock()
		r.fetches++
		failed := r.fetches <= r.fail
		r.refsAtFetch = append(r.refsAtFetch, held()...)
		r.mu.Unlock()
		res, err := gitrun.Run(ctx, o, args...)
		if err != nil {
			return res, err // a real fetch failure is never replaced by the injected one
		}
		if failed {
			r.mu.Lock()
			r.refsAfterFailure = append(r.refsAfterFailure, held())
			r.mu.Unlock()
			return gitrun.Result{Stderr: []byte(badFetchLine + "\n")}, assert.AnError
		}
		return res, nil
	case slices.Contains(args, "push"):
		r.mu.Lock()
		r.refsAtPush = append(r.refsAtPush, held()...)
		r.mu.Unlock()
	}
	return gitrun.Run(ctx, o, args...)
}

// wrongTail is a sha whose first twelve characters are head's and whose tail is invented, as
// a cheap model wrote one on the 1000-card load test (2026-10-01).
func wrongTail(head string) string {
	tail := "0123456789abcdef0123456789ab"
	if head[12:] == tail {
		tail = "ba9876543210fedcba9876543210"
	}
	return head[:12] + tail
}
