package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
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
	argv := nativeSandboxArgv(bin, nativeRunConfig{slotDir: slot}, filepath.Join(slot, "data"), jobDir)
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
	want, err := filepath.EvalSymlinks(jobDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("from cwd %s the child's cwd is %q, want the job directory %q", foreign, got, want)
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
// sits under a --write, and XDG_CONFIG_HOME / XDG_CACHE_HOME do not survive to point outside
// the wall. A planted foreign HOME/XDG_CONFIG_HOME/XDG_CACHE_HOME/TMPDIR and a provider key
// are set first, and the run happens from a foreign cwd, proving the child's own environment
// and directory are the run's, not the caller's.
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
	dataHome := filepath.Join(slot, "data")

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
	if rel, err := filepath.Rel(dataHome, tmp[0]); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Errorf("TMPDIR %q is not inside the data home %q (a --write)", tmp[0], dataHome)
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
			want, err := filepath.EvalSymlinks(jobDir)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("from cwd %s the %s child's cwd is %q, want the job directory %q", foreign, tc.name, got, want)
			}
		})
	}
}
