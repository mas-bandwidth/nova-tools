package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QueueEntries lets the mergequeue verb answer a test's held queue, as the
// forge does in production: no entries unless a test gives the fake its own.
// It keeps every test that runs a verb's example (twin_test.go) off the forge.
func (q *heldQueue) QueueEntries(context.Context, string) ([]mergeQueueEntry, error) {
	return nil, nil
}

// entryQueue is the mergequeue verb's fake forge: one answer, or one error.
type entryQueue struct {
	entries []mergeQueueEntry
	err     error
}

func (q *entryQueue) HoldsGroup(context.Context, string, string) (bool, error) { return false, nil }

func (q *entryQueue) QueueEntries(context.Context, string) ([]mergeQueueEntry, error) {
	return q.entries, q.err
}

// queueEntries is three entries out of position order, one on a card's branch,
// one on a promotion branch, their states and merge groups as the forge gives
// them.
func queueEntries(now time.Time) []mergeQueueEntry {
	return []mergeQueueEntry{
		{Position: 3, Number: "30", Title: "third", Branch: "promo/2030-01-02-1",
			State: "mergeable", Head: "ccc", Check: "ci:success", Enqueued: now.Add(-time.Minute)},
		{Position: 1, Number: "10", Title: "first", Branch: "sprint/s1-1.w1.g1.e15",
			State: "queued", Head: "aaa", Check: "ci:success", Enqueued: now.Add(-10 * time.Minute)},
		{Position: 2, Number: "20", Title: "second", Branch: "sprint/s1-2.w1.g1.e15",
			State: "awaiting checks", Head: "bbb", Check: "functional:in_progress", Enqueued: now.Add(-5 * time.Minute)},
	}
}

// TestMergeQueueListsTheQueueInPositionOrder: three entries print in position
// order with their states, the card's branch shows its card id and a
// promotion branch shows none, and --json carries the same fields.
func TestMergeQueueListsTheQueueInPositionOrder(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.a.mergeQueue = &entryQueue{entries: queueEntries(t0)}

	out := ta.ok("mergequeue --base dev")
	want := "MERGEQUEUE ENTRY pos=1 pr=10 state=queued card=s1-1 head=aaa check=ci:success in=10m0s title=first\n" +
		"MERGEQUEUE ENTRY pos=2 pr=20 state=awaiting\\x20checks card=s1-2 head=bbb check=functional:in_progress in=5m0s title=second\n" +
		"MERGEQUEUE ENTRY pos=3 pr=30 state=mergeable card=- head=ccc check=ci:success in=1m0s title=third\n" +
		"MERGEQUEUE OK entries=3 base=dev oldest=10 in=10m0s\n"
	assert.Equal(t, want, out)

	var v struct {
		Base    string `json:"base"`
		Count   int    `json:"count"`
		Entries []struct {
			Position int    `json:"position"`
			PR       string `json:"pr"`
			Title    string `json:"title"`
			Card     string `json:"card"`
			State    string `json:"state"`
			Head     string `json:"head"`
			Check    string `json:"check"`
			InQueue  string `json:"in_queue"`
		} `json:"entries"`
		Oldest *struct {
			PR string `json:"pr"`
		} `json:"oldest"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("mergequeue --json --base dev")), &v))
	assert.Equal(t, "dev", v.Base)
	assert.Equal(t, 3, v.Count)
	require.Len(t, v.Entries, 3)
	assert.Equal(t, []string{"10", "20", "30"}, []string{v.Entries[0].PR, v.Entries[1].PR, v.Entries[2].PR}, "position order")
	assert.Equal(t, []int{1, 2, 3}, []int{v.Entries[0].Position, v.Entries[1].Position, v.Entries[2].Position})
	assert.Equal(t, "queued", v.Entries[0].State)
	assert.Equal(t, "awaiting checks", v.Entries[1].State, "the JSON state is the words, not the escaped field")
	assert.Equal(t, "s1-1", v.Entries[0].Card, "a card's branch shows its card id")
	assert.Equal(t, "", v.Entries[2].Card, "a promotion branch is no card's")
	assert.Equal(t, "10m0s", v.Entries[0].InQueue)
	require.NotNil(t, v.Oldest)
	assert.Equal(t, "10", v.Oldest.PR, "the oldest entry is the one queued longest")
}

// TestMergeQueueEmptyQueuePrintsOneLine: an empty queue prints the one summary
// line, naming the base and a count of zero.
func TestMergeQueueEmptyQueuePrintsOneLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.a.mergeQueue = &entryQueue{}

	out := ta.ok("mergequeue")
	assert.Equal(t, "MERGEQUEUE OK entries=0 base=dev\n", out)
	assert.Equal(t, 1, strings.Count(out, "\n"), "one line, and no ENTRY line")
}

// TestMergeQueueReadsTheForgeAnswer is the parse of the one GraphQL query: the
// entry state enum becomes the queue's words, the pull request's number, title
// and branch are read, and the merge group's head and check run with them.
func TestMergeQueueReadsTheForgeAnswer(t *testing.T) {
	t.Parallel()
	raw := `{"data":{"repository":{"mergeQueue":{"entries":{"nodes":[
		{"position":2,"state":"LOCKED","enqueuedAt":"2030-01-02T02:04:05Z",
		 "pullRequest":{"number":20,"title":"second","headRefName":"sprint/s1-2.w1.g1.e15"},
		 "headCommit":{"oid":"bbb","statusCheckRollup":{"contexts":{"nodes":[
			{"__typename":"CheckRun","name":"functional","conclusion":"FAILURE"}]}}}},
		{"position":1,"state":"AWAITING_CHECKS","enqueuedAt":"2030-01-02T03:00:00Z",
		 "pullRequest":{"number":10,"title":"first","headRefName":"sprint/s1-1.w1.g1.e15"},
		 "headCommit":{"oid":"aaa","statusCheckRollup":{"contexts":{"nodes":[
			{"__typename":"CheckRun","name":"ci","conclusion":"SUCCESS"}]}}}}
	]}}}}}`
	gh := func(_ context.Context, _ string, args ...string) (string, error) {
		switch {
		case len(args) >= 2 && args[0] == "repo" && args[1] == "view":
			return "owner/name", nil
		case args[0] == "api":
			return raw, nil
		}
		return "", nil
	}
	f := ghForge{p: &promoter{ghRun: gh}}
	entries, err := f.QueueEntries(context.Background(), "dev")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, 2, entries[0].Position)
	assert.Equal(t, "locked", entries[0].State)
	assert.Equal(t, "20", entries[0].Number)
	assert.Equal(t, "second", entries[0].Title)
	assert.Equal(t, "bbb", entries[0].Head)
	assert.Equal(t, "functional:FAILURE", entries[0].Check, "the check run's name and conclusion")
	assert.Equal(t, time.Date(2030, 1, 2, 2, 4, 5, 0, time.UTC), entries[0].Enqueued)
	assert.Equal(t, "awaiting checks", entries[1].State)
	assert.Equal(t, "10", entries[1].Number)
	assert.Equal(t, "sprint/s1-1.w1.g1.e15", entries[1].Branch, "the pull request's head branch")
	assert.Equal(t, "ci:SUCCESS", entries[1].Check)
}
