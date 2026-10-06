package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func streamsBrief(t *testing.T, repo, base, task string) string {
	t.Helper()
	return writeBrief(t, "tier: flash\nREPO: "+repo+"\nBASE: "+base+"\n\nTHE TASK. "+task)
}

func seedLanded(t *testing.T, ta *testApp, id string) {
	t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "lead"})
	require.NoError(t, err)
	ctx := context.Background()
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	require.NoError(t, err)
	c := snap.Work.Placed(id)
	require.NotNil(t, c, id)
	_, err = ta.m.Apply(ctx, ntable.BatchManifest{
		Schema: 1, Table: st.Names.Table(sprint.Work), Epoch: "0",
		ExpectedTableRevision: fmt.Sprint(snap.Work.Revision), OperationID: "seed-land-" + id,
		Members: []ntable.BatchMemberEntry{{
			ID:     c.ID,
			Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev)},
			Move:   &ntable.MemberMoveOp{Row: c.Row, Col: sprint.Landed},
		}},
	})
	require.NoError(t, err)
}

// TestStreamsListsEveryStreamByRepoInOneCall lists three streams over two
// repositories, one of them naming both, in one streams call
// (docs/SPEC-SPRINT.md section 11).
func TestStreamsListsEveryStreamByRepoInOneCall(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	tools := "mas-bandwidth/nova-tools"
	nova := "mas-bandwidth/nova"
	ta.ok("add --stream tools tools-open --one --actor lead --brief-file " + streamsBrief(t, tools, "sprint/tools", "Open the tools listing. Then stop."))
	ta.ok("add --stream tools tools-landed --one --actor lead --brief-file " + streamsBrief(t, tools, "sprint/tools", "Land the tools card. Then stop."))
	seedLanded(t, ta, "tools-landed")
	ta.ok("stream set tools --release v1.2.0 --actor lead")
	ta.ok("add --stream seed seed-open --one --needs tools-open --actor lead --brief-file " + streamsBrief(t, nova, "sprint/nova", "Open the seed listing. Then stop."))
	ta.ok("add --stream mix mix-tools --one --actor lead --brief-file " + streamsBrief(t, tools, "sprint/tools", "Mix the tools card. Then stop."))
	ta.ok("add --stream mix mix-nova --one --needs mix-tools --actor lead --brief-file " + streamsBrief(t, nova, "sprint/nova", "Mix the seed card. Then stop."))

	ta.m.Calls = map[string]int{}
	var out, errb bytes.Buffer
	code := ta.a.cmdStreams([]string{"--cards"}, &out, &errb)
	calls := maps.Clone(ta.m.Calls)
	require.Equal(t, 0, code, "streams: %s", errb.String())
	assert.Equal(t, 0, calls["apply"], "the listing writes nothing: %v", calls)
	assert.LessOrEqual(t, calls["readset"], 4, "one load, not a read per card: %v", calls)
	assert.Positive(t, calls["readset"])
	want := "" +
		"STREAM tools repos=mas-bandwidth/nova-tools bases=sprint/tools release=v1.2.0 open=1 landed=1\n" +
		"CARD tools-open state=ready tier=flash needs=- title=Open the tools listing.\n" +
		"CARD tools-landed state=landed tier=flash needs=- title=Land the tools card.\n" +
		"STREAM seed repos=mas-bandwidth/nova bases=sprint/nova release=- open=1 landed=0\n" +
		"CARD seed-open state=waiting tier=flash needs=tools-open title=Open the seed listing.\n" +
		"STREAM mix repos=mas-bandwidth/nova,mas-bandwidth/nova-tools bases=sprint/nova,sprint/tools release=- open=2 landed=0\n" +
		"CARD mix-nova state=waiting tier=flash needs=mix-tools title=Mix the seed card.\n" +
		"CARD mix-tools state=ready tier=flash needs=- title=Mix the tools card.\n" +
		"NOTE stream mix names more than one repository: mas-bandwidth/nova, mas-bandwidth/nova-tools\n" +
		"NOTE stream mix names more than one base: sprint/nova, sprint/tools\n" +
		"STREAMS OK streams=3\n"
	assert.Equal(t, want, out.String())

	out.Reset()
	errb.Reset()
	require.Equal(t, 0, ta.a.cmdStreams([]string{"--cards", "--json"}, &out, &errb), errb.String())
	var v sprint.StreamsView
	require.NoError(t, json.Unmarshal(out.Bytes(), &v))
	require.Len(t, v.Streams, 3)
	assert.Equal(t, "v1.2.0", v.Streams[0].Release)
	assert.Equal(t, []string{"mas-bandwidth/nova", "mas-bandwidth/nova-tools"}, v.Streams[2].Repos)
	assert.Equal(t, "Mix the seed card.", v.Streams[2].Cards[0].Title)
	assert.Equal(t, []string{"mix-tools"}, v.Streams[2].Cards[0].Needs)
	assert.Len(t, v.Findings, 2)

	out.Reset()
	errb.Reset()
	require.Equal(t, 0, ta.a.cmdStreams([]string{"--repo", tools}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "STREAM tools ")
	assert.Contains(t, out.String(), "STREAM mix ")
	assert.NotContains(t, out.String(), "STREAM seed ")
	assert.Contains(t, out.String(), "STREAMS OK streams=2\n")

	out.Reset()
	errb.Reset()
	require.Equal(t, 0, ta.a.cmdStreams([]string{"--release", "v1.2.0"}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "STREAM tools ")
	assert.NotContains(t, out.String(), "STREAM mix ")
	assert.Contains(t, out.String(), "STREAMS OK streams=1\n")
	assert.NotContains(t, out.String(), "\nCARD ")

	out.Reset()
	errb.Reset()
	require.Equal(t, 0, ta.a.cmdStreams([]string{"--repo", nova, "--release", "v1.2.0"}, &out, &errb), errb.String())
	assert.Equal(t, "STREAMS OK streams=0\n", out.String())
}

func TestStreamsRefusesAWord(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	var out, errb bytes.Buffer
	code := ta.a.cmdStreams([]string{"extra"}, &out, &errb)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "REFUSED")
	assert.Empty(t, out.String())
	out.Reset()
	errb.Reset()
	code = ta.a.cmdStreams([]string{"--nope"}, &out, &errb)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "unknown flag")
}
