package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPinVersionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string // empty means expected to be refused
	}{
		{"valid version", "latest 1.2.3", "1.2.3"},
		{"valid version with v prefix", "latest v1.2.3", "v1.2.3"},
		{"valid 2-digit version", "latest 1.2", "1.2"},
		{"valid devel", "nova-wake devel", "devel"},
		{"invalid: second token is not a version", "go version go1.21.0 darwin/arm64", ""},
		{"invalid: only one token", "go", ""},
		{"invalid: empty second token", "latest ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := identity(Entry{Kind: "pin"}, tt.raw, false)
			if tt.want == "" {
				assert.False(t, r.Known(), "want refusal for %q", tt.raw)
			} else {
				assert.True(t, r.Known(), "want version %q", tt.want)
				assert.Equal(t, tt.want, r.Version)
			}
		})
	}
}
