//go:build functional

package pr_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pr"
)

func TestPRPathsCanon(t *testing.T) {
	t.Parallel()

	t.Run("space_in_json_form", func(t *testing.T) {
		got, err := pr.CanonPaths(`["a b/c", "x"]`)
		if err != nil || !reflect.DeepEqual(got, []string{"a b/c", "x"}) {
			t.Fatalf("CanonPaths = %q, %v", got, err)
		}
		if enc := pr.EncodePaths(got); enc != `["a b/c","x"]` {
			t.Fatalf("EncodePaths = %s", enc)
		}
		if tok, _ := pr.CanonPaths("a b/c"); !reflect.DeepEqual(tok, []string{"a", "b/c"}) {
			t.Fatalf("token form = %q, want two entries", tok)
		}
		h := newHarness(t)
		h.pushCard("c-sp", `["a b/c"]`, "code")
		h.mustHead("u-sp", h1, "c-sp")
		if v, _ := h.field("u-sp", "paths"); v != `["a b/c"]` {
			t.Fatalf("stored paths = %s", v)
		}
	})

	t.Run("normalize", func(t *testing.T) {
		for _, line := range []string{"./a//b/", `["./a//b/"]`, "a/b ./a/b", "a/b, a/b/"} {
			got, err := pr.CanonPaths(line)
			if err != nil || !reflect.DeepEqual(got, []string{"a/b"}) {
				t.Errorf("CanonPaths(%q) = %q, %v, want [a/b]", line, got, err)
			}
		}
		if got, _ := pr.CanonPaths("z a"); !reflect.DeepEqual(got, []string{"a", "z"}) {
			t.Errorf("not sorted: %q", got)
		}
	})

	t.Run("refuse_error_text", func(t *testing.T) {
		cases := map[string]string{
			"a/../b":    "REFUSED paths dotdot a/../b",
			"/abs":      "REFUSED paths absolute /abs",
			`["a", ""]`: `REFUSED paths empty ""`,
			`["a"`:      `REFUSED paths json ["a"`,
			"./":        "REFUSED paths empty ./",
		}
		for line, want := range cases {
			_, err := pr.CanonPaths(line)
			if err == nil || err.Error() != want || !errors.Is(err, pr.ErrRefused) {
				t.Errorf("CanonPaths(%q) err = %v, want %q", line, err, want)
			}
		}
	})
}
