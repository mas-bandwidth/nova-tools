//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// audit_functional_test.go runs audit.yml through ansible-core on localhost (in the bench group) against a requirements
// file of its own: a met requirement, an unmet one and one declared for another architecture. On Linux the unmet one is
// named and the machine fails; another OS is skipped by name.
func TestFunctionalAuditNamesWhatIsMissing(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; audit_play_test.go still runs")
	}
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	dir := t.TempDir()
	other := "arm64"
	if runtime.GOARCH == "arm64" {
		other = "amd64"
	}
	reqs := filepath.Join(dir, "reqs.tsv")
	require.NoError(t, os.WriteFile(reqs, []byte("# a test file\n"+
		"met\tany\tbase\ttrue\n"+
		"unmet thing\tany\trunner-packages\ttest -e /nonexistent/audit\n"+
		"elsewhere\t"+other+"\tbase\tfalse\n"), 0o644))
	inv := filepath.Join(dir, "inventory")
	require.NoError(t, os.WriteFile(inv, []byte("[bench]\nlocalhost ansible_connection=local\n"), 0o644))
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", "-i", inv, filepath.Join(fleet, "audit.yml"), "-e", "nova_audit_requirements="+reqs)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+home, "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"))
	b, err := cmd.CombinedOutput()
	out := string(b)

	if runtime.GOOS != "linux" {
		require.NoError(t, err, out)
		assert.Contains(t, out, "FLEET-AUDIT SKIPPED host=localhost")
		return
	}
	require.Error(t, err, out)
	assert.Contains(t, out, "FLEET-AUDIT MISSING host=localhost item=unmet thing install=runner-packages check=test -e /nonexistent/audit")
	assert.NotContains(t, out, "item=met ")
	assert.Contains(t, out, "FLEET-AUDIT NOT-APPLICABLE host=localhost item=elsewhere")
	assert.Contains(t, out, "ok=1")
	assert.Contains(t, out, "missing=1 not-applicable=1")
	assert.Contains(t, out, "FLEET-AUDIT FAILED host=localhost missing=unmet thing")
}
