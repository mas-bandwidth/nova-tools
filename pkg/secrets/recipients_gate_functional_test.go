//go:build functional

package secrets

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The store's gate judges every rule and file pair the way check and seat
// inject do (TestCheckAndSeatInjectAgreeOnEveryRuleAndFile): each pair is a
// pull request adding the rule and the file over one base, read out of a real
// git store, and the gate approves exactly the pairs the other two accept.
func TestTheGateAgreesOnEveryRuleAndFile(t *testing.T) {
	t.Parallel()
	pairs := recipientPairs()
	changes := make([]map[string]string, len(pairs))
	for i, p := range pairs {
		changes[i] = map[string]string{".sops.yaml": p.sops, "rowan.yaml": p.sealed}
	}
	dir := gateStart(t)
	base := gateHead(t, dir)
	heads := gateCommits(t, dir, base, changes)
	for i, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: heads[i]})
			assert.Equal(t, p.want, code == 0, "gate: %s", line)
		})
	}
}

// gateCommits makes one commit per entry of changes, each a child of base
// holding base's files with that entry's files written over them, in one
// git fast-import, and returns their shas in order. HEAD and the work tree
// stay at base; each commit is on its own branch (case-<n>), so a gate run
// over base..sha reads only git's objects, never the work tree, and the
// runs can share the store.
func gateCommits(t *testing.T, dir, base string, changes []map[string]string) []string {
	t.Helper()
	var in bytes.Buffer
	for i, files := range changes {
		fmt.Fprintf(&in, "commit refs/heads/case-%d\nmark :%d\ncommitter Test <test@example.com> 1000 +0000\ndata 6\nchange\nfrom %s\n", i, i+1, base)
		for _, name := range slices.Sorted(maps.Keys(files)) {
			fmt.Fprintf(&in, "M 100644 inline %s\ndata %d\n%s\n", name, len(files[name]), files[name])
		}
	}
	marks := filepath.Join(t.TempDir(), "marks")
	cmd := exec.Command("git", "-C", dir, "fast-import", "--quiet", "--export-marks="+marks)
	cmd.Stdin = &in
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git fast-import: %s", out)
	raw, err := os.ReadFile(marks)
	require.NoError(t, err)
	shas := make([]string, len(changes))
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var n int
		var sha string
		_, err := fmt.Sscanf(line, ":%d %s", &n, &sha)
		require.NoError(t, err, line)
		require.True(t, n >= 1 && n <= len(changes), line)
		shas[n-1] = sha
	}
	for i, sha := range shas {
		require.NotEmpty(t, sha, "commit %d", i)
	}
	return shas
}
