package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/FRIENDS.md: a misplaced header still finishes at origin's tip and
// teaches the pinned shape through a NOTE attached to the finish's report.
func TestAFriendReportWhoseFirstTwoLinesAreNotPinnedIsReadAllTheSame(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("a", 40)
	p := sprint.Packet{Card: "c.w1", Gen: 1, Branch: "sprint/c.w1.g1.e0", Brief: "REPO: mas-bandwidth/nova-tools\n"}
	for _, tc := range []struct {
		name, prefix string
		note         bool
	}{
		{name: "pinned"},
		{name: "later", prefix: "# Notes\n\n", note: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := tc.prefix + "Verdict: LAND\nHead: " + sha + "\n\nThe gate is green.\n"
			verdict, head, para := friendReportOf(report)
			assert.Equal(t, VerdictLand, verdict)
			assert.Equal(t, sha, head)
			assert.Equal(t, "The gate is green.", para)
			r, err := friendFinish(context.Background(), "amy", p, report, tipIs(t, sha))
			require.NoError(t, err)
			assert.False(t, r.Failed)
			assert.Equal(t, sha, r.Head)
			if tc.note {
				assert.Contains(t, r.Report, "NOTE:")
				assert.Contains(t, r.Report, "Verdict: is on line 3")
				assert.Contains(t, r.Report, "Head: is on line 4")
			} else {
				assert.Equal(t, "friend amy LAND: The gate is green.", r.Report)
			}
		})
	}
}

func TestAFriendSyncCarriesHoldFixLinesToFinish(t *testing.T) {
	t.Parallel()
	p := sprint.Packet{Card: "c.w1", Gen: 1, Branch: "sprint/c.w1.g1.e0", Brief: "REPO: mas-bandwidth/nova-tools\n"}
	report := "Verdict: HOLD\n\nThe gate requires a bench. TIER: pro was only discussed.\n\nNEEDS: s1-2\nTIER: heavy\nGATE-HOST: linux\n"
	r, err := friendFinish(context.Background(), "amy", p, report, nil)
	require.NoError(t, err)
	assert.True(t, r.Failed)
	assert.Contains(t, r.Report, "friend amy HOLD:")
	assert.Contains(t, r.Report, "; NEEDS: s1-2; TIER: heavy; GATE-HOST: linux")
	assert.NotContains(t, r.Report, "; TIER: pro")
}
