package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/harness"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
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

// A headless child runs from a private home under its data home, which the wall already
// writes, and the bench's own login directory is on no mount list at all: neither a write
// (a card could edit its config or hooks, or the credential) nor a read (its shell would
// read the whole login and the interactive history). Of that directory only the credential
// file is copied, 0600, to the private home; the bench's own file is only read.
func TestAHeadlessChildIsGivenNoMountOfTheBenchsLogin(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, k := range harness.Headless {
		require.NoError(t, os.MkdirAll(filepath.Join(home, "."+k, "sessions"), 0o755))
		cfg := nativeRunConfig{binary: k, model: "subscription-" + k + "/m", slotDir: "/s", benchHome: home, benchOS: "linux", noSharedCaches: true}
		argv := nativeSandboxArgv([]string{k}, cfg, "/s/data", "/s/jobs/l", "/s/tmp/l")
		for _, a := range argv {
			assert.False(t, strings.HasPrefix(a, filepath.Join(home, "."+k)), "%s: the bench's login directory is mounted: %s", k, a)
		}
		i := slices.Index(argv, "/s/data")
		require.GreaterOrEqual(t, i, 1)
		assert.Equal(t, "--write", argv[i-1], "%s: the private home sits in the data home, the one write it needs", k)
	}
}

func TestAHeadlessChildsPrivateHomeHoldsItsCredentialAndNothingElse(t *testing.T) {
	t.Parallel()
	home, data := t.TempDir(), t.TempDir()
	login := filepath.Join(home, ".codex")
	require.NoError(t, os.MkdirAll(filepath.Join(login, "archived_sessions"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(login, "auth.json"), []byte(`{"tokens":"x"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(login, "config.toml"), []byte("hooks = 1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(login, "archived_sessions", "s.jsonl"), []byte("history\n"), 0o600))

	// a previous card's leftovers in the private home are gone
	h := swarm.HeadlessHomeOf(harness.Codex, home, data)
	require.NoError(t, os.MkdirAll(h.Dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(h.Dir, "old-session.jsonl"), nil, 0o600))

	require.NoError(t, seedHeadlessHome(h))
	names, err := os.ReadDir(h.Dir)
	require.NoError(t, err)
	require.Len(t, names, 1, "the credential alone")
	assert.Equal(t, "auth.json", names[0].Name())
	b, err := os.ReadFile(filepath.Join(h.Dir, "auth.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"tokens":"x"}`, string(b))
	fi, err := os.Stat(filepath.Join(h.Dir, "auth.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	// the card writing its copy leaves the bench's login as it was
	require.NoError(t, os.WriteFile(filepath.Join(h.Dir, "auth.json"), []byte("tampered"), 0o600))
	b, err = os.ReadFile(filepath.Join(login, "auth.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"tokens":"x"}`, string(b))

	// a bench with no login for the harness gets an empty private home: the harness says
	// logged out, a provider failure of class auth
	h = swarm.HeadlessHomeOf(harness.Grok, home, data)
	require.NoError(t, seedHeadlessHome(h))
	names, err = os.ReadDir(h.Dir)
	require.NoError(t, err)
	assert.Empty(t, names)
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

// A codex install lives inside the bench's own ~/.codex (its `bin/codex` is reached through
// `~/.local/bin/codex`): the wall reads that install, by the resolved path, and still not
// the login directory around it.
func TestAHeadlessChildReadsItsInstallAndNotTheLoginAroundIt(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	release := filepath.Join(home, ".codex", "packages", "standalone", "releases", "0.153.4")
	bin := filepath.Join(release, "bin", "codex")
	cfg := nativeRunConfig{binary: "codex", model: "subscription-codex/m", slotDir: "/s", benchHome: home, benchOS: "linux", noSharedCaches: true}
	argv := nativeSandboxArgv([]string{bin}, cfg, "/s/data", "/s/jobs/l", "/s/tmp/l")
	reads := map[string]bool{}
	for i, a := range argv {
		if a == "--read" {
			reads[argv[i+1]] = true
		}
		assert.NotEqual(t, filepath.Join(home, ".codex"), a, "the login directory itself is on no mount list")
	}
	assert.True(t, reads[filepath.Join(release, "bin")], "the binary's directory is read")
	assert.True(t, reads[release], "so is the install above bin, where codex keeps its resources")
	assert.Equal(t, release, swarm.HeadlessProgramRoot(bin))
	assert.Equal(t, "/v/claude/versions", swarm.HeadlessProgramRoot("/v/claude/versions/2.1.220"), "a binary outside a bin directory is read by its own directory")
}

// claude's private home is seeded with nothing of the bench's login: its `.credentials.json`
// (a fake here) is a refreshable OAuth login, and a copy refreshed inside a turn would strand
// the bench's own refresh token. The bench's file is left as it was, and the child's login is
// the token the run hands it by name.
func TestClaudesPrivateHomeIsGivenNoCopyOfTheBenchsCredential(t *testing.T) {
	t.Parallel()
	home, data := t.TempDir(), t.TempDir()
	login := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(login, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(login, ".credentials.json"), []byte(`{"fake":"not a real login"}`), 0o600))

	h := swarm.HeadlessHomeOf(harness.Claude, home, data)
	require.NoError(t, os.MkdirAll(h.Dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.Dir, ".credentials.json"), []byte("an earlier card's copy"), 0o600))
	require.NoError(t, seedHeadlessHome(h))
	names, err := os.ReadDir(h.Dir)
	require.NoError(t, err)
	assert.Empty(t, names, "no credential file is copied, and an earlier copy is gone")
	b, err := os.ReadFile(filepath.Join(login, ".credentials.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"fake":"not a real login"}`, string(b))
	assert.Equal(t, "CLAUDE_CODE_OAUTH_TOKEN", h.Token)
}
