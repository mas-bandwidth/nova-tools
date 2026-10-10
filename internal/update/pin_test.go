package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// STEP 3: watch --adopt sends refused closing line to stderr while successful lines stay on stdout.
func TestWatchAdoptSeparatesStdoutAndStderr(t *testing.T) {
	t.Parallel()

	rig := fakeBusPath(t)
	checks := filepath.Join(t.TempDir(), "checks.tsv")
	rows := []string{
		"check\tcommand\towner",
		"check-pass\t" + printer(t, "pass-ok 1.0.0") + "\towner1",
		"check-fail\t" + printer(t, "fail-ok 2.0.0") + "\towner2",
	}
	if err := os.WriteFile(checks, []byte(strings.Join(rows, "\n")+"\n"), 0600); err != nil {
		require.NoError(t, err, err)
	}
	c, out, errs := run(t, rig.with(Environment{}), "watch", "--adopt", checks,
		"--as", "coordinator", "--to", "duty")
	require.EqualValues(t, 1, c, "want exit 1 with one refusal")
	assert.Contains(t, out, "ADOPT OK check=check-pass")
	assert.Contains(t, errs, "ADOPT REFUSED check=check-fail")
	assert.Contains(t, errs, "ADOPT DONE sha=")
	assert.False(t, strings.Contains(out, "ADOPT DONE"))
}
