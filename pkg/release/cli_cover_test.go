package release

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCliCoverMain drives the production entry Main through Run: the help verb
// answers on stdout at exit 0, the usage refusals answer on stderr at exit 2,
// and the stamp Main threads into Deps.Self is what adopt reads when it refuses
// a coordinator older than the release. No case reaches a subprocess, a socket
// or a store: the help and usage refusals stop in Run's flag validation, and
// the stale-coordinator refusal fires before the SSH seam composes a command
// (pkg/release/adopt.go).
func TestCliCoverMain(t *testing.T) {
	t.Parallel()
	from := t.TempDir()
	machines := filepath.Join(t.TempDir(), "machines.tsv")
	require.NoError(t, os.WriteFile(machines, []byte("machine-one\n"), 0o644))
	for _, tc := range []struct {
		name     string
		stamp    string
		args     []string
		wantCode int
		wantIn   []string
		wantErr  []string
	}{
		{
			name:     "help-prints-the-release-verbs",
			stamp:    "v0.17.0",
			args:     []string{"help"},
			wantCode: 0,
			wantIn:   []string{"nova-update release cut", "nova-update release cycle", "exit codes:"},
		},
		{
			name:     "no-verb-refuses-naming-the-verbs",
			stamp:    "v0.17.0",
			args:     nil,
			wantCode: 2,
			wantErr:  []string{"RELEASE REFUSED", "a release verb is required", "run: nova-update help release"},
		},
		{
			name:     "unknown-verb-refuses-naming-the-verbs",
			stamp:    "v0.17.0",
			args:     []string{"launch"},
			wantCode: 2,
			wantErr:  []string{`RELEASE REFUSED: unknown release verb "launch"`, "run: nova-update help release"},
		},
		{
			name: "adopt-threads-the-stamp-and-refuses-a-stale-coordinator",
			// --version says which release; adopt then compares THIS binary,
			// the stamp threaded down by Main, against it and refuses: a
			// coordinator a release behind cannot fan that release out.
			stamp:    "v0.16.0",
			wantCode: 2,
			wantErr:  []string{"ADOPT REFUSED", "v0.16.0", "v0.17.0", "release install"},
			args: []string{"adopt", "--no-certify", "--version", "v0.17.0",
				"--machines", machines, "--ssh", "ssh", "--from", from,
				"--bin", "~/.local/bin", "--dest", "~/nova-release", "--platform", "linux-amd64"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errs bytes.Buffer
			code := Main("nova-update", tc.args, tc.stamp, &out, &errs)
			assert.Equal(t, tc.wantCode, code, "stderr=%s stdout=%s", errs.String(), out.String())
			for _, want := range tc.wantIn {
				assert.Contains(t, out.String(), want)
			}
			for _, want := range tc.wantErr {
				assert.Contains(t, errs.String(), want)
			}
		})
	}
}
