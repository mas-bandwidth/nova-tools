package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// IsCardTemplate in templates.go reports whether a name is one of the task templates
// a card is built from (read-pr, probe-row, fix-card). These unit tests cover the main
// path and a refusal; the function is pure and needs no store, network or subprocess.

func TestTemplatesCoverIsCardTemplate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"read-pr", "read-pr", true},
		{"probe-row", "probe-row", true},
		{"fix-card", "fix-card", true},
		{"result-is-not-a-card", "result", false},
		{"worker-is-not-a-card", "worker", false},
		{"setup-is-not-a-card", "setup", false},
		{"card-is-not-a-task-template", "card", false},
		{"unknown-name-refused", "no-such-template", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsCardTemplate(tc.in))
		})
	}
}
