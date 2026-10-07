package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Only the promotion stream takes a card cut on a protected branch, dev or main
// (docs/SPEC-SPRINT.md section 7, protected-bases-pb-b.w2). Found 2026-10-04: cards with BASE
// dev were landed straight onto dev and ejected the promotion cut three times; the same
// hole was open for main. add refuses a card cut on main in a plain stream, the one-brief
// and the many-brief form alike, nothing written, naming the stream and --promotion;
// stream set <s> --promotion marks the stream, which then takes it and where shows it,
// and --promotion=false takes the mark off again. The lander refuses a batch whose base is dev
// or main on a plain stream before any git, naming the stream and the mark's flag.
func TestAddAndLandRefuseDevAndMainOutsideAPromotionStream(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	// add reads each brief at its base (the brief checks): a twin of the repository holds
	// every card's file on main, dev and the sprint branch, and the pin is its commit
	files := map[string]string{}
	for _, id := range []string{"p1", "p2", "d1", "n1", "m1", "m2"} {
		files["internal/"+id+".go"] = "package internal\n"
	}
	repo, sha := twinRemote(t, ta, files, "main", "dev", "sprint/mechanical-2026-10-02")
	onMain := writeBaseBrief(t, dir, repo, "p1", "main")
	onMain2 := writeBaseBrief(t, dir, repo, "p2", "main@"+sha)
	onDev := writeBaseBrief(t, dir, repo, "d1", "dev")

	for _, c := range []struct{ name, line, card string }{
		{"one brief", "add --stream s1 p1 --one --brief-file " + onMain, "p1"},
		{"a pinned main base", "add --stream s1 p2 --one --brief-file " + onMain2, "p2"},
		{"a count of cards", "add --stream s1 --count 2 --brief-file " + onMain, "s1-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := ta.applies()
			code, out, errs := ta.do(c.line)
			assert.NotEqual(t, 0, code, "%s: %s%s", c.line, out, errs)
			assert.Contains(t, out+errs, "card "+c.card+" is cut on main, a protected branch, and stream s1 is not the promotion stream")
			assert.Contains(t, out+errs, "run: nova-sprint stream set s1 --promotion")
			assert.NotContains(t, out, "MOVED", "nothing written")
			assert.False(t, ta.placed(c.card), "nothing written")
			assert.Equal(t, before, ta.applies(), "no store write")
		})
	}

	many := t.TempDir()
	writeBaseBrief(t, many, repo, "m1", "sprint/mechanical-2026-10-02")
	writeBaseBrief(t, many, repo, "m2", "main")
	code, out, errs := ta.do("add --stream s2 --brief-dir " + many)
	assert.NotEqual(t, 0, code, "many-brief add with a main card: %s%s", out, errs)
	assert.Contains(t, out+errs, "card m2 is cut on main, a protected branch, and stream s2 is not the promotion stream")
	assert.False(t, ta.placed("m1") || ta.placed("m2"), "nothing written, all or none")

	// the promotion stream takes main and dev; the mark is kept on the stream, which is a
	// stream once it holds a card (stream set refuses a name that is none)
	assert.Contains(t, ta.ok("add --stream s1 n1 --one --brief-file "+writeBaseBrief(t, dir, repo, "n1", "")), "MOVED n1 -> ready")
	assert.Contains(t, ta.ok("stream set s1 --promotion"), "land-protected any")
	assert.Contains(t, ta.ok("where"), "\npromotion: s1\n", "where shows the mark, from the stream clocks")
	assert.Contains(t, ta.ok("where --json"), `"Promotion":"any"`, "and where --json carries it on the stream")
	assert.Contains(t, ta.ok("add --stream s1 p1 --one --brief-file "+onMain), "MOVED p1 -> ready", "the promotion stream takes a card cut on main")
	assert.Contains(t, ta.ok("add --stream s1 d1 --one --brief-file "+onDev), "MOVED d1 -> ready", "and one cut on dev")

	code, out, errs = ta.do("stream set s1 --promotion --land-protected any")
	assert.NotEqual(t, 0, code, "one mark at a time: %s%s", out, errs)

	// --promotion=false takes the mark off: the stream is plain again
	assert.Contains(t, ta.ok("stream set s1 --promotion=false"), "land-protected none")
	assert.NotContains(t, ta.ok("where"), "promotion:", "and where shows no promotion stream")
	code, out, errs = ta.do("add --stream s1 p2 --one --brief-file " + onMain2)
	assert.NotEqual(t, 0, code, "unmarked again: %s%s", out, errs)
	assert.Contains(t, out+errs, "card p2 is cut on main, a protected branch, and stream s1 is not the promotion stream")

	t.Run("the lander", func(t *testing.T) {
		t.Parallel()
		for _, base := range []string{"dev", "main"} {
			t.Run(base, func(t *testing.T) {
				t.Parallel()
				r := newLandRig(t)
				for _, b := range []string{"dev", "sprint/s1"} {
					r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/"+b)
				}
				r.git(r.worker, "fetch", "-q", "origin")
				r.ok("add --stream s1 --count 1 --one")
				r.queued(map[string]string{"s1-1": r.head("s1-1", "sprint/s1", "a.txt", "a\n")}, "s1-1")
				r.ok("stream set s1 --promotion=false") // queued marks every stream; s1 is plain again
				tip := r.git(r.remote, "rev-parse", base)
				for _, land := range []string{"land --repo-dir " + r.clone + " --base " + base + " --dry-run", "land --repo-dir " + r.clone + " --base " + base} {
					code, out, errs := r.do(land)
					assert.Equal(t, 1, code, land)
					assert.Contains(t, out+errs, "LAND REFUSED stream=s1 cards=1 base="+base+" tip=- ids=s1-1 ", land)
					assert.Contains(t, out+errs, "card s1-1 lands on "+base+", a protected branch", land)
					assert.Contains(t, out+errs, "stream s1 is not marked to land on it", land)
					assert.Contains(t, out+errs, "; run: nova-sprint stream set s1 --land-protected any; or mark it the promotion stream, for every repository: nova-sprint stream set s1 --promotion\n", land)
				}
				assert.Equal(t, tip, r.git(r.remote, "rev-parse", base), "nothing was pushed to "+base)
				assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"), "nothing was recorded")

				r.ok("stream set s1 --promotion")
				assert.Contains(t, r.ok("land --repo-dir "+r.clone+" --base "+base), "LAND OK stream=s1 cards=1 base="+base, "the promotion stream lands on "+base)
			})
		}
	})
}

// promotionStream makes stream the promotion stream before a card cut on dev or main is
// added to it (docs/SPEC-SPRINT.md section 7, protected-bases-pb-b.w2): a stream is a stream
// once it holds a card, so a card naming no BASE: founds it, the mark goes on, and the
// founding card is dropped, the stream and its mark kept.
func (ta *testApp) promotionStream(stream string) {
	ta.t.Helper()
	ta.ok("add --stream " + stream + " " + stream + "-founder --one")
	ta.ok("stream set " + stream + " --promotion")
	ta.ok("drop " + stream + "-founder --reason 'it founded the promotion stream'")
}
