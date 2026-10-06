package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// External operands of DEPENDS-ON on the twin store (external.go, tick_external.go;
// tla/CardISA.tla, IsaTick and Wait): a card that waits for `pr <repo>#<n> merged`,
// `<branch> contains <sha>` or `after <RFC3339>` is admitted waiting and released by the
// tick the first tick its operand holds, never before; each distinct operand is asked once
// a tick however many cards wait on it; a malformed operand is refused at add.

// fakeOutside is the outside the tick asks: which pull requests have merged and which
// branches contain which commits, and how many times each operand was asked.
type fakeOutside struct {
	mu     sync.Mutex
	merged map[string]bool // "<repo>#<n>"
	on     map[string]bool // "<branch> <sha>"
	asks   map[string]int  // "<repo> <operand>"
}

func (f *fakeOutside) ask(_ context.Context, op swarm.DependsOperand, repo string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asks[repo+" "+op.Text]++
	switch op.Form {
	case swarm.OperandPRMerged:
		return f.merged[fmt.Sprintf("%s#%d", op.Repo, op.N)], nil
	case swarm.OperandContains:
		return f.on[op.Branch+" "+op.SHA], nil
	}
	return false, fmt.Errorf("%s is not asked", op.Text)
}

func (f *fakeOutside) set(m map[string]bool, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m[key] = true
}

func (f *fakeOutside) asked() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for k, v := range f.asks {
		out[k] = v
	}
	return out
}

// externalBrief is a card's brief whose DEPENDS-ON line is depends.
func externalBrief(depends string) string {
	return "c: an external wait tier: pro\nREPO: mas-bandwidth/nova-tools\nDEPENDS-ON: " + depends + "\n\nThe task."
}

func TestAnExternalDependsOnReleasesOnTheTickWhenItHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t0 := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	var mu sync.Mutex
	now, n := t0, 0
	out := &fakeOutside{merged: map[string]bool{}, on: map[string]bool{}, asks: map[string]int{}}
	st := &store.Store{B: store.NewMem(), Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:      func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		NewID:    func() string { mu.Lock(); defer mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep:    func(time.Duration) {},
		External: out.ask}
	require.NoError(t, st.Init(ctx))
	at := t0.Add(4 * time.Second).Format(time.RFC3339)
	cards := []sprint.CardAdd{
		{ID: "pr-a", Brief: externalBrief("pr nova-tools#5303 merged")},
		{ID: "pr-b", Brief: externalBrief("pr nova-tools#5303 merged")}, // the same operand: asked once a tick
		{ID: "br", Brief: externalBrief("main contains 0123abcd")},
		{ID: "at", Brief: externalBrief("after " + at)},
		{ID: "both", Brief: externalBrief("pr mas-bandwidth/nova-tools#7 merged, after " + at)},
	}
	res, err := st.Run(ctx, store.AddStep(sprint.AddReq{Stream: "s1", Cards: cards}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	_, _, _, err = st.SetMachine(ctx, true) // after the add, which a STOPPED machine applies at once
	require.NoError(t, err)

	state := func() map[string]string {
		s, err := st.Load(ctx, store.All, nil)
		require.NoError(t, err)
		got := map[string]string{}
		for _, c := range cards {
			got[c.ID] = s.Work.Card(c.ID).Col
		}
		return got
	}
	tick := func() {
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
		_, err := st.Tick(ctx)
		require.NoError(t, err)
	}
	waiting := func(ids ...string) map[string]string {
		want := map[string]string{}
		for _, c := range cards {
			want[c.ID] = sprint.Ready
		}
		for _, id := range ids {
			want[id] = sprint.Waiting
		}
		return want
	}

	t.Run("admitted waiting, with what it waits for", func(t *testing.T) {
		assert.Equal(t, waiting("pr-a", "pr-b", "br", "at", "both"), state())
		s, err := st.Load(ctx, store.All, nil)
		require.NoError(t, err)
		assert.Equal(t, "pr nova-tools#5303 merged", s.Work.Card("pr-a").F(sprint.FieldExternal))
		assert.Equal(t, []string{"nova-tools#5303 merged"}, sprint.ExternalWaits(s.Work.Card("pr-a")))
		var whats []string
		for _, nd := range sprint.NeedsRank(s) {
			if nd.Kind == sprint.NeedExternal {
				whats = append(whats, nd.ID+": "+nd.What)
			}
		}
		assert.Contains(t, whats, "pr-a: waits for nova-tools#5303 merged")
		assert.Contains(t, whats, "br: waits for main contains 0123abcd")
	})

	tick() // t0+1s: nothing holds
	assert.Equal(t, waiting("pr-a", "pr-b", "br", "at", "both"), state(), "nothing holds: nothing is released")
	assert.Equal(t, map[string]int{
		"mas-bandwidth/nova-tools pr nova-tools#5303 merged":            1,
		"mas-bandwidth/nova-tools main contains 0123abcd":               1,
		"mas-bandwidth/nova-tools pr mas-bandwidth/nova-tools#7 merged": 1,
	}, out.asked(), "one ask per distinct operand a tick, none for after")

	out.set(out.merged, "nova-tools#5303")
	tick() // t0+2s: the pull request has merged
	assert.Equal(t, waiting("br", "at", "both"), state(), "both cards on the merged pull request are released on the first tick after it merges")
	assert.Equal(t, 2, out.asked()["mas-bandwidth/nova-tools pr nova-tools#5303 merged"], "asked once this tick for two cards")

	out.set(out.on, "main 0123abcd")
	out.set(out.merged, "mas-bandwidth/nova-tools#7")
	tick() // t0+3s: the branch contains the commit; the time is not yet past
	assert.Equal(t, waiting("at", "both"), state(), "the branch's card is released; after and both wait for the clock")
	assert.Equal(t, 2, out.asked()["mas-bandwidth/nova-tools pr nova-tools#5303 merged"], "a released card's operand is asked no more")

	tick() // t0+4s: the time is past
	assert.Equal(t, waiting(), state(), "after is released when the clock passes it, and both when its every operand holds")
	s, err := st.Load(ctx, store.All, nil)
	require.NoError(t, err)
	for _, o := range s.Open {
		assert.NotEqual(t, sprint.NStalled, o.Note.Type, "an external wait is held by the tick, never stalled: %s", o.Note.What)
	}
}

func TestAMalformedExternalOperandIsRefusedAtAddAndAtLint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &store.Store{B: store.NewMem(), Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now: func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }, Sleep: func(time.Duration) {}}
	require.NoError(t, st.Init(ctx))
	for _, tc := range []struct{ depends, says string }{
		{"pr nova-tools#x merged", `"nova-tools#x" is not <owner/repo>#<n>`},
		{"pr nova-tools#5303", "wants three words, pr <owner/repo>#<n> merged"},
		{"main contains HEAD", `"HEAD" is not a commit`},
		{"after tomorrow", `"tomorrow" is not an RFC3339 time`},
	} {
		t.Run(tc.depends, func(t *testing.T) {
			brief := externalBrief(tc.depends)
			res, err := st.Run(ctx, store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"x"}, Brief: brief}))
			require.NoError(t, err)
			require.Len(t, res.Refused, 1, "add refuses %q", tc.depends)
			assert.Contains(t, res.Refused[0].Why, tc.says)
			assert.Contains(t, res.Refused[0].Why, swarm.DependsOperandForms, "the refusal names the forms")
			lint := swarm.LintCardDepends([]byte("RESULT: x sha=0\n"+brief[len("c: an external wait tier: pro\n"):]), nil)
			require.Len(t, lint, 1, "lint refuses %q: %+v", tc.depends, lint)
			assert.Contains(t, lint[0].Excerpt, tc.says)
		})
	}
	t.Run("the three forms lint clean", func(t *testing.T) {
		raw := "RESULT: x sha=0\nDEPENDS-ON: a-card, pr mas-bandwidth/nova-tools#1 merged, main contains 0123abc, after 2030-01-02T03:04:05Z\n\nThe task."
		assert.Empty(t, swarm.LintCardDepends([]byte(raw), swarm.Lineup{"a-card": true}))
	})
}
