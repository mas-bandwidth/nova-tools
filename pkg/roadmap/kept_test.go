package roadmap

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// :kept and the done fix status came from nova-sprint's roadmapdoc fork when the two
// copies became this one: a move between the files carries fields under :kept, which
// decode in order and are never rendered, and a fix that has landed before its
// release is cut is done.
func TestKeptDecodesUnrenderedAndADoneFixIsAStatus(t *testing.T) {
	t.Parallel()
	src := strings.Replace(good, `:origin "issue #7" :release "v1.3")`,
		`:origin "issue #7" :release "v1.3" :kept (:fix-status "planned" :fix-release ""))`, 1)
	src = strings.Replace(src, `(done "d1" :title "Settled" :text "In place." :date "2026-10-09")`,
		`(done "d1" :title "Settled" :text "In place." :date "2026-10-09" :kept (:item-why "Not yet."))`, 1)
	require.NotEqual(t, good, src)
	doc, err := Decode("kept.sexp", []byte(src))
	require.NoError(t, err)
	require.Equal(t, []Pair{{Key: "fix-status", Value: "planned"}, {Key: "fix-release", Value: ""}}, doc.Items[2].Kept)
	require.Equal(t, []Pair{{Key: "item-why", Value: "Not yet."}}, doc.Done[0].Kept)
	require.Equal(t, Render(mustDecode(t, good), "x.sexp"), Render(doc, "x.sexp"), ":kept is not rendered")

	fx, err := DecodeFixes("done.sexp", []byte(strings.Replace(goodFixes,
		`:origin "a card." :status "in-progress")`, `:origin "a card." :status "done" :kept (:item-why "Later.")`+")", 1)))
	require.NoError(t, err)
	require.Equal(t, "done", fx.Items[1].Status)
	require.Equal(t, []Pair{{Key: "item-why", Value: "Later."}}, fx.Items[1].Kept)

	_, err = DecodeFixes("rel.sexp", []byte(strings.Replace(goodFixes, `(release "v1.2.5" :status "planned"`, `(release "v1.2.5" :status "done"`, 1)))
	require.ErrorContains(t, err, `:status "done" wants shipped, in-progress or planned`, "a release is never done: done is a fix's status")
}

func TestKeptRefusesWhatIsNotPairsOrAKeyTwice(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ kept, want string }{
		"not pairs":  {`:kept (:a)`, `:kept wants :key "value" pairs`},
		"not string": {`:kept (:a 1)`, `:kept wants :key "value" pairs`},
		"not a list": {`:kept "a"`, `:kept`},
		"key twice":  {`:kept (:a "1" :a "2")`, `:kept key :a stands twice`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := strings.Replace(good, `:origin "issue #7" :release "v1.3")`, `:origin "issue #7" :release "v1.3" `+c.kept+`)`, 1)
			require.NotEqual(t, good, src)
			doc, err := Decode("bad.sexp", []byte(src))
			require.Nil(t, doc)
			require.ErrorContains(t, err, c.want)
		})
	}
}

func mustDecode(t *testing.T, src string) *Doc {
	t.Helper()
	doc, err := Decode("x.sexp", []byte(src))
	require.NoError(t, err)
	return doc
}
