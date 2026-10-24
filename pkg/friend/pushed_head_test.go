package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	stagedSha = "1111111111111111111111111111111111111111"
	pushOne   = "2222222222222222222222222222222222222222"
	pushTwo   = "3333333333333333333333333333333333333333"
)

// stagedJob lays out a staged job the way Stage leaves it, with no git run: JOB.md naming
// the staged commit, and the checkout's repository holding origin's remote-tracking ref of
// the branch at that commit (a branch carried from an attempt before: origin holds it).
func stagedJob(t *testing.T, dir, job, branch string) {
	t.Helper()
	jd := JobDir(dir, job)
	require.NoError(t, os.MkdirAll(filepath.Join(jd, "repo", ".git"), 0o755))
	p := Packet{Card: strings.Split(job, "~")[0], Job: job, Repo: "owner/name", Base: "main", Branch: branch}
	require.NoError(t, os.WriteFile(filepath.Join(jd, JobFile), []byte(JobText(dir, p, stagedSha)), 0o644))
	pushed(t, dir, job, branch, stagedSha)
}

// pushed is what a push git took writes: origin's remote-tracking ref of the branch.
func pushed(t *testing.T, dir, job, branch, sha string) {
	t.Helper()
	ref := filepath.Join(JobDir(dir, job), "repo", ".git", "refs", "remotes", "origin", filepath.FromSlash(branch))
	require.NoError(t, os.MkdirAll(filepath.Dir(ref), 0o755))
	require.NoError(t, os.WriteFile(ref, []byte(sha+"\n"), 0o644))
}

// progresses is a Progress that records every stamp and answers with answer (nil: taken).
type progresses struct {
	mu     sync.Mutex
	cards  []Card
	answer func(n int) error
}

func (p *progresses) progress(_ context.Context, cs []Card) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cards = append(p.cards, cs...)
	if p.answer != nil {
		return p.answer(len(p.cards))
	}
	return nil
}

func (p *progresses) heads() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, c := range p.cards {
		if c.Head != "" {
			out = append(out, c.ID+" "+c.Head)
		}
	}
	return out
}

// The daemon says each new pushed head of a working card on her row once, with progress
// --head, and never the commit the job was staged at: a carried branch is no push of this
// attempt. A read, and a card not working, are never said. The clock is the rig's: a second
// a step.
func TestTheDaemonSaysEachPushedHeadOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	p := &progresses{}
	r.d.Progress = p.progress

	work := workCard("pushes.w1", "working")
	work.Branch = "sprint/pushes.w1.g1.e15" // the server's word for it: read in the staged checkout
	viaBrief := workCard("brief.w1", "working")
	viaBrief.Gen, viaBrief.Job = 2, "brief.w1~15.g2" // no branch from the server: the brief's STATUS line names it
	ready := workCard("ready.w1", "ready")
	read := workCard("read.r1.x", "working")
	read.Kind = "read"
	for _, c := range []HeldCard{work, viaBrief, ready, read} {
		inboxJob(t, dir, c.Job, c.Brief)
	}
	stagedJob(t, dir, work.Job, "sprint/pushes.w1.g1.e15")
	stagedJob(t, dir, viaBrief.Job, "sprint/brief.w1.g1.e15")
	for _, c := range []HeldCard{ready, read} {
		stagedJob(t, dir, c.Job, "sprint/"+c.Card+".g1.e15")
		pushed(t, dir, c.Job, "sprint/"+c.Card+".g1.e15", pushOne)
	}
	row.set(work, viaBrief, ready, read)

	r.at[5] = func() {
		pushed(t, dir, work.Job, "sprint/pushes.w1.g1.e15", pushOne)
		pushed(t, dir, viaBrief.Job, "sprint/brief.w1.g1.e15", pushOne)
	}
	r.at[45] = func() { pushed(t, dir, work.Job, "sprint/pushes.w1.g1.e15", pushTwo) }
	r.run(t, 100)

	assert.ElementsMatch(t, []string{"pushes.w1 " + pushOne, "brief.w1 " + pushOne, "pushes.w1 " + pushTwo}, p.heads(),
		"each new head once, never the staged commit, never a read's or a card not working")
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.cards {
		if c.ID == "brief.w1" {
			assert.Equal(t, [][]string{{"progress", "--as", "friend.bob", "brief.w1@2", "--epoch", "15", "--head", pushOne}}, ProgressArgv("bob", []Card{c}), "the card at the generation and epoch the server holds it")
		}
	}
}

// The report is best effort: a head the server refuses is said on the record once and not
// sent again; one it did not answer is said and sent again after ProgressEvery. Neither
// finishes, fails or touches the card.
func TestAPushedHeadNotTakenIsSaidNotFailed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		answer error
		sent   int
		said   string
	}{
		{"refused", &Refused{Why: "card refused.w1 is not working"}, 1, "refused by the server: card refused.w1 is not working"},
		{"unanswered", errors.New("the server did not answer"), 2, "not said: the server did not answer; tried again in 3m0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			dir := r.d.Dir
			row := &twinRow{}
			r.d.Held = row.held
			f := &finishes{}
			r.d.Finish = f.finish
			p := &progresses{answer: func(n int) error {
				if n == 1 {
					return tc.answer
				}
				return nil
			}}
			r.d.Progress = p.progress
			c := workCard("refused.w1", "working")
			inboxJob(t, dir, c.Job, c.Brief)
			stagedJob(t, dir, c.Job, "sprint/refused.w1.g1.e15")
			pushed(t, dir, c.Job, "sprint/refused.w1.g1.e15", pushOne)
			row.set(c)
			r.run(t, 30+int(ProgressEvery.Seconds())+40)

			assert.Len(t, p.heads(), tc.sent)
			assert.Empty(t, f.got(), "no finish comes of a head report")
			n := 0
			for _, l := range r.records {
				if strings.Contains(l, "pushed: card refused.w1 head "+pushOne+" "+tc.said) {
					n++
				}
			}
			assert.Equal(t, 1, n, "%v", r.records)
		})
	}
}

// A card with a pushed head is its own progress verb, --head last, its lane named when a lane
// holds it; the cards with none are stamped by epoch as before.
func TestProgressArgvSendsTheHead(t *testing.T) {
	t.Parallel()
	plain := Card{ID: "c1", Outbox: filepath.Join("x", "outbox", "c1~15")}
	headed := Card{ID: "c2", Outbox: filepath.Join("x", "outbox", "c2~15"), ServerEpoch: "15", ServerGen: 2, Head: pushOne}
	laned := Card{ID: "c3", Lane: 2, ServerEpoch: "16", ServerGen: 1, Head: pushTwo}
	assert.Equal(t, [][]string{
		{"progress", "--as", "friend.bob", "c2@2", "--epoch", "15", "--head", pushOne},
		{"progress", "--as", "friend.bob", "c3@1", "--epoch", "16", "--lane", "2", "--head", pushTwo},
		{"progress", "--as", "friend.bob", "c1", "--epoch", "15"},
	}, ProgressArgv("bob", []Card{plain, headed, laned}))
}
