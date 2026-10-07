package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// bases lists one row per base a card not landed or dropped names, with its cards by state,
// ahead and behind origin's dev in land's kept clone (one fetch a call), and the gate at its
// tip as the lander last recorded it; add refuses a card on a personal base unless
// --allow-personal-base is given (docs/SPEC-SPRINT.md section 11, bases-view-r.w2). On a
// temporary origin: main is landed on (green), hot stops its stream on its gate (red, with the
// time), feature is never gated (-), and coordinator/x, admitted with the flag, is no branch of
// origin.
func TestBasesListsEveryBaseInUseAndAddRefusesAPersonalOne(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "push", "-q", "origin", "main:refs/heads/dev", "main:refs/heads/hot")
	briefs := t.TempDir()
	brief := func(id, base string) string {
		path := filepath.Join(briefs, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+base+"\n\nWrite "+id+".txt.")), 0o600))
		return path
	}

	// main: a card lands through land, so the lander gated main's tip green; s1 and s2 take
	// cards cut on main, which the promotion stream alone takes
	r.promotionStream("s1")
	r.promotionStream("s2")
	r.ok("add --stream s1 g --one --brief-file " + brief("g", "main"))
	r.ok("add --stream s3 h --one --brief-file " + brief("h", "hot"))
	r.queued(map[string]string{"g": r.head("g", "main", "g.txt", "g\n"), "h": r.head("h", "hot", "h.txt", "h\n")}, "g", "h")
	r.ok("land --stream s1")
	r.a.sleep(time.Minute)
	// hot: its stream stops on the base's gate
	r.ok("merge --stream s3 --base-red 'go vet: boom'")
	stopped := r.a.now().UTC().Format(time.RFC3339)

	// feature: two commits ahead of dev; dev then moves one past main
	r.git(r.worker, "switch", "-q", "--detach", "refs/remotes/origin/main")
	r.commit("f1.txt", "1\n", "f1")
	r.commit("f2.txt", "2\n", "f2")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/feature")
	r.moveBase("dev", "dev.txt")
	r.ok("add --stream s2 m2 --one --brief-file " + brief("m2", "main"))
	r.ok("add --stream s2 f --one --brief-file " + brief("f", "feature"))

	// a personal base: refused, naming the base and the flag, nothing written; taken with it
	r.a.friends = friendRows("ada")
	r.ok("friend sync --root " + t.TempDir())
	r.ok("init --owner owner") // the owner init records, beside the coordinator and the friends
	for _, base := range []string{"coordinator/x", "owner/x", "ada/x"} {
		code, _, errs := r.do("add --stream s5 p --one --brief-file " + brief("p", base))
		assert.Equal(t, 2, code, base)
		assert.Contains(t, errs, "add REFUSED: the BASE "+base+" is a personal branch", base)
		assert.Contains(t, errs, "--allow-personal-base", base)
	}
	code, _, _ := r.do("card p")
	assert.NotEqual(t, 0, code, "a refused add wrote the card")
	r.ok("add --stream s5 p --one --allow-personal-base --brief-file " + brief("p", "coordinator/x"))

	kept := filepath.Join(r.dir, "land", repoDirName(r.remote))
	before := r.git(kept, "rev-parse", "refs/remotes/origin/main")
	out := r.ok("bases")
	line := func(base string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "BASES "+base+" ") {
				return l
			}
		}
		t.Errorf("no row for the base %s in:\n%s", base, out)
		return ""
	}
	mainAhead := r.git(kept, "rev-list", "--count", "refs/remotes/origin/dev..refs/remotes/origin/main")
	assert.Equal(t, before, r.git(kept, "rev-parse", "refs/remotes/origin/main"))
	assert.Contains(t, line("main"), "repo="+r.remote+" cards=1 waiting=0 ready=1 working=0 review=0 merging=0 ahead="+mainAhead+" behind=1 gate=green gated=")
	assert.Contains(t, line("feature"), " cards=1 waiting=0 ready=1 working=0 review=0 merging=0 ahead=2 behind=1 gate=- gated=-")
	assert.Contains(t, line("hot"), " cards=1 waiting=0 ready=0 working=0 review=0 merging=1 ahead=0 behind=1 gate=red gated="+stopped)
	assert.Contains(t, line("coordinator/x"), " cards=1 waiting=0 ready=1 working=0 review=0 merging=0 ahead=- behind=- gate=- gated=-")
	assert.Contains(t, out, "NOTE the base coordinator/x is no branch of origin "+r.remote)
	assert.Contains(t, out, "BASES OK bases=4 cards=4")
	assert.NotContains(t, out, "BASES g ", "a landed card's base is no row of its own when no open card names it")

	var v struct {
		Bases []baseRow `json:"bases"`
	}
	r.json("bases", &v)
	require.Len(t, v.Bases, 4)
	assert.Equal(t, []string{"coordinator/x", "feature", "hot", "main"}, []string{v.Bases[0].Base, v.Bases[1].Base, v.Bases[2].Base, v.Bases[3].Base}, "rows in base order")
	assert.Nil(t, v.Bases[0].Ahead, "coordinator/x is no branch of origin")
	assert.Equal(t, []string{"f"}, v.Bases[1].Cards[sprint.Ready])
	require.NotNil(t, v.Bases[1].Ahead)
	assert.Equal(t, 2, *v.Bases[1].Ahead)
	assert.Equal(t, "red", v.Bases[2].Gate)

	// with no kept clone, ahead and behind are - and a note says why; nothing is cloned
	require.NoError(t, os.RemoveAll(kept))
	out = r.ok("bases")
	assert.Contains(t, out, "ahead=- behind=- gate=green")
	assert.Contains(t, out, "NOTE land keeps no clone of "+r.remote)
	_, err := os.Stat(kept)
	assert.True(t, os.IsNotExist(err), "bases cloned")
}
