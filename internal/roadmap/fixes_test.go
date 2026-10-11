package roadmap

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const goodFixes = `; a comment is text
(fixes "v1"
 :title "Fixes"
 :text "The point
        releases."
 :releases ((release "v1.2.3" :status "shipped" :date "2026-10-10" :text "The split.")
            (release "v1.2.5" :status "planned" :text "Next."))
 :items ((fix "f1" :release "v1.2.3" :title "One" :origin "PR #1" :status "shipped")
         (fix "f2" :release "v1.2.5" :title "Two" :text "What it fixes." :origin "a card." :status "in-progress")))`

// A good fixes file decodes into its records and renders the same bytes every time,
// each release with its fixes in the file's order.
func TestFixesDecodesAndRendersTheShape(t *testing.T) {
	t.Parallel()
	fx, err := DecodeFixes("good.sexp", []byte(goodFixes))
	require.NoError(t, err)
	require.Len(t, fx.Releases, 2)
	require.Equal(t, Fix{ID: "f2", Release: "v1.2.5", Title: "Two", Text: "What it fixes.", Origin: "a card.", Status: "in-progress"}, fx.Items[1])
	page := RenderFixes(fx, "x.sexp")
	require.Equal(t, page, RenderFixes(fx, "x.sexp"))
	for _, want := range []string{
		"# Fixes\n",
		"The point releases.\n",
		"## v1.2.3 (shipped, 2026-10-10)\n\nThe split.\n\n- **One** (shipped). From: PR #1.\n",
		"## v1.2.5 (planned)\n\nNext.\n\n- **Two** (in-progress). What it fixes. From: a card.\n",
		"`make roadmap`",
	} {
		require.Contains(t, page, want)
	}
}

// Every shape problem is refused, named, and the whole file with it.
func TestFixesRefusesEveryShapeProblem(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ from, to, want string }{
		"unknown key":     {`:origin "PR #1"`, `:origin "PR #1" :colour "red"`, "unknown key :colour"},
		"missing key":     {`:origin "PR #1" `, ``, "missing key :origin"},
		"bad status":      {`:status "in-progress"`, `:status "done"`, `:status "done" wants shipped, in-progress or planned`},
		"unknown release": {`(fix "f2" :release "v1.2.5"`, `(fix "f2" :release "v9"`, `:release "v9" names no (release ...) record`},
		"empty release":   {`(fix "f2" :release "v1.2.5"`, `(fix "f2" :release "v1.2.3"`, `release "v1.2.5" holds no fix`},
		"duplicate id":    {`(fix "f2"`, `(fix "f1"`, `id "f1" is used twice`},
		"bad date":        {`:date "2026-10-10"`, `:date "Oct 10"`, `wants YYYY-MM-DD`},
		"wrong format":    {`(fixes "v1"`, `(fixes "v2"`, `want one (fixes "v1" ...) form`},
		"evaluation form": {`:title "Two"`, `:title #.(x)`, "dispatch macro"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := strings.Replace(goodFixes, c.from, c.to, 1)
			require.NotEqual(t, goodFixes, src, "the case must change the file")
			fx, err := DecodeFixes("bad.sexp", []byte(src))
			require.Nil(t, fx)
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, "bad.sexp")
		})
	}
}
