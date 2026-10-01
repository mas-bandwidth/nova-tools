package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// twinSprint is member.Sprint over a twin file: each verb one process of
// nova-sprint, as the member's execSprint runs it, with the member's actor.
type twinSprint struct {
	file, actor string
}

func (s twinSprint) Run(args ...string) (int, []byte) {
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + s.file, "NOVA_SPRINT_ACTOR": s.actor}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	var out, errb bytes.Buffer
	code := a.run(args, &out, &errb)
	if code != 0 && out.Len() == 0 {
		return code, errb.Bytes()
	}
	return code, out.Bytes()
}

// twinChild is a child the test ends by hand; twinRunner hands one per card.
type twinChild struct {
	mu   sync.Mutex
	done bool
	res  member.Result
}

func (c *twinChild) Done() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.done }
func (c *twinChild) Result() member.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.res
}

type twinRunner struct {
	packets  []member.Packet
	children map[string]*twinChild
}

func (r *twinRunner) Start(p member.Packet) (member.Child, error) {
	c := &twinChild{}
	r.packets = append(r.packets, p)
	r.children[p.Card] = c
	return c, nil
}

// twinPusher pushes nothing anywhere: it records what it was asked and
// answers the push given.
type twinPusher struct {
	asked []member.Packet
	push  member.Push
}

func (p *twinPusher) Push(pk member.Packet, _ member.Result) member.Push {
	p.asked = append(p.asked, pk)
	return p.push
}

// twinMemberFlow runs one card through the twin with the member loop doing
// the take and the finish, the push answered by pu; it returns the twin file,
// the card's work branch and the member's output.
func twinMemberFlow(t *testing.T, pu *twinPusher) (file, branch, out string) {
	t.Helper()
	file = filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range []string{
		"nova-sprint init --readers reader-a,reader-b --members m1",
		"nova-sprint add --stream s1 --count 1",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
	} {
		code, o, e := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s\n%s%s", line, o, e)
	}
	rn := &twinRunner{children: map[string]*twinChild{}}
	var log bytes.Buffer
	m := member.New(member.Config{As: "m1", Width: 1}, twinSprint{file: file, actor: "m1"}, rn, pu, &log)
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err, log.String())
	require.Len(t, rn.packets, 1, "the member took the card: %s", log.String())
	p := rn.packets[0]
	require.NotEmpty(t, p.Branch, "the sprint names the work's branch in the packet")
	c := rn.children[p.Card]
	c.mu.Lock()
	c.done, c.res = true, member.Result{Ran: true, OK: true, Head: "0123456", Report: "did the work"}
	c.mu.Unlock()
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err, log.String())
	require.Len(t, pu.asked, 1, "the ended card was pushed once: %s", log.String())
	assert.Equal(t, p.Branch, pu.asked[0].Branch)
	return file, p.Branch, log.String()
}

// The member's push on the twin: the finish records the pushed sha as the
// card's head and the work branch as its branch, and the merge queue the
// landing reads carries that head, so the coordinator finds the commit on
// origin's branch from any machine.
func TestTheMembersPushIsWhatTheMergeQueueCarries(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	file, branch, out := twinMemberFlow(t, &twinPusher{push: member.Push{Sha: sha}})
	assert.Contains(t, out, "push s1-1.w1 pushed="+sha+" branch="+branch)

	code, story, e := twinProcess(t, file, "nova-sprint card s1-1")
	require.Equal(t, 0, code, e)
	assert.Contains(t, story, "head "+sha)
	assert.Contains(t, story, "branch "+branch)
	assert.Contains(t, story, "pushed="+sha+" to "+branch+": did the work")

	for _, line := range []string{
		"nova-sprint tick",
		"nova-sprint read --as reader-a --begin --epoch 0",
		"nova-sprint read --as reader-b --begin --epoch 0",
		"nova-sprint read --as reader-a --ok --epoch 0",
		"nova-sprint read --as reader-b --ok --epoch 0",
		"nova-sprint tick",
	} {
		code, o, e := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s\n%s%s", line, o, e)
	}
	code, q, e := twinProcess(t, file, "nova-sprint queue --stream s1 --json")
	require.Equal(t, 0, code, e)
	var got struct {
		Cards []struct {
			ID, Head string
		} `json:"cards"`
	}
	require.NoError(t, json.Unmarshal([]byte(q), &got), q)
	require.Len(t, got.Cards, 1, q)
	assert.Equal(t, sha, got.Cards[0].Head, "the merge queue carries the pushed head")
}

// A push the member could not make is a failed finish on the twin: the card
// is in review as failed, with git's line first in its report, never done.
func TestARefusedPushIsAFailedCardOnTheTwin(t *testing.T) {
	t.Parallel()
	line := "fatal: could not read Username for 'https://forge.example': terminal prompts disabled"
	file, _, out := twinMemberFlow(t, &twinPusher{push: member.Push{Refused: line}})
	assert.Contains(t, out, "NOTE push s1-1.w1 refused: "+line)
	assert.Contains(t, out, "finish s1-1.w1 ok=false exit=0")
	code, story, e := twinProcess(t, file, "nova-sprint card s1-1")
	require.Equal(t, 0, code, e)
	assert.Contains(t, story, "push refused: "+line+"; did the work")
	assert.True(t, strings.Contains(story, "failed"), "the card's story says it failed:\n%s", story)
}
