package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE NATIVE OPENCODE PATH (issue #296, slice 2). A frozen run configuration is executed
// directly against the real fake harness, with no pool, no worker description and no
// supervisor: the binary, the model, the card text, the slot and the deadline are the whole
// truth. These tests run the same fake harness the dispatcher tests run, so the child that
// starts is the one whose argv, sleep and exit the machinery already trusts.

func nativeHarness(t *testing.T) string {
	t.Helper()
	bin, err := build(t, t.TempDir(), "fake-harness", "./cmd/nova-swarm/testdata/fakeharness")
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// nativeSandbox builds the fake sandbox of the seam tests: a stand-in for nova-sandbox that
// records its argv and, under NOVA_FAKE_SANDBOX=hosts, reports hosts=enforceable so the
// repo allow rule reaches the argv.
func nativeSandbox(t *testing.T) string {
	t.Helper()
	bin, err := build(t, t.TempDir(), "fake-sandbox", "./cmd/nova-swarm/testdata/fakesandbox")
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// nativeSandboxOnPath puts the fake sandbox on PATH under its own name (`nova-sandbox`), so
// the native run resolves the wall itself rather than being handed a --sandbox path. It
// returns the directory that now names the wall on PATH.
func nativeSandboxOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := build(t, dir, "nova-sandbox", "./cmd/nova-swarm/testdata/fakesandbox"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// sandboxArgv reads the argv the wall recorded into the job directory, if any.
func sandboxArgv(t *testing.T, jobDir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(jobDir, "sandbox-argv"))
	if err != nil {
		t.Fatalf("the wall recorded no argv under %s: %v", jobDir, err)
	}
	return string(raw)
}

// TestNativeArgvReadsHarnessDir: the wall's argv reads the harness binary's own directory
// and /opt/homebrew (when it exists), so git and the harness's libraries resolve inside the
// wall — the reads the shell launcher made, which the native path of run 7 must make too.
func TestNativeArgvReadsHarnessDir(t *testing.T) {
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argv := nativeSandboxArgv(bin, nativeRunConfig{slotDir: slot}, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	harnessDir := filepath.Dir(bin)
	if !hasFlagPair(argv, "--read", harnessDir) {
		t.Errorf("the wall argv does not read the harness directory %s:\n%s", harnessDir, strings.Join(argv, " "))
	}
	if fi, err := os.Stat("/opt/homebrew"); err == nil && fi.IsDir() {
		if !hasFlagPair(argv, "--read", "/opt/homebrew") {
			t.Errorf("the wall argv does not read /opt/homebrew, which exists:\n%s", strings.Join(argv, " "))
		}
	} else if hasFlagPair(argv, "--read", "/opt/homebrew") {
		t.Errorf("the wall argv reads /opt/homebrew, which is absent:\n%s", strings.Join(argv, " "))
	}
}

func hasFlagPair(argv []string, flag, val string) bool {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) && argv[i+1] == val {
			return true
		}
	}
	return false
}

// hasFlagPairResolved is hasFlagPair with the value compared by symlink-resolved path: on
// macOS t.TempDir() lands under /var -> /private/var, so the wall argv carries the resolved
// spelling while the test holds the unresolved one.
func hasFlagPairResolved(argv []string, flag, want string) bool {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			if got, err := filepath.EvalSymlinks(argv[i+1]); err == nil && got == want {
				return true
			}
		}
	}
	return false
}

// aSlot returns a slot dir and the root it is under, both fresh.
func aSlot(t *testing.T) (root, slot string) {
	t.Helper()
	root = t.TempDir()
	slot = filepath.Join(root, "slot-1")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, slot
}

// TestNativeRunRefusesMissingBinary: a binary that does not exist, and one that exists but
// is not executable, are both the refusal that runs before any child can start.
func TestNativeRunRefusesMissingBinary(t *testing.T) {
	root, slot := aSlot(t)
	for _, tc := range []struct {
		name   string
		binary string
		word   string
	}{
		{"missing", filepath.Join(t.TempDir(), "no-such-binary"), "missing"},
		{"not_executable", func() string {
			p := filepath.Join(t.TempDir(), "data")
			if err := os.WriteFile(p, []byte("not a program\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return p
		}(), "not executable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: tc.binary, model: "fake/fake-model", label: "lbl",
				card: []byte("a card\n"), slotDir: slot, root: root, deadline: time.Second,
			}, &errOut)
			if code != 2 {
				t.Fatalf("a bad binary exits 2, got %d:\n%s", code, errOut.String())
			}
			if !strings.Contains(errOut.String(), "NATIVE REFUSED") {
				t.Fatalf("the refusal is one REFUSED line, got:\n%s", errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.word) {
				t.Fatalf("the refusal names its reason (%s):\n%s", tc.word, errOut.String())
			}
			if got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1; got != 1 {
				t.Fatalf("exactly one REFUSED line, got %d:\n%s", got, errOut.String())
			}
		})
	}
}

// TestNativeRunRecordsCardAndBinaryHashes: a run that finishes records the child's exit
// code, the wall it took, and the sha256 of the card text and of the binary itself.
func TestNativeRunRecordsCardAndBinaryHashes(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := []byte("the card text, byte for byte\nwith a second line\n")

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "a-label",
		card: card, slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("a finished run exits 0, got %d:\n%s", code, errOut.String())
	}
	if res.rc != 0 {
		t.Fatalf("the child exits 0, recorded %d", res.rc)
	}
	if res.wallSeconds <= 0 {
		t.Fatalf("the wall is positive, recorded %v", res.wallSeconds)
	}
	wantCardSum := sha256.Sum256(card)
	wantCard := hex.EncodeToString(wantCardSum[:])
	if res.cardSHA256 != wantCard {
		t.Errorf("card sha256 is %s, want %s", res.cardSHA256, wantCard)
	}
	wantBinary, err := fileSHA256(bin)
	if err != nil {
		t.Fatal(err)
	}
	if res.binarySHA256 != wantBinary {
		t.Errorf("binary sha256 is %s, want %s", res.binarySHA256, wantBinary)
	}
}

// TestNativeRunKillsAtDeadline: a child that sleeps past the wall is killed by it, and the
// run records a non-zero exit rather than hanging.
func TestNativeRunKillsAtDeadline(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	start := time.Now()
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card: []byte("FAKE-SLEEP 60\n"), slotDir: slot, root: root, deadline: time.Second, noWall: true,
	}, &errOut)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("a deadline kill is not a refusal, got exit %d:\n%s", code, errOut.String())
	}
	if res.rc == 0 {
		t.Fatalf("the deadline killed the child, and the run records a non-zero exit")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the deadline should cut the run short, but it took %v", elapsed)
	}
}

// TestNativeRunAuthCopyIs0600: the named provider's entry is copied from the auth file into
// the data home, mode 0600, and no other provider's entry travels with it.
func TestNativeRunAuthCopyIs0600(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret","other":"the-other-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, authFile: auth, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("an 0600 auth copy runs, got exit %d:\n%s", code, errOut.String())
	}
	copied := filepath.Join(slot, "data", "auth.json")
	st, err := os.Stat(copied)
	if err != nil {
		t.Fatalf("the auth copy was not written: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the auth copy is mode %04o, want 0600", st.Mode().Perm())
	}
	body, err := os.ReadFile(copied)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"fake":"the-fake-secret"}` {
		t.Errorf("the copy holds only the named provider entry, got %s", body)
	}
}

// TestNativeCarriesProviderConfig: a `--config` opencode.json is copied beside the carried
// auth file into the job's own data home, mode 0600, byte for byte, and the fake harness
// sees it at the path it resolves from its own XDG data home. Without --config the file is
// absent, and with it the NATIVE OK line records its sha8.
func TestNativeCarriesProviderConfig(t *testing.T) {
	bin := nativeHarness(t)
	const config = `{"provider":{"fake":{"options":{"baseURL":"http://localhost:11434/v1"}}}}` + "\n"

	t.Run("without_config", func(t *testing.T) {
		root, slot := aSlot(t)
		auth := filepath.Join(t.TempDir(), "auth.json")
		if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("FAKE-RECORD-CONFIG\n"), slotDir: slot, root: root, authFile: auth,
			deadline: 30 * time.Second, noWall: true,
		}, &errOut)
		if code != 0 {
			t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
		}
		assertConfigRecord(t, slot, "absent", "")
	})

	t.Run("with_config", func(t *testing.T) {
		root, slot := aSlot(t)
		auth := filepath.Join(t.TempDir(), "auth.json")
		if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		cfgPath := filepath.Join(t.TempDir(), "opencode.json")
		if err := os.WriteFile(cfgPath, []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("FAKE-RECORD-CONFIG\n"), slotDir: slot, root: root, authFile: auth,
			configFile: cfgPath, deadline: 30 * time.Second, noWall: true,
		}, &errOut)
		if code != 0 {
			t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
		}
		wantSum := sha256.Sum256([]byte(config))
		wantSHA := hex.EncodeToString(wantSum[:])[:8]
		if res.configSHA != wantSHA {
			t.Errorf("the run records config sha8 %q, want %q", res.configSHA, wantSHA)
		}
		assertConfigRecord(t, slot, "0600", config)
		copied := filepath.Join(slot, "data", ".config", "opencode", "opencode.json")
		st, err := os.Stat(copied)
		if err != nil {
			t.Fatalf("the config copy was not written: %v", err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("the config copy is mode %04o, want 0600", st.Mode().Perm())
		}
		body, err := os.ReadFile(copied)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != config {
			t.Errorf("the config copy bytes differ:\n%s", body)
		}
	})
}

// assertConfigRecord reads what the fake harness recorded about the provider config and
// proves it saw the file (or its absence) at its own XDG data home path.
func assertConfigRecord(t *testing.T, slot, wantMode, wantBody string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(slot, "jobs", "lbl", "config-record"))
	if err != nil {
		t.Fatalf("the harness recorded no config-record: %v", err)
	}
	rec := string(raw)
	if wantMode == "absent" {
		if rec != "absent\n" {
			t.Errorf("the harness saw a config where none should be: %q", rec)
		}
		return
	}
	if !strings.HasPrefix(rec, "mode="+wantMode+"\n") {
		t.Errorf("the harness saw %q, want mode %s", rec, wantMode)
	}
	if wantBody != "" && !strings.HasSuffix(rec, "\n"+wantBody) {
		t.Errorf("the harness saw different bytes:\n%s", rec)
	}
}

// TestNativeRefusesConfigProviderWithoutKey: a --config whose entry for THE MODEL'S OWN
// provider has no key in --auth is refused before anything runs, in one line, naming the
// provider and never the key. That provider is the one the harness is about to call, so its
// missing key is a run that dies rc=1 in under a second.
func TestNativeRefusesConfigProviderWithoutKey(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(cfgPath, []byte(`{"provider":{"fake":{},"zeta":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "zeta/zeta-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, authFile: auth,
		configFile: cfgPath, deadline: time.Second,
	}, &errOut)
	if code != 2 {
		t.Fatalf("a config whose entry for the model's provider has no key exits 2, got %d:\n%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "NATIVE REFUSED") {
		t.Fatalf("the refusal is one REFUSED line:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "zeta") {
		t.Fatalf("the refusal names the provider, never the key:\n%s", errOut.String())
	}
	if strings.Contains(errOut.String(), "the-fake-secret") {
		t.Fatalf("the refusal never prints a key:\n%s", errOut.String())
	}
	if got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1; got != 1 {
		t.Fatalf("exactly one REFUSED line, got %d:\n%s", got, errOut.String())
	}
}

// TestNativeConfigChecksOnlyTheModelsProvider: a --config may name every provider a person
// keeps in ~/.config/opencode, and only the one the --model names is checked for a key. The
// config here names two -- a keyless ollama the model uses, and an inception that has no
// entry in --auth and that this run never calls -- and the run is admitted. Checking all of
// them turned every adoption pass on this bench into `names provider inception, whose key is
// absent` for a card that wanted a local model (issue #523 follow-up).
func TestNativeConfigChecksOnlyTheModelsProvider(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	const config = `{"provider":{"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}},"inception":{}}}` + "\n"
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(cfgPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "ollama/north-mini-code-32k", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, authFile: auth,
		configFile: cfgPath, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("a provider the model does not use is not checked, got exit %d:\n%s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "NATIVE REFUSED") {
		t.Fatalf("no refusal for a provider this run never calls:\n%s", errOut.String())
	}
	if strings.Contains(errOut.String(), "inception") {
		t.Fatalf("the unused provider is not named at all:\n%s", errOut.String())
	}
}

// TestNativeConfigKeylessProviderAdmitted: a --config that names a provider whose entry is
// absent from --auth is admitted, not refused, when that provider's options carry a baseURL
// and no apiKey field -- ollama on localhost needs no key, so there is no key to be absent.
// The config is still copied verbatim, mode 0600, and the run records its sha8.
func TestNativeConfigKeylessProviderAdmitted(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	const config = `{"provider":{"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}}}}` + "\n"
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(cfgPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "ollama/north-mini-code-32k", label: "lbl",
		card: []byte("FAKE-RECORD-CONFIG\n"), slotDir: slot, root: root, authFile: auth,
		configFile: cfgPath, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("a keyless provider (baseURL, no apiKey) is admitted, got exit %d:\n%s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "NATIVE REFUSED") {
		t.Fatalf("the keyless provider is not refused, got:\n%s", errOut.String())
	}
	wantSum := sha256.Sum256([]byte(config))
	wantSHA := hex.EncodeToString(wantSum[:])[:8]
	if res.configSHA != wantSHA {
		t.Errorf("the run records config sha8 %q, want %q", res.configSHA, wantSHA)
	}
	assertConfigRecord(t, slot, "0600", config)
}

// TestFriendSequenceLocalModelCard runs one known-answer card on a fake local provider: the
// harness is the fake, the provider is a keyless ollama (baseURL, no apiKey, no auth entry),
// and the card FAKE-PWD answers with the job directory. The run is walled, admitted without a
// refusal, and the wall's own name and the card's known answer both land where a reader looks.
func TestFriendSequenceLocalModelCard(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	const config = `{"provider":{"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}}}}` + "\n"
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(cfgPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	label := "local-model-card"

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "ollama/north-mini-code-32k", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, authFile: auth,
		configFile: cfgPath, deadline: 30 * time.Second, sandbox: sandbox,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the keyless local provider runs walled, got exit %d:\n%s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "NATIVE REFUSED") {
		t.Fatalf("the local provider card is admitted, not refused:\n%s", errOut.String())
	}
	if res.wall != "fake-wall" {
		t.Errorf("the run names the wall it ran inside, got %q", res.wall)
	}
	jobDir := filepath.Join(slot, "jobs", label)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("the card's known answer was not written: %v", err)
	}
	got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
	if !sameDir(got, jobDir) {
		t.Errorf("the card's known answer is %q, want the job directory %q", got, jobDir)
	}
}

// TestNativeRunRefusalsNameTheirReason drives the remaining three refusals -- a model with
// no provider prefix, an auth file looser than 0600, and a slot outside its root -- so each
// prints its one REFUSED line.
func TestNativeRunRefusalsNameTheirReason(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	looseAuth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(looseAuth, []byte(`{"fake":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-slot")

	cases := []struct {
		name string
		cfg  nativeRunConfig
		word string
	}{
		{
			"model_no_prefix",
			nativeRunConfig{binary: bin, model: "no-prefix", card: []byte("x\n"), slotDir: slot, root: root, deadline: time.Second},
			"no provider prefix",
		},
		{
			"auth_not_0600",
			nativeRunConfig{binary: bin, model: "fake/fake-model", card: []byte("x\n"), slotDir: slot, root: root, authFile: looseAuth, deadline: time.Second},
			"would not be 0600",
		},
		{
			"slot_outside_root",
			nativeRunConfig{binary: bin, model: "fake/fake-model", card: []byte("x\n"), slotDir: outside, root: root, deadline: time.Second},
			"outside the configured root",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			_, code := nativeRun(tc.cfg, &errOut)
			if code != 2 {
				t.Fatalf("the refusal exits 2, got %d:\n%s", code, errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.word) {
				t.Fatalf("the refusal names its reason (%s):\n%s", tc.word, errOut.String())
			}
		})
	}
}

func TestCmdNativeCLI(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("test card line 1\nline 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Missing flags -> exit 2 with refusal
	var stdout, stderr bytes.Buffer
	rc := run([]string{"native"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 2 {
		t.Fatalf("missing flags must exit 2, got %d", rc)
	}
	if !strings.Contains(stderr.String(), "--harness is required") {
		t.Fatalf("expected --harness is required, got:\n%s", stderr.String())
	}

	// Success run -> exit 0 with NATIVE OK
	stdout.Reset()
	stderr.Reset()
	args := []string{
		"native",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", "test-label",
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "10s",
		"--no-wall",
	}
	rc = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("native run must exit 0, got %d:\nstdout: %s\nstderr: %s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("stdout must contain NATIVE OK, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "label=test-label") {
		t.Fatalf("stdout must contain label=test-label, got:\n%s", stdout.String())
	}
}

// TestNativeRunChildDirIsJobDir: the child runs in its job directory <slot>/jobs/<label>,
// told its place by its cwd, not the caller's. The fake harness writes its own working
// directory into RESULT.md, and the test asserts it is exactly the job directory.
func TestNativeRunChildDirIsJobDir(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "a-label"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	jobDir := filepath.Join(slot, "jobs", label)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("the child did not write pwd into RESULT.md under the job directory: %v", err)
	}
	got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
	want, err := filepath.EvalSymlinks(jobDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("the child's cwd is %q, want the job directory %q", got, want)
	}
}

// TestNativeChildCwdIsJobDirFromForeignCwd: the walled child also runs in the job
// directory, and it does so even when the caller's own cwd is somewhere else entirely.
// The test chdirs away from the slot, the root, and the job directory, then runs the
// walled path and asserts the fake harness's pwd is exactly the job directory.
func TestNativeChildCwdIsJobDirFromForeignCwd(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	label := "foreign-cwd-label"

	foreign := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(foreign); err != nil {
		t.Fatalf("chdir to a foreign directory: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the walled run exits 0, got %d:\n%s", code, errOut.String())
	}
	jobDir := filepath.Join(slot, "jobs", label)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("the child did not write pwd into RESULT.md under the job directory: %v", err)
	}
	got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
	if !sameDir(got, jobDir) {
		t.Errorf("from cwd %s the child's cwd is %q, want the job directory %q", foreign, got, jobDir)
	}
}

// TestNativeOKNamesTheWall: NATIVE OK names the wall it ran inside, copied from the wall's
// own SANDBOX OK line, and says none when no wall was named -- so a run without a wall is
// visible in the one line a caller reads.
func TestNativeOKNamesTheWall(t *testing.T) {
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)

	t.Run("no_wall", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		if err := os.WriteFile(cardPath, []byte("a card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "10s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("exit 0, got %d:\n%s", rc, stderr.String())
		}
		if !strings.Contains(stdout.String(), "NATIVE OK ") || !strings.Contains(stdout.String(), " sandbox=none-by-flag ") {
			t.Fatalf("NATIVE OK names the wall none-by-flag when --no-wall runs:\n%s", stdout.String())
		}
	})

	t.Run("walled", func(t *testing.T) {
		t.Setenv("NOVA_FAKE_SANDBOX", "pass")
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		if err := os.WriteFile(cardPath, []byte("a card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "10s", "--sandbox", sandbox}, strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("exit 0, got %d:\n%s", rc, stderr.String())
		}
		if !strings.Contains(stdout.String(), " sandbox=fake-wall ") {
			t.Fatalf("NATIVE OK copies the wall's own name (fake-wall):\n%s", stdout.String())
		}
	})
}

// THE WALL RULES OF THE NATIVE RUN (slice 11, lesson 11). A frozen run configuration gains
// two lists: repos (repositories a card may clone) and recipients (bus lanes a card may
// address, default none). The native run passes them to the sandbox layer as allow rules:
// a repo is network to github.com only, and it is a HOST rule -- a wall that cannot express
// it refuses rather than running unwalled -- while the recipients are never expressed, and
// a bus send is denied by the wall by construction (no nova-bus on PATH, no bus checkout in
// the write set).

// TestNativeRunPassesRepoAllowRule: when the wall can express a hash host rule, the native
// run's argv carries each repo the card named as a --repo allow rule, and the child still
// runs to completion.
func TestNativeRunPassesRepoAllowRule(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "hosts")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "a-label",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox, repos: []string{"mas-bandwidth/nova-tools"},
	}, &errOut)
	if code != 0 {
		t.Fatalf("a walled run with a repo rule exits 0, got %d:\n%s", code, errOut.String())
	}
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", "a-label"))
	if !strings.Contains(argv, "--repo mas-bandwidth/nova-tools") {
		t.Errorf("the wall argv does not carry the repo allow rule:\n%s", argv)
	}
}

// TestNativeRunDeniesBusInsideWall: recipients are never turned into an allow rule. The
// native run built the wall, and the wall's argv grants no bus -- no nova-bus command, no
// --recipient flag, and no bus checkout in the write set -- so a bus send from inside the
// wall is denied by construction.
func TestNativeRunDeniesBusInsideWall(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "a-label",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox, recipients: []string{"adrienne", "rowan"},
	}, &errOut)
	if code != 0 {
		t.Fatalf("a walled run exits 0, got %d:\n%s", code, errOut.String())
	}
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", "a-label"))
	for _, denied := range []string{"nova-bus", "--recipient"} {
		if strings.Contains(argv, denied) {
			t.Errorf("the wall argv grants a bus lane the wall denies (%q):\n%s", denied, argv)
		}
	}
}

// TestNativeRefusesWhenWallCannotExpressRule: a card that names repos but no wall, or a
// wall that cannot express a HOST rule, is a refusal -- never an unwalled run. The one line
// names the label and the reason.
func TestNativeRefusesWhenWallCannotExpressRule(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	// No wall at all (--no-wall): the card named repos there is no wall to allow.
	t.Run("no_wall", func(t *testing.T) {
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("a card\n"), slotDir: slot, root: root, deadline: time.Second,
			repos: []string{"mas-bandwidth/nova-tools"}, noWall: true,
		}, &errOut)
		if code != 2 {
			t.Fatalf("the refusal exits 2, got %d:\n%s", code, errOut.String())
		}
		assertRepoRefusal(t, errOut.String())
	})

	// A wall that cannot express a host rule (hosts=none).
	t.Run("wall_without_host_rules", func(t *testing.T) {
		t.Setenv("NOVA_FAKE_SANDBOX", "pass")
		sandbox := nativeSandbox(t)
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("a card\n"), slotDir: slot, root: root, deadline: time.Second,
			sandbox: sandbox, repos: []string{"mas-bandwidth/nova-tools"},
		}, &errOut)
		if code != 2 {
			t.Fatalf("the refusal exits 2, got %d:\n%s", code, errOut.String())
		}
		assertRepoRefusal(t, errOut.String())
	})
}

func assertRepoRefusal(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "NATIVE REFUSED") {
		t.Fatalf("the refusal is one REFUSED line, got:\n%s", out)
	}
	if !strings.Contains(out, "lbl wall cannot express repo rule") {
		t.Fatalf("the refusal names the label and the reason, got:\n%s", out)
	}
	if got := strings.Count(strings.TrimSpace(out), "\n") + 1; got != 1 {
		t.Fatalf("exactly one REFUSED line, got %d:\n%s", got, out)
	}
}

// TestNativeRefusesWithoutWallUnlessFlagged: the wall is never implied away (SPEC-SANDBOX
// rule 1). A machine with no wall binary -- none named with --sandbox and none on PATH -- is
// a refusal naming what was looked for, unless the caller typed --no-wall, in which case the
// run goes unwalled and says so by its own name.
func TestNativeRefusesWithoutWallUnlessFlagged(t *testing.T) {
	bin := nativeHarness(t)
	t.Setenv("PATH", t.TempDir()) // no nova-sandbox on PATH anywhere

	t.Run("no_wall_no_flag", func(t *testing.T) {
		root, slot := aSlot(t)
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("a card\n"), slotDir: slot, root: root, deadline: time.Second,
		}, &errOut)
		if code != 2 {
			t.Fatalf("a run with no wall and no --no-wall exits 2, got %d:\n%s", code, errOut.String())
		}
		if !strings.Contains(errOut.String(), "NATIVE REFUSED") {
			t.Fatalf("the refusal is one REFUSED line, got:\n%s", errOut.String())
		}
		if !strings.Contains(errOut.String(), "lbl no wall:") {
			t.Fatalf("the refusal names the label and the missing wall, got:\n%s", errOut.String())
		}
		if !strings.Contains(errOut.String(), "nova-sandbox") {
			t.Fatalf("the refusal names what was looked for, got:\n%s", errOut.String())
		}
		if got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1; got != 1 {
			t.Fatalf("exactly one REFUSED line, got %d:\n%s", got, errOut.String())
		}
	})

	t.Run("no_wall_with_flag", func(t *testing.T) {
		root, slot := aSlot(t)
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("a card\n"), slotDir: slot, root: root, deadline: time.Second,
			noWall: true,
		}, &errOut)
		if code != 0 {
			t.Fatalf("--no-wall owns the run and exits 0, got %d:\n%s", code, errOut.String())
		}
		if res.wall != "none-by-flag" {
			t.Errorf("--no-wall names the run none-by-flag, got %q", res.wall)
		}
	})
}

// TestNativeRunsWalledWithoutHostRulesWhenNoRepos: a wall that cannot express a host rule
// (its check does not say hosts=enforceable) is still a wall. A card naming no repos runs
// inside it without --repo rules -- never unwalled, and no refusal -- while the same wall
// and a named repo is the refusal asserted elsewhere.
func TestNativeRunsWalledWithoutHostRulesWhenNoRepos(t *testing.T) {
	bin := nativeHarness(t)
	nativeSandboxOnPath(t)
	label := "a-label"
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
	}, &errOut)
	if code != 0 {
		t.Fatalf("a wall without host rules still walls a card naming no repos, got %d:\n%s", code, errOut.String())
	}
	if res.wall != "fake-wall" {
		t.Errorf("the run names the wall it resolved on PATH, got %q", res.wall)
	}
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", label))
	if strings.Contains(argv, "--repo") {
		t.Errorf("no repo was named, so no --repo rule is built:\n%s", argv)
	}
}

// TestNativeEnvIsCleanAndInsideTheWall: the walled child is handed a clean environment, not
// the caller's. HOME and XDG_DATA_HOME appear exactly once and point at the data home, TMPDIR
// is the slot's own tmp/<label> (outside the git-inited job directory), and XDG_CONFIG_HOME /
// XDG_CACHE_HOME do not survive to point outside the wall. A planted foreign
// HOME/XDG_CONFIG_HOME/XDG_CACHE_HOME/TMPDIR and a provider key are set first, and the run
// happens from a foreign cwd, proving the child's own environment and directory are the
// run's, not the caller's.
func TestNativeEnvIsCleanAndInsideTheWall(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	label := "clean-env"

	// Plant a foreign environment the run must shed: HOME and the two XDG homes outside the
	// wall, a TMPDIR the wall would deny, and a provider key whose value the log must redact.
	foreign := t.TempDir()
	t.Setenv("HOME", filepath.Join(foreign, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(foreign, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(foreign, "cache"))
	t.Setenv("TMPDIR", filepath.Join(foreign, "tmp"))
	t.Setenv("FAKE_KEY", "planted-secret-value")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(foreign); err != nil {
		t.Fatalf("chdir to a foreign directory: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the walled run exits 0, got %d:\n%s", code, errOut.String())
	}

	// The child still runs in its job directory even from a foreign cwd.
	jobDir := filepath.Join(slot, "jobs", label)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("the child did not write pwd into RESULT.md: %v", err)
	}
	if got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd="); got != jobDir {
		if want, evalErr := filepath.EvalSymlinks(jobDir); evalErr == nil && got != want {
			t.Errorf("from cwd %s the child's cwd is %q, want the job directory %q", foreign, got, want)
		}
	}

	// The environment the run recorded is what the wall was handed.
	rawLog, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
	if err != nil {
		t.Fatalf("the run recorded no native-argv.log: %v", err)
	}
	env := nativeLoggedEnv(t, string(rawLog))
	// The slot admission recorded is symlink-resolved (issue #578), so the expectation is too.
	dataHome := filepath.Join(resolvedPath(t, slot), "data")

	if got := env["HOME"]; len(got) != 1 {
		t.Errorf("the child has %d HOME entries, want 1: %v", len(got), got)
	} else if got[0] != dataHome {
		t.Errorf("HOME is %q, want the data home %q", got[0], dataHome)
	}
	if got := env["XDG_DATA_HOME"]; len(got) != 1 {
		t.Errorf("the child has %d XDG_DATA_HOME entries, want 1: %v", len(got), got)
	} else if got[0] != dataHome {
		t.Errorf("XDG_DATA_HOME is %q, want the data home %q", got[0], dataHome)
	}
	if got := env["XDG_CONFIG_HOME"]; got != nil {
		t.Errorf("XDG_CONFIG_HOME survived and points outside the wall: %v", got)
	}
	if got := env["XDG_CACHE_HOME"]; got != nil {
		t.Errorf("XDG_CACHE_HOME survived and points outside the wall: %v", got)
	}
	tmp := env["TMPDIR"]
	if len(tmp) != 1 {
		t.Fatalf("the child has %d TMPDIR entries, want 1: %v", len(tmp), tmp)
	}
	// The run symlink-resolves the slot it was handed (#586), so the two spellings of one
	// directory are compared as directories, not as strings.
	if want := filepath.Join(slot, "tmp", label); !sameDir(tmp[0], want) {
		t.Errorf("TMPDIR is %q, want the slot's own tmp dir %q", tmp[0], want)
	}
	if got := env["FAKE_KEY"]; len(got) != 1 || got[0] != "<redacted>" {
		t.Errorf("the secret's value is not redacted in the log: %v", got)
	}
}

// nativeLoggedEnv reads the env lines of a native-argv.log into a name -> values map.
func nativeLoggedEnv(t *testing.T, log string) map[string][]string {
	t.Helper()
	m := map[string][]string{}
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, "env: ") {
			continue
		}
		kv := strings.TrimPrefix(line, "env: ")
		name, val, _ := strings.Cut(kv, "=")
		m[name] = append(m[name], val)
	}
	return m
}

// TestNativeChildCwdIsJobDirUnwalled: the child runs in its job directory on BOTH paths --
// walled and unwalled -- even when the caller's own cwd is somewhere else entirely. The
// unwalled half is the one the sixth run proved: with no wall the child must still be in the
// job directory, not the invoker's.
func TestNativeChildCwdIsJobDirUnwalled(t *testing.T) {
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)

	for _, tc := range []struct {
		name    string
		sandbox string
		noWall  bool
	}{
		{"unwalled", "", true},
		{"walled", sandbox, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sandbox != "" {
				t.Setenv("NOVA_FAKE_SANDBOX", "pass")
			}
			root, slot := aSlot(t)
			label := "foreign-cwd-" + tc.name

			foreign := t.TempDir()
			orig, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(foreign); err != nil {
				t.Fatalf("chdir to a foreign directory: %v", err)
			}
			defer func() { _ = os.Chdir(orig) }()

			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: label,
				card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
				sandbox: tc.sandbox, noWall: tc.noWall,
			}, &errOut)
			if code != 0 {
				t.Fatalf("the %s run exits 0, got %d:\n%s", tc.name, code, errOut.String())
			}
			jobDir := filepath.Join(slot, "jobs", label)
			raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
			if err != nil {
				t.Fatalf("the child did not write pwd into RESULT.md under the job directory: %v", err)
			}
			got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
			if !sameDir(got, jobDir) {
				t.Errorf("from cwd %s the %s child's cwd is %q, want the job directory %q", foreign, tc.name, got, jobDir)
			}
		})
	}
}

// TestNativeRunWritesUsageInJobDirectory: the native run writes usage.tsv beside RESULT.md
// in <slot>/jobs/<label>/usage.tsv (and slotDir fallback), so the batch gather reads it.
func TestNativeRunWritesUsageInJobDirectory(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "usage-loc"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("native run exits 0, got %d:\n%s", code, errOut.String())
	}
	jobUsage := filepath.Join(slot, "jobs", label, "usage.tsv")
	if _, err := os.Stat(jobUsage); err != nil {
		t.Fatalf("usage.tsv not found beside RESULT.md in %s: %v", jobUsage, err)
	}
	slotUsage := filepath.Join(slot, "usage.tsv")
	if _, err := os.Stat(slotUsage); err != nil {
		t.Fatalf("usage.tsv not found in slot directory %s: %v", slotUsage, err)
	}
}

// resolvedPath is a path made absolute and symlink-resolved, which is the form admission
// records. A test that builds its expectation from t.TempDir() must resolve it too: on
// darwin the temp directory is handed out under /var, a symlink to /private/var, so the
// unresolved spelling and the recorded one are two names for one directory and a string
// compare between them fails on every macOS bench (issue #578).
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %s: %v", path, err)
	}
	return real
}

// TestNativeRelativeSlotIsAbsolutized: the native run absolutizes --slot and --root at
// admission -- absolute and symlink-resolved -- so the wall's argv reads the slot and writes
// the job by that one path; the wall's refusal of `--read ./root/1` and `--write root/1/...`
// is what this absolutization exists to prevent. The run starts from a foreign working
// directory with the slot and root spelled relatively, and the test asserts the wall argv
// carries the resolved absolute slot.
func TestNativeRelativeSlotIsAbsolutized(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	foreign := t.TempDir()
	root := filepath.Join(foreign, "root")
	slot := filepath.Join(root, "slot-1")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(foreign); err != nil {
		t.Fatalf("chdir to a foreign directory: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()
	relRoot, err := filepath.Rel(foreign, root)
	if err != nil {
		t.Fatal(err)
	}
	relSlot, err := filepath.Rel(foreign, slot)
	if err != nil {
		t.Fatal(err)
	}
	label := "rel-slot"
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: relSlot, root: relRoot, deadline: 30 * time.Second,
		sandbox: sandbox,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", label))
	want, err := filepath.EvalSymlinks(slot)
	if err != nil {
		t.Fatalf("resolving the slot %q: %v", slot, err)
	}
	if !hasFlagPairResolved(strings.Fields(argv), "--read", want) {
		t.Errorf("the wall argv does not read the slot by absolute path %s:\n%s", slot, argv)
	}
}

// TestNativeSilentHarnessIsNotOK: a harness that exits 0 having written NOTHING under the
// job directory -- no `harness.log` and no `RESULT.md` -- did not run the card, and the one
// line a coordinator reads says so: `harness=silent` (issue #591). The fault it closes was a
// local model whose tool calls the harness never parsed: the child emitted its calls as raw
// text, no tool ran, nothing was written, the process exited 0 and `NATIVE OK` said OK. The
// token is ALWAYS present -- `harness=ok` for a harness that wrote -- so a reader never has
// to know which runs carry it, and `native.log` (which that run did fill, with the unparsed
// text) is deliberately not one of the two files: the harness's own records are.
func TestNativeSilentHarnessIsNotOK(t *testing.T) {
	bin := nativeHarness(t)
	for _, tc := range []struct{ name, card, want string }{
		{"silent", "FAKE-NORESULT\n", " harness=silent"},
		{"wrote_a_result", "a card line 1\nline 2\n", " harness=ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			if err := os.WriteFile(cardPath, []byte(tc.card), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			rc := run([]string{"native", "--harness", bin, "--model", "fake/fake-model",
				"--label", "silent-label", "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != 0 {
				t.Fatalf("the run exits 0, got %d:\n%s", rc, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("the NATIVE OK line carries%s:\n%s", tc.want, stdout.String())
			}
		})
	}
}

// TestNativeTmpDirIsOutsideAnyRepo: the native run hands the child a TMPDIR that is the slot's
// own tmp/<label>, not the job directory. Native admission git-inits the job directory into a
// repo, and a card's temp dir inside a repo is exactly what makes nova-wake's
// TestAwakeRefusesNonBus fail for a reason the card did not cause (#460). The test git-inits
// the job directory the way admission does, then runs `git rev-parse --show-toplevel` from
// inside the exported TMPDIR and asserts it does not resolve into the job's repo.
func TestNativeTmpDirIsOutsideAnyRepo(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "tmp-outside-repo"

	// git-init the job directory the way native admission does, before the run starts.
	jobDir := filepath.Join(slot, "jobs", label)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", jobDir).CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v: %s", err, out)
	}

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("native run exits 0, got %d:\n%s", code, errOut.String())
	}

	// The exported TMPDIR is the slot's own tmp/<label>, not under the job directory. The run
	// symlink-resolves the slot it was handed (#586), so the two spellings of one directory
	// are compared as directories, not as strings.
	want := filepath.Join(slot, "tmp", label)
	if !sameDir(res.tmp, want) {
		t.Errorf("TMPDIR is %q, want %q", res.tmp, want)
	}
	if st, err := os.Stat(res.tmp); err != nil || !st.IsDir() {
		t.Fatalf("the exported TMPDIR %q is not a made directory: %v", res.tmp, err)
	}

	// A git rev-parse from inside the exported TMPDIR must not resolve into the job's repo.
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = res.tmp
	out, err := cmd.CombinedOutput()
	toplevel := strings.TrimSpace(string(out))
	if err == nil && sameDir(toplevel, jobDir) {
		t.Errorf("git rev-parse --show-toplevel from TMPDIR %q resolved into the job's repo %q", res.tmp, toplevel)
	}
}
