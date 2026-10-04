package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/harness"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A launch whose binary is a headless harness builds its argv from the harness's own
// shape (swarm.HeadlessArgv), the model the route's part after its provider, and never
// asks the providers table; an opencode binary still goes through the table.
func TestAHeadlessLaunchHasTheHarnesssOwnArgv(t *testing.T) {
	t.Parallel()
	card := "card: do the thing\n"
	cfg := nativeRunConfig{binary: "/opt/bin/codex", model: "subscription-codex/gpt-6-astra", label: "lbl", card: []byte(card)}
	assert.Equal(t, harness.Codex, cfg.headless())
	argv, err := nativeLaunchArgv("/opt/bin/codex", cfg, "subscription-codex")
	require.NoError(t, err)
	want, _ := swarm.HeadlessArgv(harness.Codex, "/opt/bin/codex", "gpt-6-astra", card)
	assert.Equal(t, want, argv)
	cfg.binary = "/usr/local/bin/opencode"
	assert.Equal(t, "", cfg.headless())
	argv, err = nativeLaunchArgv("/usr/local/bin/opencode", cfg, "subscription-codex")
	require.NoError(t, err)
	assert.Equal(t, "run", argv[1], "an opencode binary launches through the providers table's row")
}

// The wall grants the headless harness's own home as a write where it exists, and the
// child is pointed at it by name (claude, codex) or by a link under its data home (grok).
func TestAHeadlessChildIsPointedAtTheHarnesssHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".codex"), 0o755))
	cfg := nativeRunConfig{binary: "codex", model: "subscription-codex/m", slotDir: "/s", benchHome: home, benchOS: "linux", noSharedCaches: true}
	argv := nativeSandboxArgv([]string{"codex"}, cfg, "/s/data", "/s/jobs/l", "/s/tmp/l")
	assert.Contains(t, argv, filepath.Join(home, ".codex"))
	cfg.binary = "claude" // no ~/.claude here: nothing to grant
	argv = nativeSandboxArgv([]string{"claude"}, cfg, "/s/data", "/s/jobs/l", "/s/tmp/l")
	assert.NotContains(t, argv, filepath.Join(home, ".claude"))

	data := t.TempDir()
	h := swarm.HeadlessHomeOf(harness.Grok, home)
	require.NoError(t, linkHeadlessHome(data, h))
	target, err := os.Readlink(filepath.Join(data, ".grok"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".grok"), target)
	require.NoError(t, linkHeadlessHome(data, h), "a link already there is kept")
	require.NoError(t, os.WriteFile(filepath.Join(data, ".claude"), nil, 0o644))
	assert.Error(t, linkHeadlessHome(data, swarm.HeadlessHome{Dir: home, Link: ".claude"}), "a file of the link's name is refused")
}

// A headless launch's usage row is read from its capture: the harness's figures, or
// dashes with the reason no-usage (nothing printed) or unreadable (a result that will
// not parse).
func TestAHeadlessLaunchsUsageIsReadFromItsCapture(t *testing.T) {
	t.Parallel()
	u, note, reason := headlessLaunchUsage(harness.Codex, []byte(`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}`+"\n"))
	assert.Empty(t, note)
	assert.Empty(t, reason)
	assert.Equal(t, "10", u.Values["tokens_in"])
	_, note, reason = headlessLaunchUsage(harness.Claude, []byte("nothing\n"))
	assert.Empty(t, note)
	assert.Equal(t, "no-usage", reason)
	_, note, reason = headlessLaunchUsage(harness.Claude, []byte("{\"usage\":{\"input_tokens\":1}\n"))
	assert.Contains(t, note, "could not be read")
	assert.Equal(t, "unreadable", reason)
}

// A packet whose route names a headless harness launches that program from this
// member's PATH; one this machine has not got refuses the launch, naming the harness,
// so the sprint deals the card elsewhere; a packet naming none runs the member's own.
func TestAMemberLaunchesAHeadlessRouteOnItsOwnProgram(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	r := &nativeRunner{harness: "/usr/local/bin/opencode", lookPath: func(name string) (string, error) {
		if name == "codex" {
			return filepath.Join(bin, "codex"), nil
		}
		return "", errors.New("not found: " + name)
	}}
	got, err := r.harnessFor(member.Packet{Card: "c1", Route: "heavy-codex", Harness: harness.Codex})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(bin, "codex"), got)
	got, err = r.harnessFor(member.Packet{Card: "c2", Route: "flash-a"})
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/opencode", got)
	_, err = r.harnessFor(member.Packet{Card: "c3", Route: "heavy-grok", Harness: harness.Grok})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "card c3's route heavy-grok runs under grok, which is on no PATH entry")
}

// The doctor reports each headless harness: on PATH or not, its version, its login.
func TestDoctorReportsEachHeadlessHarness(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{"/opt/go/bin/nova-swarm": doctorRebuiltLine, doctorLocal("/home/me"): doctorRebuiltLine},
		func(name string) (string, error) {
			switch name {
			case "claude":
				return "/u/bin/claude", nil
			case "codex":
				return "/u/bin/codex", nil
			}
			return "", errors.New("not found: " + name)
		}, "/home/me")
	env.run = func(binary string, args ...string) (string, int) {
		switch {
		case args[0] == "--version" && binary == "/u/bin/claude":
			return "2.1.220 (Claude Code)", 0
		case args[0] == "--version":
			return "codex-cli 0.153.4", 0
		case binary == "/u/bin/claude":
			return `{"loggedIn":false,"authMethod":"none"}`, 0
		}
		return "Logged in using ChatGPT", 0
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/nova-swarm", "--local", doctorLocal("/home/me")}, &out, &errOut)
	require.Equal(t, 0, code, errOut.String())
	assert.Equal(t, "DOCTOR OK stamp="+doctorRebuiltLine+"\n"+
		"DOCTOR HARNESS kind=claude binary=/u/bin/claude version=2.1.220 (Claude Code) login=no said=loggedIn=false authMethod=none\n"+
		"DOCTOR HARNESS kind=codex binary=/u/bin/codex version=codex-cli 0.153.4 login=yes said=Logged in using ChatGPT\n"+
		"DOCTOR HARNESS kind=grok binary=- version=- login=-\n", out.String())
}

// A headless harness has the same wall as an opencode child and no wider network: the
// loopback address a providers config names for a keyless provider is opened for an opencode
// child only, and the headless wall carries no --net-allow, and no --net-deny either (the
// wall makes no network promise to either; the web tools are off in the argv).
func TestAHeadlessWallOpensNoAddressBeyondAnOpencodeChilds(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"provider":{"subscription-codex":{"options":{"baseURL":"http://127.0.0.1:11434/v1"}},"ollama":{"options":{"baseURL":"http://127.0.0.1:11434/v1"}}}}`), 0o644))

	oc := nativeRunConfig{binary: "/usr/local/bin/opencode", model: "ollama/m", configFile: cfgPath}
	assert.Equal(t, "127.0.0.1:11434", nativeNetAllow(oc, "ollama"), "an opencode child's keyless provider is opened by name")

	hl := nativeRunConfig{binary: "/opt/bin/codex", model: "subscription-codex/m", configFile: cfgPath, slotDir: "/s", benchHome: t.TempDir(), benchOS: "linux", noSharedCaches: true}
	assert.Empty(t, nativeNetAllow(hl, "subscription-codex"), "a headless child is granted no address, whatever a config says")
	hl.netAllow = nativeNetAllow(hl, "subscription-codex")
	argv := nativeSandboxArgv([]string{"codex"}, hl, "/s/data", "/s/jobs/l", "/s/tmp/l")
	assert.NotContains(t, argv, "--net-allow")
	assert.NotContains(t, argv, "--net-deny")
}
