package roadmap

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sexp"
	"github.com/stretchr/testify/require"
)

// Deferring into a roadmap with no Unsorted area makes it, adds it to the page and
// the root work-set, and renders each card as an open row; a second deferral adds
// rows to that area. Every byte of the original stays where it was.
func TestAddDeferredMakesThenExtendsTheUnsortedArea(t *testing.T) {
	t.Parallel()
	src := "; a comment the edit keeps\n" + minimalFeatures + "\n"
	out, err := AddDeferred("x.sexp", []byte(src), []Deferred{{ID: "c1", Title: "Faster  acks |\n now", Brief: `say "why" \ here`, Date: "2026-10-18", Origin: "card c1"}})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(out), "; a comment the edit keeps\n(:schema 1 :root \"goal\""))
	fs, err := DecodeFeatures("x.sexp", out)
	require.NoError(t, err)
	require.Equal(t, []string{"a", UnsortedArea}, fs.Areas)
	require.Equal(t, "todo", fs.Nodes["card/c1"].State)
	page := RenderFeatures(fs, "docs/roadmap.sexp", "")
	require.Contains(t, page, "## Unsorted\n\nWork moved out of the sprint, not yet placed in an area.\n")
	require.Contains(t, page, "| Faster acks / now | ❌ |\n| complete | 0% |\n")
	require.Contains(t, string(out), `:brief "say \"why\" \\ here"`)
	top, err := sexp.Read("x.sexp", out, FeatureLimits)
	require.NoError(t, err)
	brief := plistValue(*findNode(plistValue(top, "nodes"), "card/c1"), "brief")
	require.Equal(t, `say "why" \ here`, brief.Value, "the brief reads back as written")

	again, err := AddDeferred("x.sexp", out, []Deferred{{ID: "c2", Title: "Second"}, {ID: "c3", Title: ""}})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(again), string(out[:strings.Index(string(out), "(\"c1\"")])), "the earlier bytes are kept")
	fs, err = DecodeFeatures("x.sexp", again)
	require.NoError(t, err)
	require.Equal(t, []string{"a", UnsortedArea}, fs.Areas, "the area is not made twice")
	require.Contains(t, RenderFeatures(fs, "s", ""), "| Faster acks / now | ❌ |\n| Second | ❌ |\n| c3 | ❌ |\n| complete | 0% |\n")
}

// A card the roadmap holds, an empty set, a missing id, a root that is no
// work-set and an Unsorted node that is no single-column area are refused, and
// the data is not changed.
func TestAddDeferredRefusals(t *testing.T) {
	t.Parallel()
	once, err := AddDeferred("x.sexp", []byte(minimalFeatures), []Deferred{{ID: "c1", Title: "One"}})
	require.NoError(t, err)
	rootArea := strings.Replace(minimalFeatures, `:root "goal"`, `:root "a"`, 1)
	rootArea = strings.Replace(rootArea, `:page (:title "P" :roadmaps ("a"))`, `:page (:title "P" :roadmaps ("a"))`, 1)
	notArea := strings.Replace(minimalFeatures, `(:id "t2" :type :task`, `(:id "unsorted" :type :work-set :children ("t2")) (:id "t2" :type :task`, 1)
	cases := map[string]struct {
		src   string
		items []Deferred
		want  string
	}{
		"held card":    {string(once), []Deferred{{ID: "c1"}}, `already holds card "c1"`},
		"twice in one": {minimalFeatures, []Deferred{{ID: "c9"}, {ID: "c9"}}, `already holds card "c9"`},
		"none":         {minimalFeatures, nil, "no deferred work"},
		"no id":        {minimalFeatures, []Deferred{{ID: " "}}, "has no id"},
		"area root":    {rootArea, []Deferred{{ID: "c1"}}, "add it by hand"},
		"not an area":  {notArea, []Deferred{{ID: "c1"}}, "not the unsorted area"},
		"card form":    {good, []Deferred{{ID: "c1"}}, "is not the feature-table form"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := AddDeferred("x.sexp", []byte(c.src), c.items)
			require.Nil(t, out)
			require.ErrorContains(t, err, c.want)
		})
	}
}
