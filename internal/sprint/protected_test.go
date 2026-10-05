package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lander never lands on a protected branch (dev, main) of a repository unless
// the stream is marked for that repository (docs/SPEC-SPRINT.md section 7, the
// protected branches); the mark is the coordinator's `stream set --land-protected`.
func TestLanderNeverLandsOnAProtectedBranchUnlessMarked(t *testing.T) {
	t.Parallel()
	const repo = "mas-bandwidth/nova-tools"
	w := streamsWorld(t, "s1", "promote")
	remedy := "; run: nova-sprint stream set s1 --land-protected " + repo

	t.Run("an unmarked stream is refused on each protected branch, with the remedy", func(t *testing.T) {
		t.Parallel()
		for _, base := range ProtectedBranches {
			why := ProtectedLandWhy(w.s, "s1", repo, base, "c1")
			assert.Contains(t, why, "card c1 lands on "+base+", a protected branch of "+repo, base)
			assert.Contains(t, why, "stream s1 is not marked to land on it", base)
			assert.True(t, len(why) > len(remedy) && why[len(why)-len(remedy):] == remedy, "the remedy ends the line: %s", why)
		}
	})
	t.Run("a branch that is not protected lands in any stream", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, ProtectedLandWhy(w.s, "s1", repo, "sprint/mechanical-2026-10-02", "c1"))
	})
	t.Run("a card naming no repository is held to the rule too", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, ProtectedLandWhy(w.s, "s1", "", "dev", "c1"), "run: nova-sprint stream set s1 --land-protected "+LandProtectedAny)
	})

	m := streamsWorld(t, "s1", "promote")
	m.must(Set(m.s, SetReq{Streams: []string{"promote"}, LandProtected: repo, Who: "coordinator"}))
	require.Equal(t, repo, m.s.StreamCtl("promote").F(FieldLandProtected), "the mark is the control card's field")
	t.Run("the marked stream lands on the repository it is marked for, in any spelling", func(t *testing.T) {
		t.Parallel()
		for _, spelling := range []string{repo, "https://forge.example.invalid/mas-bandwidth/nova-tools.git", "git@forge.example.invalid:Mas-Bandwidth/nova-tools"} {
			for _, base := range ProtectedBranches {
				assert.Empty(t, ProtectedLandWhy(m.s, "promote", spelling, base, "c1"), spelling+" "+base)
			}
		}
	})
	t.Run("the mark is per repository and per stream", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, ProtectedLandWhy(m.s, "promote", "mas-bandwidth/schema", "dev", "c1"), "a protected branch of mas-bandwidth/schema")
		assert.Contains(t, ProtectedLandWhy(m.s, "s1", repo, "dev", "c1"), "stream s1 is not marked")
	})

	a := streamsWorld(t, "s1")
	a.must(Set(a.s, SetReq{Streams: []string{"s1"}, LandProtected: LandProtectedAny, Who: "coordinator"}))
	t.Run("a stream marked any lands on every repository's protected branches", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, ProtectedLandWhy(a.s, "s1", "", "main", "c1"))
		assert.Empty(t, ProtectedLandWhy(a.s, "s1", "mas-bandwidth/schema", "dev", "c1"))
	})

	d := streamsWorld(t, "s1")
	d.must(Set(d.s, SetReq{Streams: []string{"s1"}, LandProtected: repo, Who: "coordinator"}))
	d.must(Set(d.s, SetReq{Streams: []string{"s1"}, LandProtected: ReadTierDefault, Who: "coordinator"}))
	t.Run("default takes the mark off", func(t *testing.T) {
		t.Parallel()
		assert.False(t, d.s.StreamCtl("s1").Has(FieldLandProtected))
		assert.NotEmpty(t, ProtectedLandWhy(d.s, "s1", repo, "dev", "c1"))
	})

	t.Run("the mark is refused whole when it is not a stream's or names no repository", func(t *testing.T) {
		t.Parallel()
		r := streamsWorld(t, "s1")
		for _, req := range []struct {
			req  SetReq
			want string
		}{
			{SetReq{LandProtected: repo, Who: "coordinator"}, "--land-protected is a stream's"},
			{SetReq{Streams: []string{"s1"}, LandProtected: "a/b,,c/d", Who: "coordinator"}, "--land-protected wants repositories"},
			{SetReq{Streams: []string{"s1"}, LandProtected: "-x", Who: "coordinator"}, "--land-protected wants repositories"},
			{SetReq{Streams: []string{"s1"}, LandProtected: repo, Who: "intruder"}, "coordinator"},
		} {
			p := Set(r.s, req.req)
			require.NotEmpty(t, p.Refused, "%+v", req.req)
			assert.Contains(t, p.Refused[0].Why, req.want)
			assert.Empty(t, p.Units)
		}
	})
}
