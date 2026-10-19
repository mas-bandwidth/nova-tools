package workgh

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestQueryCoverDefaultProgram: DefaultProgram names the GitHub CLI the
// production Query runs when none is named (SPEC-WORK-V1 section 1.6,
// "GraphQL documents run by gh api graphql"). It is a name, not an
// invocation, so its main path is the call itself; it has no refusal, and
// GhQuery's success path is reached only by running a gh subprocess, which
// unit tests do not (ghquery_functional_test.go holds that tier).
func TestQueryCoverDefaultProgram(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want string
	}{
		{name: "names the GitHub CLI on PATH", want: "gh"},
		{name: "is the package's own program constant", want: ghProgram},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, DefaultProgram(), "DefaultProgram returned %q", DefaultProgram())
		})
	}
}
