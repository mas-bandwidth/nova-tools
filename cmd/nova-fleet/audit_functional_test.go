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
	"gopkg.in/yaml.v3"
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
	cmd := exec.CommandContext(ctx, "ansible-playbook", "-i", inv, filepath.Join(fleet, "audit.yml"), "-e", "nova_audit_requirements="+reqs,
		"-e", "nova_audit_memlock=false", "-e", "nova_audit_job=false")
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
	assert.Contains(t, out, " ok=1 missing=1 not-applicable=1")
	assert.Contains(t, out, "FLEET-AUDIT FAILED host=localhost missing=unmet thing")
}

// TestFunctionalAuditMemlockNamesTheOddOneOut gives the audit a nova-reserve whose command reports a 1 KiB locked-memory
// limit, below and unlike this session's: on Linux the machine fails on memlock by name, with the three values and the
// play that fixes the session's; another OS is skipped by name.
func TestFunctionalAuditMemlockNamesTheOddOneOut(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; audit_play_test.go still runs")
	}
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	dir := t.TempDir()
	reqs := filepath.Join(dir, "reqs.tsv")
	require.NoError(t, os.WriteFile(reqs, []byte("met\tany\tbase\ttrue\n"), 0o644))
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	// the command a reserve slot would run, as one whose limits are 1 KiB soft and hard
	require.NoError(t, os.WriteFile(filepath.Join(bin, "nova-reserve"), []byte("#!/bin/sh\necho '1 1'\n"), 0o755))
	inv := filepath.Join(dir, "inventory")
	require.NoError(t, os.WriteFile(inv, []byte("[bench]\nlocalhost ansible_connection=local\n"), 0o644))
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", "-i", inv, filepath.Join(fleet, "audit.yml"), "-e", "nova_audit_requirements="+reqs,
		"-e", "nova_bin_dir="+bin, "-e", "nova_audit_job=false")
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+home, "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"))
	b, err := cmd.CombinedOutput()
	out := string(b)

	if runtime.GOOS != "linux" {
		require.NoError(t, err, out)
		assert.Contains(t, out, "FLEET-AUDIT SKIPPED host=localhost")
		return
	}
	require.Error(t, err, out)
	assert.Regexp(t, `FLEET-AUDIT MISSING host=localhost item=memlock ssh=\S+ reserve=1024/1024 runner=\S+ need=unlimited/unlimited install=health`, out)
	assert.Contains(t, out, " ok=1 missing=1 not-applicable=0")
	assert.Contains(t, out, "FLEET-AUDIT FAILED host=localhost missing=memlock")
}

// TestFunctionalAuditJobProbeNamesTheStepThatCannotWrite runs the audit's job probe (audit_job_probe in audit.yml) under
// bash with fake rustup, go and cmake: a rustup whose home the login cannot write and a CCACHE_DIR it cannot
// write are each named with the reason, the steps that work pass, and the probe's directory is gone after.
func TestFunctionalAuditJobProbeNamesTheStepThatCannotWrite(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the job probe runs on Linux machines only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only directory")
	}
	b, err := os.ReadFile("audit.yml")
	require.NoError(t, err)
	var docs []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &docs))
	probe := ""
	for _, task := range mapsOf(t, docs[0]["tasks"]) {
		if vars, ok := task["vars"].(map[string]any); ok {
			if p, ok := vars["audit_job_probe"].(string); ok {
				probe = p
			}
		}
	}
	require.NotEmpty(t, probe, "audit.yml has the job probe")

	dir := t.TempDir()
	bin, tmp, home, ro := filepath.Join(dir, "bin"), filepath.Join(dir, "tmp"), filepath.Join(dir, "home"), filepath.Join(dir, "ro")
	for _, d := range []string{bin, tmp, home, ro} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	fakes := map[string]string{
		"rustup": "#!/bin/sh\n[ \"$1\" = show ] && { echo " + ro + "; exit 0; }\nexit 0\n",
		"go":     "#!/bin/sh\nexit 0\n",
		"cmake":  "#!/bin/sh\nexit 0\n",
	}
	for name, body := range fakes {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755))
	}
	require.NoError(t, os.Chmod(ro, 0o555))
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) }) // ignored: TempDir's removal reports a failure

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", probe)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "TMPDIR=" + tmp, "HOME=" + home, "CCACHE_DIR=" + filepath.Join(ro, "ccache")}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	s := string(out)
	assert.Regexp(t, `(?m)^MISSING rustup .*`, s)
	assert.Contains(t, s, "OK go\n")
	assert.Contains(t, s, "OK cmake\n")
	assert.Contains(t, s, "MISSING cache-dirs CCACHE_DIR="+filepath.Join(ro, "ccache")+" is not writable by ")
	left, err := filepath.Glob(filepath.Join(tmp, "fleet-audit-job.*"))
	require.NoError(t, err)
	assert.Empty(t, left, "the probe's directory is removed")
}
