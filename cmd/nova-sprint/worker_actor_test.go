package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A worker's verb is recorded as the worker it was run as (docs/SPEC-SPRINT.md,
// the verbs' classes), whatever NOVA_SPRINT_ACTOR or --actor say, as the
// server records a worker's verb: a take, a finish and a read in the log
// name the member or reader, never the coordinator whose shell ran them.
func TestAWorkersVerbIsRecordedAsTheWorker(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t) // NOVA_SPRINT_ACTOR is the coordinator
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1")
	ta.ok("finish --as m1 s1-1.w1@1 --report done --actor someone-else")
	ta.ok("tick")
	ta.ok("read --as reader-a --begin")
	ta.ok("read --as reader-a --ok --finding fine")
	var log struct {
		Lines []sprint.Line `json:"lines"`
	}
	ta.json("log", &log)
	seen := map[string]bool{}
	for _, l := range log.Lines {
		want := map[string]string{"take": "m1", "finish": "m1", "read": "reader-a"}[l.Verb]
		if want == "" {
			continue
		}
		seen[l.Verb] = true
		assert.Equal(t, want, l.Actor, "the %s line %s %s -> %s", l.Verb, l.Card, l.From, l.To)
	}
	require.Equal(t, map[string]bool{"take": true, "finish": true, "read": true}, seen, "the log holds each worker verb's lines")
}
