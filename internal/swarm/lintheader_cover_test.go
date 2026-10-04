package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The unit cover for CardHeaderValue (lintheader.go:223), the typed-header
// reader nova-sprint add reads DEPENDS-ON: and PATHS: through: the value of
// one `KEY: value` line of the block the gate reads, ok false where the block
// carries no such line. Every test is named TestLintheaderCover* so
// `-run TestLintheaderCover` selects them.

// CardHeaderValue answers one key's value from the typed header block, and
// ok=false where the block has no such line.
func TestLintheaderCoverCardHeaderValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		raw    string
		key    string
		want   string
		wantOK bool
	}{
		{
			name: "a key the block carries answers its value",
			raw: "contract line\n" +
				"KIND: fix-red\n" +
				"PATHS: internal/swarm/lintheader.go\n" +
				"TEST: ./internal/swarm TestSomething\n",
			key:    "PATHS",
			want:   "internal/swarm/lintheader.go",
			wantOK: true,
		},
		{
			name: "each key of the block answers on its own",
			raw: "contract line\n" +
				"KIND: fix-red\n" +
				"TEST: ./internal/swarm TestSomething\n",
			key:    "TEST",
			want:   "./internal/swarm TestSomething",
			wantOK: true,
		},
		{
			name:   "the value is trimmed of the padding around it",
			raw:    "contract line\nPATHS:   internal/swarm, internal/ci  \n",
			key:    "PATHS",
			want:   "internal/swarm, internal/ci",
			wantOK: true,
		},
		{
			name:   "a key the block does not carry is refused",
			raw:    "contract line\nKIND: fix-red\n",
			key:    "LEGS",
			want:   "",
			wantOK: false,
		},
		{
			name: "a key line below the prose is outside the block and refused",
			raw: "contract line\n" +
				"KIND: fix-red\n" +
				"\n" +
				"the body begins here\n" +
				"PATHS: internal/swarm/lintheader.go\n",
			key:    "PATHS",
			want:   "",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value, ok := CardHeaderValue([]byte(tc.raw), tc.key)
			assert.Equal(t, tc.wantOK, ok, "ok says the block the gate reads carries the key")
			assert.Equal(t, tc.want, value, "the value is the key line's own")
		})
	}
}
