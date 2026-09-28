package canonpath_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/canonpath"
)

func TestCanonPaths(t *testing.T) {
	t.Parallel()

	t.Run("space_in_json_form", func(t *testing.T) {
		got, err := canonpath.CanonPaths(`["a b/c", "x"]`)
		if err != nil || !reflect.DeepEqual(got, []string{"a b/c", "x"}) {
			t.Fatalf("CanonPaths = %q, %v", got, err)
		}
		if enc := canonpath.EncodePaths(got); enc != `["a b/c","x"]` {
			t.Fatalf("EncodePaths = %s", enc)
		}
		if tok, _ := canonpath.CanonPaths("a b/c"); !reflect.DeepEqual(tok, []string{"a", "b/c"}) {
			t.Fatalf("token form = %q, want two entries", tok)
		}
	})

	t.Run("normalize_and_dedup", func(t *testing.T) {
		for _, line := range []string{"./a//b/", `["./a//b/"]`, "a/b ./a/b", "a/b, a/b/", `a\b`} {
			got, err := canonpath.CanonPaths(line)
			if err != nil || !reflect.DeepEqual(got, []string{"a/b"}) {
				t.Errorf("CanonPaths(%q) = %q, %v, want [a/b]", line, got, err)
			}
		}
		if got, _ := canonpath.CanonPaths("z a"); !reflect.DeepEqual(got, []string{"a", "z"}) {
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
			"":          `REFUSED paths empty ""`,
			".":         "REFUSED paths empty .",
			"..":        "REFUSED paths dotdot ..",
			"a/..":      "REFUSED paths dotdot a/..",
		}
		for line, want := range cases {
			_, err := canonpath.CanonPaths(line)
			if err == nil || err.Error() != want || !errors.Is(err, canonpath.ErrRefused) {
				t.Errorf("CanonPaths(%q) err = %v, want %q (wrapping ErrRefused)", line, err, want)
			}
		}
	})

	t.Run("canon_json", func(t *testing.T) {
		cj, err := canonpath.CanonJSON("b/c, a/b")
		if err != nil || cj != `["a/b","b/c"]` {
			t.Fatalf("CanonJSON = %q, %v", cj, err)
		}
		if _, err := canonpath.CanonJSON("/abs"); err == nil {
			t.Fatal("CanonJSON should fail on invalid path")
		}
	})
}
