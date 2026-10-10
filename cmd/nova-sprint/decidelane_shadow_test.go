package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
)

type fakeShadowReadBackend struct {
	verdict string
	called  bool
	state   string
}

func (f *fakeShadowReadBackend) Name() string { return "fake-shadow" }

func (f *fakeShadowReadBackend) Ask(_ context.Context, s decide.Schema, state string) (map[string]decide.Answer, decide.Usage, error) {
	f.called = true
	f.state = state
	out := map[string]decide.Answer{}
	for name, q := range s.Questions {
		if q.Type == decide.Choice {
			out[name] = decide.Answer{Type: decide.Choice, Value: f.verdict, P: map[string]float64{f.verdict: 1}}
		} else {
			out[name] = decide.Answer{Type: decide.Noul, Value: "no", P: map[string]float64{"yes": 0.05}}
		}
	}
	return out, decide.Usage{}, nil
}

// shadowReadRound reads each card in review in shadow over its brief and diff, recording the
// verdict in read-shadow.jsonl, without counting as a regular read or altering the card's
// review status.
func TestShadowReadRound(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 s1-1 --one --brief-file " + proBriefFile(t))
	ta.deal(1)
	ta.ok("take --as m1")
	head := "0123456789abcdef0123456789abcdef01234567"
	ta.ok("finish --as m1 s1-1.w1@1 --head " + head)

	// s1-1 is now in Review
	pr := ta.primary("s1-1")
	require.NotNil(t, pr)
	assert.Equal(t, sprint.Review, pr.Col)

	dir := t.TempDir()
	backend := &fakeShadowReadBackend{verdict: decide.Bounce}
	lane := newDecideLane(dir, backend, ta.a.now, GradeWait)
	diffContent := "--- a/file.go\n+++ b/file.go\n@@ -1 +1 @@\n-old\n+new\n"
	diffCalled := false
	diffCards := map[string]string{}
	lane.diffOf = func(_ context.Context, c *sprint.Card) (string, error) {
		diffCalled = true
		diffCards[c.ID] = c.F("head")
		return diffContent, nil
	}
	ta.a.decide = lane

	ctx := context.Background()
	s, err := ta.a.decideSnapshot(ctx, "mem:0")
	require.NoError(t, err)

	n, problems := lane.shadowReadRound(ctx, s)
	require.Empty(t, problems)
	assert.Equal(t, 1, n)
	assert.True(t, diffCalled)
	assert.Equal(t, head, diffCards["s1-1"])
	assert.True(t, backend.called)
	assert.Contains(t, backend.state, "RULES")
	assert.Contains(t, backend.state, diffContent)

	// Verdict recorded in read-shadow.jsonl
	recordPath := filepath.Join(dir, decide.ShadowReadName+".jsonl")
	shadows, err := decide.Load(recordPath)
	require.NoError(t, err)
	require.Len(t, shadows, 1)
	assert.Equal(t, decide.ShadowReadName, shadows[0].Decision)
	assert.Equal(t, "s1-1", shadows[0].Inputs["card"])
	assert.Equal(t, "true", shadows[0].Inputs["shadow"])
	assert.Equal(t, decide.Bounce, shadows[0].Answers["verdict"].Value)
	assert.Equal(t, decide.ShadowMethod, shadows[0].Answers["verdict"].Method)

	// It did NOT count as a regular read: read.jsonl does not exist
	_, err = os.Stat(filepath.Join(dir, decide.ReadName+".jsonl"))
	assert.True(t, os.IsNotExist(err), "no regular read record should exist")

	// The primary card is still in Review and regular read count / broken_reads is unchanged
	prAfter := ta.primary("s1-1")
	assert.Equal(t, sprint.Review, prAfter.Col)
	assert.Equal(t, 0, prAfter.Int("broken_reads"))

	// Subsequent shadowReadRound does not re-read the same head
	n2, problems2 := lane.shadowReadRound(ctx, s)
	require.Empty(t, problems2)
	assert.Equal(t, 0, n2)

	// Also verify that decideRound invokes shadowReadRound and prints shadow_read=1 for new card
	ta.ok("add --stream s1 s1-2 --one --brief-file " + proBriefFile(t))
	ta.deal(1)
	ta.ok("take --as m1")
	head2 := "fedcba9876543210fedcba9876543210fedcba98"
	ta.ok("finish --as m1 s1-2.w1@1 --head " + head2)

	var buf bytes.Buffer
	ta.a.decideRound(ctx, "mem:0", &buf)
	assert.Contains(t, buf.String(), "shadow_read=1")

	// Outcomes attached when card lands
	for range 2 {
		ta.ok("ask --limit 100")
		ta.ok("read --as reader-a --ok --limit 100")
		ta.ok("read --as reader-b --ok --limit 100")
	}
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --batch 10")

	buf.Reset()
	ta.a.decideRound(ctx, "mem:0", &buf)
	shadowsAfter, err := decide.Load(recordPath)
	require.NoError(t, err)
	for _, sh := range shadowsAfter {
		if sh.Inputs["card"] == "s1-1" {
			require.NotNil(t, sh.Outcome)
			assert.Equal(t, decide.Land, sh.Outcome.Label)
		}
	}
}
