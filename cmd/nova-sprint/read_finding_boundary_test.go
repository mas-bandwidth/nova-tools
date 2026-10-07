package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A broken report without a defect is not a read (docs/SPEC-SPRINT.md section 6).
// The authoritative verb must preserve a recoverable assignment: the reader can
// return it and the tick can ask a replacement at the same head and attempt
// (tla/DirtyTick.tla, ReadReturn, JudgedOnlyAfterTheBound).
func TestAuthoritativeBrokenReadFindingBoundary(t *testing.T) {
	t.Parallel()
	const finding = "a.go drops the error; return it to the caller"
	for _, tc := range []struct {
		name, finding string
		valid         bool
	}{
		{"empty finding", "", false},
		{"generic finding", "Request changes.", false},
		{"single-letter filename", finding, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
			ta.ok("reader away reader-c")
			ta.inReview(1)
			ta.ok("ask")
			ta.ok("reader up reader-c")
			const original = "s1-1.r1.reader-a"
			ta.ok("read --as reader-a --begin " + original)
			head := ta.primary("s1-1").F("head")
			st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
			readCard := func(id string) *sprint.Card {
				t.Helper()
				s, err := st.Load(context.Background(), []string{sprint.Readers}, nil)
				require.NoError(t, err)
				c := s.Readers.Placed(id)
				require.NotNil(t, c, "read assignment %s must remain on the table", id)
				return c
			}

			line := "read --as reader-a --broken " + original
			if tc.finding != "" {
				line += " --finding '" + tc.finding + "'"
			}
			code, out, errs := ta.do(line)
			rc := readCard(original)
			if tc.valid {
				assert.Zero(t, code, "%s: %s%s", line, out, errs)
				assert.Equal(t, sprint.Broken, rc.Col)
				assert.Equal(t, "broken", rc.F("verdict"))
				assert.Equal(t, finding, rc.F("finding"))
				assert.Contains(t, ta.group(sprint.NReadBroken, "s1").What, finding)
				return
			}

			// A refusal or a handback may recover the report; neither may persist
			// a broken verdict or ask the coordinator to judge an empty finding.
			assert.Contains(t, []string{sprint.Asked, sprint.Reading}, rc.Col, "%s: exit=%d %s%s", line, code, out, errs)
			assert.Empty(t, rc.F("verdict"), "an inadequate finding must not spend a read")
			assert.Empty(t, rc.F("finding"), "an inadequate finding must not become the read's finding")
			for _, g := range ta.inboxGroups() {
				assert.NotEqual(t, sprint.NReadBroken, g.Type, "no broken-read judgment is owed: %+v", g)
			}
			pr := ta.primary("s1-1")
			assert.Equal(t, sprint.Review, pr.Col)
			assert.Equal(t, head, pr.F("head"))
			assert.Equal(t, 1, pr.Int("attempt"))
			assert.Zero(t, pr.Int("broken_reads"), "no primary broken-read bound is spent")

			// A refused report leaves the held assignment available to return.
			// An automatic handback has already used that same production step.
			if rc.F(sprint.FieldReturned) == "" {
				ta.ok("read --as reader-a --return " + original + " --reason 'no finding: name the defect'")
			}
			rc = readCard(original)
			require.Equal(t, sprint.Asked, rc.Col, "the return leaves a pending assignment")
			assert.NotEmpty(t, rc.F(sprint.FieldReturned))
			assert.Empty(t, rc.F("verdict"))
			ta.ok("start")
			ta.ok("tick")
			const replacement = "s1-1.r1.reader-c"
			assert.Empty(t, ta.askedOf("reader-a"), "the returned assignment is not wedged with its old reader")
			require.Equal(t, []string{replacement}, ta.askedOf("reader-c"))
			rc = readCard(replacement)
			assert.Equal(t, sprint.Asked, rc.Col)
			assert.Equal(t, 1, rc.Int("attempt"))
			assert.Equal(t, head, rc.F("head"))
			assert.Zero(t, ta.primary("s1-1").Int("broken_reads"))
			for _, g := range ta.inboxGroups() {
				assert.NotEqual(t, sprint.NReadBroken, g.Type, "reasking is not a broken read: %+v", g)
			}
			ta.ok("read --as reader-c --broken " + replacement + " --finding '" + finding + "'")
			rc = readCard(replacement)
			assert.Equal(t, sprint.Broken, rc.Col)
			assert.Equal(t, finding, rc.F("finding"))
			assert.Contains(t, ta.group(sprint.NReadBroken, "s1").What, finding)
		})
	}
}
