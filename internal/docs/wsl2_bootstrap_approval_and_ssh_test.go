package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsl2_bootstrap_approval_and_ssh_test.go holds the two behaviours #1458's title names that
// TestWSL2BootstrapScriptCarriesTheOneStepSetup (wsl2bench_test.go) does not
// discriminate. That test asserts each step's text is present and the auth key
// is never printed; it still passes if the script asks a second question, if
// the elevation guard runs after the first host change, or if the ssh probe
// that proves "the keeper adopts over ssh" is deleted (the word "sshd" stays in
// the CHECK lines). These tests fail on each of those.
//
// They read the script as text and run nothing; a Windows box is not on this
// host and the script is not executable here.

// issue1458Code returns the script with whole-line comments dropped, so a
// comment that mentions a word never satisfies or trips a behaviour check.
func issue1458Code(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(wsl2BootstrapPath)
	require.NoError(t, err, "%s: %v", wsl2BootstrapPath, err)
	var code []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	return strings.Join(code, "\n")
}

// issue1458Index is the offset of want in code, failing the test when absent.
func issue1458Index(t *testing.T, code, want string) int {
	t.Helper()
	i := strings.Index(code, want)
	require.GreaterOrEqual(t, i, 0, "tools/bench-wsl2.ps1 has no %q", want)
	return i
}

// One approval: the elevated run and the auth key parameter are the only
// things a person supplies. Nothing prompts again, nothing re-elevates, and the
// elevation guard refuses before the first change to the host.
func TestWSL2BootstrapScriptRequiresSingleApproval(t *testing.T) {
	t.Parallel()
	code := issue1458Code(t)
	lower := strings.ToLower(code)

	for _, prompt := range []string{"read-host", "get-credential", "$host.ui.prompt", "-verb runas", "pause", "-confirm"} {
		assert.NotContains(t, lower, prompt, "tools/bench-wsl2.ps1 runs %q: a second prompt or elevation breaks #1458's one approval", prompt)
	}

	const mandatory = "[Parameter(Mandatory = $true)]"
	param := issue1458Index(t, code, mandatory)
	key := issue1458Index(t, code, "[string]$AuthKey")
	assert.True(t, key >= param && strings.TrimSpace(code[param+len(mandatory):key]) == "", "tools/bench-wsl2.ps1: $AuthKey is not the mandatory parameter; the key must arrive with the one approval, not later")

	guard := issue1458Index(t, code, "WindowsBuiltInRole]::Administrator")
	refuse := issue1458Index(t, code[guard:], "Refuse ")
	first := issue1458Index(t, code, "winget install")
	assert.LessOrEqual(t, guard+refuse, first, "tools/bench-wsl2.ps1: the elevation guard does not refuse before the first host change (winget install); a non-elevated run would change the host and then fail")
}

// The keeper adopts over ssh: the run is green only after a non-interactive
// ssh to the nova user on the tailnet address answers, and a failed probe is a
// refusal. The OK line and exit 0 come after that probe and nowhere else.
func TestWSL2BootstrapGreensOnlyAfterSshProbe(t *testing.T) {
	t.Parallel()
	code := issue1458Code(t)

	enable := issue1458Index(t, code, "systemctl enable --now ssh")
	probe := issue1458Index(t, code, "ssh -o BatchMode=yes")
	line := code[probe:]
	if nl := strings.Index(line, "\n"); nl >= 0 {
		line = line[:nl]
	}
	assert.Contains(t, line, `"$User@$tailIP"`, "tools/bench-wsl2.ps1 ssh probe %q does not target $User@$tailIP, the address the keeper adopts over", line)
	assert.GreaterOrEqual(t, probe, enable, "tools/bench-wsl2.ps1 probes ssh before sshd is enabled in the distro")

	check := issue1458Index(t, code[probe:], "if ($LASTEXITCODE -ne 0)")
	refuse := issue1458Index(t, code[probe+check:], "Refuse ")
	ok := issue1458Index(t, code, "BENCH-WSL2 OK")
	assert.LessOrEqual(t, probe+check+refuse, ok, "tools/bench-wsl2.ps1: a failed ssh probe does not refuse before the BENCH-WSL2 OK line")
	assert.Equal(t, 1, strings.Count(code, "BENCH-WSL2 OK"), "tools/bench-wsl2.ps1 has more than one green exit; the only green is after the ssh probe")
	assert.Equal(t, 1, strings.Count(code, "exit 0"), "tools/bench-wsl2.ps1 has more than one green exit; the only green is after the ssh probe")
	assert.GreaterOrEqual(t, issue1458Index(t, code, "exit 0"), ok, "tools/bench-wsl2.ps1 exits 0 before the ssh probe's OK line")
}
