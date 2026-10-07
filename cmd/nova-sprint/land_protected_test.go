package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The command's protected drive uses its real git seam against a private bare
// remote and a fake gh executable (docs/SPEC-SPRINT.md section 7). Opening and
// polling cannot report a landing before the forge reports the PR merged.
func TestLandProtectedCommandRecordsThePRThenPollsWithoutPushingAgain(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1 --one")
	head := r.head("s1-1", "main", "a.txt", "a\n")
	r.queued(map[string]string{"s1-1": head}, "s1-1")
	r.git(r.clone, "fetch", "origin")
	before := r.git(r.remote, "rev-parse", "main")
	fake := filepath.Join(r.dir, "fake-bin")
	require.NoError(t, os.MkdirAll(fake, 0755))
	calls, merged := filepath.Join(r.dir, "gh-calls"), filepath.Join(r.dir, "merged")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + strconv.Quote(calls) + "\ncase \"$*\" in\n 'pr create '*) echo https://forge.example.invalid/owner/repo/pull/1;;\n 'pr view 1 --json id') echo '{\"id\":\"PR_test\"}';;\n 'api graphql '*) echo '{}';;\n 'pr view 1 --json state,mergeCommit') if [ -f " + strconv.Quote(merged) + " ]; then echo '{\"state\":\"MERGED\",\"mergeCommit\":{\"oid\":\"" + head + "\"}}'; else echo '{\"state\":\"OPEN\",\"mergeCommit\":null}'; fi;;\n *) echo unexpected fake forge call >&2; exit 1;;\nesac\n"
	require.NoError(t, os.WriteFile(filepath.Join(fake, "gh"), []byte(script), 0755))
	r.a.gitEnv = r.env
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	load := func() *sprint.Snapshot {
		s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Merge, sprint.Readers}, nil)
		require.NoError(t, err)
		return s
	}
	s := load()
	pins := []landCard{{id: "s1-1", head: head, attempt: s.Work.Card("s1-1").F("attempt"), primary: s.Work.Card("s1-1")}}
	l := &lander{a: r.a, st: st, c: common{redis: "mem:0", actor: "tester"}, epoch: st.PinnedEpoch(), ghBin: filepath.Join(fake, "gh")}
	batch := landBatch{Stream: "s1", Base: "main", Status: "refused", Cards: 1, IDs: []string{"s1-1"}}
	done, on, ok := l.protectedDrive(context.Background(), s, r.clone, &batch, "s1", pins, head)
	require.True(t, ok)
	assert.False(t, on)
	assert.Zero(t, done)
	require.Equal(t, "merging", batch.Status, batch.Reason)
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
	assert.Equal(t, head, r.git(r.remote, "rev-parse", "refs/heads/land/s1"))
	l = &lander{a: r.a, st: st, c: common{redis: "mem:0", actor: "tester"}, epoch: st.PinnedEpoch(), ghBin: filepath.Join(fake, "gh")}
	// A fresh lander recovers the durable URL rather than local process state.
	s = load()
	pins[0].primary = s.Work.Card("s1-1")
	assert.Equal(t, "https://forge.example.invalid/owner/repo/pull/1", pins[0].primary.F(sprint.FieldLandPR))
	// A recorded URL is polled, even if another actor advanced the land branch.
	r.git(r.clone, "push", "origin", before+":refs/heads/land/s1-other")
	_, on, ok = l.protectedDrive(context.Background(), s, r.clone, &batch, "s1", pins, head)
	require.True(t, ok)
	assert.False(t, on)
	log, err := os.ReadFile(calls)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(log), "pr create "))
	// The outside merge advances the private protected base before its API receipt.
	r.git(r.clone, "push", "origin", head+":refs/heads/main")
	require.NoError(t, os.WriteFile(merged, []byte("merged\n"), 0600))
	_, on, ok = l.protectedDrive(context.Background(), load(), r.clone, &batch, "s1", pins, head)
	require.True(t, ok)
	assert.True(t, on)
	assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
	assert.Equal(t, head, r.git(r.remote, "rev-parse", "main"))
}

// An arrival beside an open PR is a later batch, so the next poll does not
// refuse both cards for naming different URLs (docs/SPEC-SPRINT.md section 7).
func TestLandProtectedBatchSeparatesArrivalsFromTheOpenPR(t *testing.T) {
	t.Parallel()
	card := func(url string) landCard {
		return landCard{head: "h1", repo: "owner/repo", base: "main", primary: &sprint.Card{Fields: map[string]string{sprint.FieldLandPR: url, sprint.FieldLandCardHead: "h1"}}}
	}
	open := card("https://forge.example.invalid/owner/repo/pull/1")
	assert.True(t, sameLandBatch(open, open))
	assert.False(t, sameLandBatch(open, card("")))
	assert.False(t, sameLandBatch(card(""), open))
	assert.True(t, sameLandBatch(card(""), card("")))
	assert.False(t, sameLandBatch(open, card("https://forge.example.invalid/owner/repo/pull/2")))
}

func TestLandProtectedRefusesAnUnavailableReaderSnapshot(t *testing.T) {
	t.Parallel()
	l := &lander{}
	_, err := l.withReaders(context.Background(), &sprint.Snapshot{})
	require.ErrorContains(t, err, "reader snapshot")
}
