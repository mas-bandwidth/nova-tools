package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A scope amendment of the same change is allowed by rule and the rest is refused
// (docs/SPEC-SPRINT.md section 7): the package's test, its testdata, a doc; never code
// outside PATHS, and nothing when the change has no file of its own.
func TestAScopeAmendmentOfTheSameChangeIsAllowedByRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		changed, outside []string
		amended, refused []string
	}{
		{"the package's test beside a file in PATHS",
			[]string{"internal/a/a.go", "internal/a/a_test.go"}, []string{"internal/a/a_test.go"},
			[]string{"internal/a/a_test.go"}, nil},
		{"a test of another package is refused",
			[]string{"internal/a/a.go", "internal/b/b_test.go"}, []string{"internal/b/b_test.go"},
			nil, []string{"internal/b/b_test.go"}},
		{"the package's golden under testdata",
			[]string{"cmd/t/help.go", "cmd/t/testdata/help.golden"}, []string{"cmd/t/testdata/help.golden"},
			[]string{"cmd/t/testdata/help.golden"}, nil},
		{"a doc under docs/ and one beside the change",
			[]string{"internal/a/a.go", "docs/SPEC-A.md", "internal/a/README.md"}, []string{"docs/SPEC-A.md", "internal/a/README.md"},
			[]string{"docs/SPEC-A.md", "internal/a/README.md"}, nil},
		{"code outside PATHS is never amended",
			[]string{"internal/a/a.go", "internal/a/b.go", "go.mod"}, []string{"internal/a/b.go", "go.mod"},
			nil, []string{"internal/a/b.go", "go.mod"}},
		{"a change with no file of its own amends nothing",
			[]string{"internal/a/a_test.go", "docs/X.md"}, []string{"internal/a/a_test.go", "docs/X.md"},
			nil, []string{"internal/a/a_test.go", "docs/X.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			amended, refused := ScopeAmended(tc.changed, tc.outside)
			assert.Equal(t, tc.amended, amended, "amended")
			assert.Equal(t, tc.refused, refused, "refused")
		})
	}
}
