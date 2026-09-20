package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE NATIVE OPENCODE PATH (issue #296, slice 2). A frozen run configuration is executed
// directly against the real fake harness, with no pool, no worker description and no
// supervisor: the binary, the model, the card text, the slot and the deadline are the whole
// truth. These tests run the same fake harness the dispatcher tests run, so the child that
// starts is the one whose argv, sleep and exit the machinery already trusts.

func nativeHarness(t *testing.T) string {
	t.Helper()
	if err := buildShared(); err != nil {
		t.Fatalf("building the binaries these tests run: %v", err)
	}
	return builtHarness
}

// nativeSandbox returns the fake sandbox of the seam tests: a stand-in for nova-sandbox that
// records its argv and, under NOVA_FAKE_SANDBOX=hosts, reports hosts=enforceable so the
// repo allow rule reaches the argv. Its compile is shared by every test that asks.
func nativeSandbox(t *testing.T) string {
	t.Helper()
	if err := buildShared(); err != nil {
		t.Fatalf("building the binaries these tests run: %v", err)
	}
	return builtFakeSandbox
}

// nativeSandboxOnPath puts the fake sandbox on PATH under its own name (`nova-sandbox`), so
// the native run resolves the wall itself rather than being handed a --sandbox path. It
// returns the directory that now names the wall on PATH; the stand-in itself is built once
// and linked there, never compiled per test.
func nativeSandboxOnPath(t *testing.T) string {
	t.Helper()
	if err := buildShared(); err != nil {
		t.Fatalf("building the binaries these tests run: %v", err)
	}
	dir := builtPathBin
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

// TestNativeArgvReadsTheBenchToolchainRoots is the edge the schema dogfood loop found on
// 2026-09-18, and it is the whole bug in one assertion: the provisioning standard puts Go
// and sbcl under `~/sdk` with `~/go/bin` on PATH and the module cache at `~/go/pkg/mod`,
// the wall named none of them, and `nova-swarm native` pins GOTOOLCHAIN=local -- so every
// Go card on hulk got `Permission denied` on the bench's own go and then
// `go.mod requires go >= 1.26 (running go 1.22.2)` from the only one the wall left it.
// The roots are read-only and come from ONE list (swarm.ToolchainRoots).
func TestNativeArgvReadsTheBenchToolchainRoots(t *testing.T) {
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A home of the test's own, with the standard's shape under it, so the assertion is
	// about the argv and not about the machine the test happens to run on.
	// The LINUX list, named rather than taken from the machine, so the assertion is the
	// same on a Mac runner and on a linux one: those are the roots that live under a home.
	// The home RESOLVED, because a root reaches the argv resolved through its symlinks (the
	// wall checks the resolved target) and on a Mac a temp dir is under /var, itself a link
	// to /private/var. Resolving here keeps the assertion about the argv.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range swarm.ToolchainRootNames("linux") {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Paths that are NOT the toolchain, made before the argv so an argv that named the
	// home or globbed it would carry them.
	var others []string
	for _, name := range []string{".config/nova-secrets", ".ssh"} {
		other := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(other, 0o700); err != nil {
			t.Fatal(err)
		}
		others = append(others, other)
	}
	cfg := nativeRunConfig{slotDir: slot, benchHome: home, benchOS: "linux"}
	argv := nativeSandboxArgv(bin, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	// ONE LIST, TWO KINDS. An exec root goes on --read, which carries EXECUTE on both wall
	// bodies; a read-only root goes on --read-noexec, which takes the execute away. The
	// kind is the list's, and each root must be on ITS OWN flag and on no other -- a
	// read-only root that slipped onto --read is exactly the widening Johnny's security
	// read of #1364 refused.
	for _, root := range swarm.ToolchainRootList("linux") {
		if !root.Home() {
			// A system root is that absolute path on the machine the test runs on, not a
			// path under the test's own home: TestNativeArgvGrantsTheDotnetMutexRootAsAWrite
			// is where the one system root's kind is held.
			continue
		}
		path := filepath.Join(home, filepath.FromSlash(root.Name))
		want := root.Flag()
		for _, wrong := range []string{"--read", "--read-noexec", "--write"} {
			if wrong == want {
				continue
			}
			if hasFlagPair(argv, wrong, path) {
				t.Errorf("the toolchain root %s is on %s and its kind is %s:\n%s", path, wrong, want, strings.Join(argv, " "))
			}
		}
		if !hasFlagPair(argv, want, path) {
			t.Errorf("the wall argv does not carry the toolchain root %s as %s:\n%s", path, want, strings.Join(argv, " "))
		}
	}
	// THE MODULE CACHE BY NAME, because it is the root this argv form was added for: READ
	// WITHOUT EXECUTE, never read+execute. Every `go mod download` on the bench lands
	// there and the bench user can write to it, so a card able to execute out of it could
	// run whatever a dependency shipped.
	modCache := filepath.Join(home, filepath.FromSlash("go/pkg/mod"))
	if !hasFlagPair(argv, "--read-noexec", modCache) {
		t.Errorf("the module cache is not granted read-without-execute:\n%s", strings.Join(argv, " "))
	}
	if hasFlagPair(argv, "--read", modCache) {
		t.Errorf("the module cache is on --read, which CARRIES EXECUTE:\n%s", strings.Join(argv, " "))
	}
	// NOTHING ELSE UNDER HOME. The wall gained the toolchain and not the home: the key
	// store and an ssh directory beside it stay outside every named path, on either flag.
	for _, other := range append(others, home) {
		for _, flag := range []string{"--read", "--read-noexec", "--write"} {
			if hasFlagPair(argv, flag, other) {
				t.Errorf("the wall argv names %s on %s, and it is not a toolchain root:\n%s", other, flag, strings.Join(argv, " "))
			}
		}
	}
	// ~/go/bin is granted BY NEITHER KIND (Johnny's security read of #1364): every
	// `go install` on the bench lands there and the bench user can write to it. On a
	// provisioned bench ~/go/bin/go is a symlink into the sdk tree and the kernel checks
	// the resolved target, so a card's PATH still finds the granted toolchain.
	goBin := filepath.Join(home, filepath.FromSlash("go/bin"))
	if err := os.MkdirAll(goBin, 0o755); err != nil {
		t.Fatal(err)
	}
	argv = nativeSandboxArgv(bin, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	for _, flag := range []string{"--read", "--read-noexec", "--write"} {
		if hasFlagPair(argv, flag, goBin) {
			t.Errorf("the wall argv grants ~/go/bin on %s:\n%s", flag, strings.Join(argv, " "))
		}
	}
}

// TestNativeArgvSkipsAToolchainRootThatIsNotThere: rule 5 of the wall REFUSES a --read
// naming a path that does not exist, so a bench without the standard's layout -- a darwin
// bench has no ~/sdk -- loses the root rather than refusing the run.
// TestNativeArgvReadsTheDarwinToolchainRoots is the darwin face of the same edge, measured
// on the M2 Air 2026-09-18: a Mac's toolchains are INSTALLED and on PATH, and three of them
// still died inside the bare wall because each resolves its runtime from the directory of
// the launcher that ran it, and that launcher is a symlink out of any granted tree --
// `go: cannot find GOROOT directory: 'go' binary is trimmed`, `dotnet: Failed to resolve
// full path of the current executable []`, `java: Unable to locate a Java Runtime`. The
// remedy measured by hand was `--read /opt/homebrew/Cellar/go/1.27.1`, and the wall now
// names that tree itself, with the version read off the launcher.
//
// It runs ON a Mac, because what it asserts is that THIS bench's own installed toolchain
// reaches the argv; the per-OS list itself is held by the class test in internal/ci on every
// platform.
func TestNativeArgvReadsTheDarwinToolchainRoots(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the darwin toolchain roots are this bench's own installs; asserted on a Mac")
	}
	var system []swarm.ToolchainRoot
	for _, r := range swarm.ToolchainRoots("darwin", os.Getenv("HOME")) {
		if !r.Home() {
			system = append(system, r)
		}
	}
	if len(system) == 0 {
		t.Skip("this Mac has none of the darwin system toolchains installed")
	}
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := nativeRunConfig{slotDir: slot, benchHome: t.TempDir(), benchOS: "darwin"}
	argv := nativeSandboxArgv(bin, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	for _, r := range system {
		if !filepath.IsAbs(r.Path) || strings.HasSuffix(r.Path, string(filepath.Separator)+"bin") {
			t.Errorf("the darwin root %s resolved to %s, which is not a toolchain tree", r.Name, r.Path)
		}
		// THE ONE WRITABLE SYSTEM ROOT is the .NET named-mutex directory and is held by
		// TestNativeArgvGrantsTheDotnetMutexRootAsAWrite; here it is only asserted to be on
		// its own kind and not quietly on a read one.
		if r.Write {
			if !hasFlagPair(argv, "--write", r.Path) {
				t.Errorf("the darwin writable toolchain root %s (%s) is not on --write:\n%s", r.Name, r.Path, strings.Join(argv, " "))
			}
			for _, wrong := range []string{"--read", "--read-noexec"} {
				if hasFlagPair(argv, wrong, r.Path) {
					t.Errorf("the writable darwin toolchain root %s is also on %s:\n%s", r.Path, wrong, strings.Join(argv, " "))
				}
			}
			continue
		}
		// Every other darwin system root is a RUNTIME the card runs, so every one of them is
		// the exec-carrying kind -- and each reaches the argv RESOLVED, because the grant is
		// checked against the resolved target and `/opt/homebrew/opt/openjdk` is itself a
		// symlink into the Cellar.
		if !r.Exec {
			t.Errorf("the darwin system root %s is granted without execute; it is a runtime the card runs", r.Name)
		}
		if !hasFlagPair(argv, "--read", r.Path) {
			t.Errorf("the wall argv does not carry the darwin toolchain root %s (%s) as --read:\n%s", r.Name, r.Path, strings.Join(argv, " "))
		}
		if hasFlagPair(argv, "--write", r.Path) {
			t.Errorf("the darwin toolchain root %s is a WRITE; it is read-only:\n%s", r.Path, strings.Join(argv, " "))
		}
	}
	// AND NEVER A DIRECTORY OF LAUNCHERS. `/opt/homebrew/bin` holds a symlink for every
	// formula on the machine and brew writes it; the grant is on the Cellar tree the runtime
	// lives in, and naming the bin directory as a toolchain root is the widening Johnny's
	// security read of #1364 refused on ~/go/bin.
	for _, r := range swarm.ToolchainRootList("darwin") {
		if strings.HasSuffix(r.Name, "/bin") {
			t.Errorf("the darwin list names the launcher directory %s as a toolchain root", r.Name)
		}
	}
}

func TestNativeArgvSkipsAToolchainRootThatIsNotThere(t *testing.T) {
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir() // empty: not one root exists under it
	argv := nativeSandboxArgv(bin, nativeRunConfig{slotDir: slot, benchHome: home, benchOS: "linux"}, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	for i, a := range argv {
		if a != "--read" && a != "--read-noexec" {
			continue
		}
		if i+1 < len(argv) && strings.HasPrefix(argv[i+1], home) {
			t.Errorf("the wall argv names %s under a home with no toolchain:\n%s", argv[i+1], strings.Join(argv, " "))
		}
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
	if elapsed > 30*time.Second {
		t.Fatalf("the deadline should cut the run short, but it took %v", elapsed)
	}
}

// TestNativeRunAuthCopyIs0600: the named provider's entry is copied from the auth file into
// the data home, mode 0600, and no other provider's entry travels with it.
func TestNativeRunAuthCopyIs0600(t *testing.T) {
	windowsIsNotABench(t)
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

// TestNativeCarriesProviderConfig: a `--config` opencode.json is carried beside the auth
// file into the job's own data home, mode 0600, and the fake harness sees it at the path it
// resolves from its own XDG data home. The provider's own bytes reach the child unchanged.
//
// ISSUE #644 CHANGED THE OTHER HALF OF THIS: the file is written WHETHER OR NOT --config
// named one, because the job's fence rules live in it, and the sha8 on the NATIVE OK line is
// the sha8 of the bytes the child saw -- the only config a later reader can check the run
// against. Before it, a run with no --config wrote no config and ran on the harness's default
// fence, which auto-rejected the card's own `../scratch`.
func TestNativeCarriesProviderConfig(t *testing.T) {
	windowsIsNotABench(t)
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
		// The config exists even with no --config: it is where the job's fence rules are.
		assertConfigRecord(t, slot, "0600", `"external_directory"`)
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
		assertConfigRecord(t, slot, "0600", `"baseURL": "http://localhost:11434/v1"`)
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
		wantSum := sha256.Sum256(body)
		wantSHA := hex.EncodeToString(wantSum[:])[:8]
		if res.configSHA != wantSHA {
			t.Errorf("the run records the sha8 of the bytes the child saw: %q, want %q", res.configSHA, wantSHA)
		}
		if !strings.Contains(string(body), `"baseURL": "http://localhost:11434/v1"`) {
			t.Errorf("the carried provider reaches the child:\n%s", body)
		}
		if !strings.Contains(string(body), `"external_directory"`) {
			t.Errorf("the job's fence rules are in the config the child reads:\n%s", body)
		}
	})
}

// assertConfigRecord reads what the fake harness recorded about the config at its own XDG
// data home path: the mode it found, and one thing that must be in the bytes it read.
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
	if wantBody != "" && !strings.Contains(rec, wantBody) {
		t.Errorf("the harness saw no %s in the config it read:\n%s", wantBody, rec)
	}
}

// TestNativeRefusesConfigProviderWithoutKey: a --config whose entry for THE MODEL'S OWN
// provider has no key in --auth is refused before anything runs, in one line, naming the
// provider and never the key. That provider is the one the harness is about to call, so its
// missing key is a run that dies rc=1 in under a second.
func TestNativeRefusesConfigProviderWithoutKey(t *testing.T) {
	windowsIsNotABench(t)
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
	windowsIsNotABench(t)
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
// The config is still carried, mode 0600, and the run records the sha8 of what the child saw.
func TestNativeConfigKeylessProviderAdmitted(t *testing.T) {
	windowsIsNotABench(t)
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
	written, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(written)
	wantSHA := hex.EncodeToString(wantSum[:])[:8]
	if res.configSHA != wantSHA {
		t.Errorf("the run records config sha8 %q, want %q", res.configSHA, wantSHA)
	}
	assertConfigRecord(t, slot, "0600", `"baseURL": "http://localhost:11434/v1"`)
}

// TestNativeOKNamesTheCarriedConfig: the NATIVE OK line itself names the config the CHILD
// sees -- config=<sha8> -- which is the one token of issue #465's fix no other test pins on
// the printed line: the carry test pins the struct's sha8 and the copied bytes, and the
// OK-line tests pin sandbox= and harness=, but the token a caller reads to know a configured
// provider was carried before the child ever ran is asserted by nothing.
//
// WHAT THE SHA8 IS, AND WHY IT IS NOT THE NAMED FILE'S OWN BYTES. writeJobConfig hashes the
// bytes it WRITES to <dataHome>/.config/opencode/opencode.json, AFTER this job's own fence
// block is merged into them (issue #644, #704) -- "the sha8 OF THE BYTES THE CHILD SEES,
// which is the only config any later reader can check the run against". So the sha8 is of
// the merged body and never of the caller's file, and there is no config=- case at all: the
// fence block is written WHETHER OR NOT --config named a file, so a run without --config
// still carries a config and still names its sha8. This test originally pinned the caller's
// own bytes and a dash; both were the pre-#704 contract, and the two assertions below are
// the contract the code now promises.
func TestNativeOKNamesTheCarriedConfig(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	const config = `{"provider":{"fake":{"options":{"baseURL":"http://localhost:11434/v1"}}}}` + "\n"

	// carriedSHA is the sha8 of the bytes that landed where the harness reads them, read back
	// off the disk rather than recomputed from the inputs, so the assertion cannot agree with
	// the code by repeating its arithmetic.
	carriedSHA := func(t *testing.T, slot string) string {
		t.Helper()
		written, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
		if err != nil {
			t.Fatalf("the run carries a config where the harness reads it: %v", err)
		}
		sum := sha256.Sum256(written)
		return hex.EncodeToString(sum[:])[:8]
	}

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
		cardPath := filepath.Join(root, "card.md")
		if err := os.WriteFile(cardPath, []byte("a card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--auth", auth, "--config", cfgPath, "--deadline", "30s", "--no-wall"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("the --config run exits 0, got %d:\n%s", rc, stderr.String())
		}
		// The named provider is in the carried bytes -- config= names a config that really
		// carried --config's provider, not merely some config.
		written, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(written), `"baseURL"`) || !strings.Contains(string(written), "fake") {
			t.Errorf("the carried config keeps --config's provider:\n%s", written)
		}
		wantSHA := carriedSHA(t, slot)
		if !strings.Contains(stdout.String(), " config="+wantSHA+" ") {
			t.Fatalf("NATIVE OK names the sha8 %s of the config the child sees:\n%s", wantSHA, stdout.String())
		}
	})

	t.Run("without_config", func(t *testing.T) {
		root, slot := aSlot(t)
		auth := filepath.Join(t.TempDir(), "auth.json")
		if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		cardPath := filepath.Join(root, "card.md")
		if err := os.WriteFile(cardPath, []byte("a card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--auth", auth, "--deadline", "30s", "--no-wall"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("the run without --config exits 0, got %d:\n%s", rc, stderr.String())
		}
		// No --config, but the fence block is still written, so the line still names a sha8
		// and NEVER a dash: a reader can check the fence the child ran under.
		wantSHA := carriedSHA(t, slot)
		if !strings.Contains(stdout.String(), " config="+wantSHA+" ") {
			t.Fatalf("NATIVE OK names the sha8 %s of the fence config carried without --config:\n%s", wantSHA, stdout.String())
		}
		if strings.Contains(stdout.String(), " config=- ") {
			t.Fatalf("config= is never a dash: the fence block is carried whether or not --config named a file:\n%s", stdout.String())
		}
	})
}

// TestFriendSequenceLocalModelCard runs one known-answer card on a fake local provider: the
// harness is the fake, the provider is a keyless ollama (baseURL, no apiKey, no auth entry),
// and the card FAKE-PWD answers with the job directory. The run is walled, admitted without a
// refusal, and the wall's own name and the card's known answer both land where a reader looks.
func TestFriendSequenceLocalModelCard(t *testing.T) {
	windowsIsNotABench(t)
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
			if tc.name == "auth_not_0600" && runtime.GOOS == "windows" {
				t.Skip("windows cannot make a file 0600 in the POSIX sense: NTFS reports 0666 for every readable file, so writing the auth file 0644 cannot produce the looser-than-0600 condition the refusal names")
			}
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
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
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

// TestWallNamedDecodesTheProducersEscapedCwd is the unit half of issue #572: the wall writes
// `cwd=` through oneline.Field (cmd/nova-sandbox/main.go), so the job directory reaches this
// side with its spaces escaped (`stella 2` -> `stella\x202`). wallNamed must decode that
// field back to the path the producer held before it names the directory the child ran in.
// The receipt is built by the producer's own encoder, not hard-coded unescaped.
func TestWallNamedDecodesTheProducersEscapedCwd(t *testing.T) {
	dir := "/Users/glenn/Documents/ChatGPT/stella 2/.scratch/stella-tools/runs/1/jobs/terminology"
	line := "SANDBOX OK backend=sandbox-exec abi=- read=3 write=2 net=nopromise cwd=" +
		oneline.Field(dir) + " ancestors=17 cmd=opencode\n"
	backend, cwd, reason := wallNamed(line)
	if reason != "" {
		t.Fatalf("wallNamed did not read the SANDBOX OK line (%s): %q", reason, line)
	}
	if backend != "sandbox-exec" {
		t.Errorf("wallNamed's backend is %q, want sandbox-exec", backend)
	}
	if cwd != dir {
		t.Errorf("wallNamed's cwd is %q, want the decoded path %q", cwd, dir)
	}
}

// TestNativeWalledJobPathWithSpacesCompletes is the regression for issue #572: a job whose
// path holds a space -- the configured root sits under `stella 2` -- is not a pre-launch
// refusal. The wall is the fake sandbox, which encodes its `cwd=` through oneline.Field
// exactly as the real producer does, so the escape round-trip is exercised rather than a
// hard-coded unescaped receipt. A job that completed must be validated against the decoded
// path and reach the usage recorder, never wear the face of a launch that never happened.
func TestNativeWalledJobPathWithSpacesCompletes(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)

	root := filepath.Join(t.TempDir(), "stella 2")
	slot := filepath.Join(root, "slot-1")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	label := "space-cwd"

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox,
	}, &errOut)
	if code != 0 {
		t.Fatalf("a completed job in a path with a space exits 0, got %d:\n%s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "NATIVE REFUSED") {
		t.Fatalf("a completed job is not a pre-launch refusal:\n%s", errOut.String())
	}
	if res.wall != "fake-wall" {
		t.Errorf("the run keeps the wall's own name, got %q", res.wall)
	}
	jobDir := filepath.Join(slot, "jobs", label)
	// The child ran in the job directory, proven by the card's own known answer.
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("the card's known answer was not written: %v", err)
	}
	got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
	if !sameDir(got, jobDir) {
		t.Errorf("the child's cwd is %q, want the job directory %q", got, jobDir)
	}
	// A completed job retains its usage receipt.
	if _, err := os.Stat(filepath.Join(jobDir, "usage.tsv")); err != nil {
		t.Errorf("the completed job wrote no usage.tsv under %s: %v", jobDir, err)
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
		rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
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
		rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
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

// TestNativeSharedGoCaches: the Go module and build caches are bench-shared under
// <root>/cache, not one copy per card under the data home (card 8963). The child records
// GOMODCACHE, GOCACHE and GOTOOLCHAIN and stats the two directories it was handed, and the
// wall's argv carries the shared cache in its write set. The directories must exist with
// mode 0755 BEFORE the child runs, which the child's own stat is what proves.
func TestNativeSharedGoCaches(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	label := "shared-caches"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-RECORD-CACHES\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, sandbox: sandbox,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	jobDir := filepath.Join(slot, "jobs", label)
	record, err := os.ReadFile(filepath.Join(jobDir, "cache-record"))
	if err != nil {
		t.Fatalf("the harness recorded no cache-record: %v", err)
	}
	got := string(record)
	cacheDir := filepath.Join(resolvedPath(t, root), "cache")
	wantMod := filepath.Join(cacheDir, "go-mod")
	wantBuild := filepath.Join(cacheDir, "go-build")

	if !strings.Contains(got, "GOMODCACHE="+wantMod+"\n") {
		t.Errorf("GOMODCACHE is not the shared module cache %s:\n%s", wantMod, got)
	}
	if !strings.Contains(got, "GOCACHE="+wantBuild+"\n") {
		t.Errorf("GOCACHE is not the shared build cache %s:\n%s", wantBuild, got)
	}
	if !strings.Contains(got, "GOTOOLCHAIN=local\n") {
		t.Errorf("GOTOOLCHAIN is not local:\n%s", got)
	}
	// Each directory existed before the child ran: the record is written by the child, so
	// its own stat is the proof the parent made them first. Windows has no POSIX mode bits,
	// so there the record proves existence and this test proves a file can be created;
	// elsewhere the mode 0755 is the assertion.
	for _, dir := range []struct{ name, path string }{
		{"GOMODCACHE", wantMod},
		{"GOCACHE", wantBuild},
	} {
		line := ""
		for _, l := range strings.Split(got, "\n") {
			if strings.HasPrefix(l, "stat "+dir.name+": ") {
				line = l
			}
		}
		if runtime.GOOS == "windows" {
			if !strings.Contains(line, "dir=true") {
				t.Errorf("%s was not an existing directory before the child ran:\n%s", dir.name, got)
				continue
			}
			probe := filepath.Join(dir.path, "writable-probe")
			if err := os.WriteFile(probe, []byte("probe\n"), 0o644); err != nil {
				t.Errorf("%s is not writable for the child's caches: %v", dir.path, err)
				continue
			}
			_ = os.Remove(probe)
			continue
		}
		if !strings.Contains(line, "mode=0755 dir=true") {
			t.Errorf("%s was not an existing 0755 directory before the child ran:\n%s", dir.name, got)
		}
	}
	// The shared cache is in the wall's write set, so every card of the bench may extract a
	// module inside the wall.
	argv := strings.Fields(sandboxArgv(t, jobDir))
	if !hasFlagPair(argv, "--write", cacheDir) {
		t.Errorf("the wall argv does not write the shared cache %s:\n%s", cacheDir, strings.Join(argv, " "))
	}
}

// TestNativeNoSharedCachesRestoresHomeCaches: --no-shared-caches restores today's behaviour
// exactly -- GOMODCACHE, GOCACHE and GOTOOLCHAIN are not set (Go derives the caches from
// HOME as before), <root>/cache is not made, and the wall's write set does not name it.
func TestNativeNoSharedCachesRestoresHomeCaches(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	label := "no-shared-caches"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-RECORD-CACHES\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, sandbox: sandbox, noSharedCaches: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	jobDir := filepath.Join(slot, "jobs", label)
	record, err := os.ReadFile(filepath.Join(jobDir, "cache-record"))
	if err != nil {
		t.Fatalf("the harness recorded no cache-record: %v", err)
	}
	got := string(record)
	for _, name := range []string{"GOMODCACHE", "GOCACHE", "GOTOOLCHAIN"} {
		if !strings.Contains(got, name+"=\n") {
			t.Errorf("--no-shared-caches set %s; the caches must stay under HOME:\n%s", name, got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "cache")); !os.IsNotExist(err) {
		t.Errorf("--no-shared-caches made <root>/cache, want none: err=%v", err)
	}
	argv := strings.Fields(sandboxArgv(t, jobDir))
	if hasFlagPair(argv, "--write", filepath.Join(resolvedPath(t, root), "cache")) {
		t.Errorf("--no-shared-caches wrote <root>/cache into the wall argv:\n%s", strings.Join(argv, " "))
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

// TestNativeNoWallWritesHarnessLog: the UNWALLED run captures the harness's output to
// <job>/harness-output.log, exactly as the walled run does. Before this, `native --no-wall`
// pinned the child's stdout and stderr to <slot>/native.log alone and wrote nothing under
// the job, so every unwalled Space card that produced no RESULT left NO evidence of what the
// harness said -- the whole no-result class of 2026-09-16 was undiagnosable -- and
// `harness=silent` (#604) could not tell a silent harness from a lost log (issue #608).
//
// The capture is NOT `harness.log`: that file is the harness's own, and `harness=silent`
// reads it to ask whether the harness itself wrote anything. The test asserts both -- the
// capture holds the lines, and `harness.log` is left alone by this process.
//
// The fake harness says one line on each stream; both modes hold both lines in the file.
func TestNativeNoWallWritesHarnessLog(t *testing.T) {
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
			label := "harness-log-" + tc.name
			touched := filepath.Join(t.TempDir(), "touched")
			card := []byte("FAKE-SAY the-harness-said-this\nFAKE-TOUCH " + touched + "\n")

			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: label,
				card: card, slotDir: slot, root: root, deadline: 30 * time.Second,
				sandbox: tc.sandbox, noWall: tc.noWall,
			}, &errOut)
			if code != 0 {
				t.Fatalf("the %s run exits 0, got %d:\n%s", tc.name, code, errOut.String())
			}

			logPath := filepath.Join(slot, "jobs", label, "harness-output.log")
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("the %s run wrote no harness output log at %s: %v", tc.name, logPath, err)
			}
			// The harness's own file is not this process's to write: a capture landing there
			// would answer `harness=silent` for a harness that said nothing at all (#604).
			if _, err := os.Stat(filepath.Join(slot, "jobs", label, "harness.log")); err == nil {
				t.Errorf("the %s run wrote the harness's own harness.log; the capture belongs in harness-output.log", tc.name)
			}
			for _, want := range []string{"the-harness-said-this", "touch " + touched} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("the %s harness output log %s does not carry %q:\n%s", tc.name, logPath, want, raw)
				}
			}
			// One capture path: whatever the harness log holds, the run log holds too, so a
			// reader of either sees the same run.
			runLog, err := os.ReadFile(filepath.Join(slot, "native.log"))
			if err != nil {
				t.Fatalf("the %s run wrote no native.log: %v", tc.name, err)
			}
			for _, want := range []string{"the-harness-said-this", "touch " + touched} {
				if !strings.Contains(string(runLog), want) {
					t.Errorf("the %s native.log does not carry %q:\n%s", tc.name, want, runLog)
				}
			}
		})
	}
}

// TestNativeSilentHarnessIsNotOK pins THE ONE DEFINITION of the token (issues #591, #594,
// #608 folded): `harness=silent` exactly when the run's own capture
// (<job>/harness-output.log, the file the test above proves is written walled or not) holds
// nothing the CHILD said AND no RESULT.md is found anywhere the gather looks; `harness=ok`
// otherwise, and the token is always present.
//
// The four cases are the four ways that can go, and each is a fault someone had:
//   - silent: the run of issue #591 -- a local model whose tool calls the harness never
//     parsed, so no tool ran, nothing was written, the child exited 0 and the line said OK.
//   - spoke_no_result: a harness that SAID something and published nothing is `ok`, because
//     there is evidence to read; the batch scores that card `no-result`, which is the model's
//     own doing and a different remedy.
//   - wall_lines_only: the wall's own `SANDBOX ` lines are in the capture too, and counting
//     them would make a WALLED run -- the shape of #591 itself -- impossible to call silent.
//   - result_under_repo: the result is looked for where the gather looks (#594), and the
//     capture here is EMPTY on purpose, so this case fails the moment that lookup narrows
//     back to the job root: it is the only path where the lookup alone decides.
func TestNativeSilentHarnessIsNotOK(t *testing.T) {
	bin := nativeHarness(t)
	const label = "silent-label"
	// The fake says this on its own stderr for FAKE-SAY: "fake harness: " + the word + "\n",
	// which is 37 bytes -- the harness speaking, and nothing published.
	const saidBytes = 37
	for _, tc := range []struct {
		name, card, want string
		// resultInRepo plants a RESULT.md under <job>/repo before the run, the way a card
		// whose STEP 1 cloned into repo/ publishes, while the run itself says nothing.
		resultInRepo bool
		// walled runs the case inside the fake wall instead of --no-wall, so the capture
		// holds the wall's own SANDBOX lines and nothing else.
		walled bool
		// wantCapture is how many bytes of the child's own words the capture must hold.
		wantCapture int
	}{
		{name: "silent", card: "FAKE-NORESULT\n", want: " harness=silent"},
		{name: "wrote_a_result", card: "a card line 1\nline 2\n", want: " harness=ok"},
		{name: "spoke_no_result", card: "FAKE-SAY twenty-two-characters!\nFAKE-NORESULT\n", want: " harness=ok", wantCapture: saidBytes},
		{name: "wall_lines_only", card: "FAKE-NORESULT\n", want: " harness=silent", walled: true},
		{name: "result_under_repo", card: "FAKE-NORESULT\n", want: " harness=ok", resultInRepo: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.walled {
				t.Setenv("NOVA_FAKE_SANDBOX", "pass")
			}
			root, slot := aSlot(t)
			jobDir := filepath.Join(slot, "jobs", label)
			if tc.resultInRepo {
				repo := filepath.Join(jobDir, "repo")
				if err := os.MkdirAll(repo, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo, "RESULT.md"), []byte("a card line 1\nall green from the clone\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cardPath := filepath.Join(root, "card.md")
			if err := os.WriteFile(cardPath, []byte(tc.card), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
				"--label", label, "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", "30s"}
			if tc.walled {
				args = append(args, "--sandbox", nativeSandbox(t))
			} else {
				args = append(args, "--no-wall")
			}
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != 0 {
				t.Fatalf("the run exits 0, got %d:\n%s", rc, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("the NATIVE OK line carries%s:\n%s", tc.want, stdout.String())
			}
			// The capture is the file the token is asked of, so the case that says the
			// harness spoke proves the bytes are in it.
			if tc.wantCapture > 0 {
				raw, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
				if err != nil {
					t.Fatalf("the capture the token reads is missing: %v", err)
				}
				if len(raw) != tc.wantCapture {
					t.Errorf("the capture holds %d bytes of the child's words, want %d:\n%s", len(raw), tc.wantCapture, raw)
				}
			}
			// A silent run's capture holds no word of the child's: either nothing at all, or
			// the wall's own header lines.
			if tc.want == " harness=silent" {
				raw, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
				if err != nil {
					t.Fatalf("the capture is written even for a silent run: %v", err)
				}
				for _, line := range strings.Split(string(raw), "\n") {
					if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "SANDBOX ") {
						t.Errorf("a silent run's capture carries a line the child wrote: %q", line)
					}
				}
			}
		})
	}
}

// TestNativeCaptureRefusesSymlink: the capture is the first file this process opens inside
// the JOB, which is the card's own writable directory. A symlink planted there -- by an
// earlier run of the same card, or by the card itself -- would carry the child's output to
// wherever it points, written by a process that has no wall around it (security#30's class).
// The open carries O_NOFOLLOW, so the run refuses by name and the target is untouched.
func TestNativeCaptureRefusesSymlink(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "planted-capture"
	jobDir := filepath.Join(slot, "jobs", label)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(victim, []byte("the bytes outside the wall\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(jobDir, "harness-output.log")); err != nil {
		t.Skipf("this platform will not plant a symlink: %v", err)
	}

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-SAY planted\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 2 {
		t.Fatalf("a planted symlink at the capture path is a refusal (exit 2), got %d:\n%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "NATIVE REFUSED") {
		t.Errorf("the refusal does not name itself:\n%s", errOut.String())
	}
	raw, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "the bytes outside the wall\n" {
		t.Errorf("the run wrote through the planted symlink; the file outside now holds:\n%s", raw)
	}
}

// ISSUE #881 (a): a key is authorized for one model only, and the worker description pins
// that one. `nova-swarm native --worker <file>` makes the description the source of the
// model; a --model whose model half differs is refused, naming BOTH models on one line, at
// exit 2, before any directory is made and before any child starts. Without --worker,
// native keeps --model as today.
func TestNativeRefusesAModelThatDiffersFromTheWorkerDescription(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("a card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	desc := nativeWorkerDescription(t, "fake-model", "key_file")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/other-model",
		"--worker", desc, "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 2 {
		t.Fatalf("a --model that differs from the description's is refused exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	// THE ONE LINE NAMES BOTH MODELS: the description's fake-model and the --model typed.
	line := strings.TrimSpace(stderr.String())
	mustContain(t, "the refusal", line, "fake/other-model")
	mustContain(t, "the refusal", line, "fake-model")
	if n := strings.Count(line, "\n") + 1; n != 1 {
		t.Fatalf("the refusal names both models on ONE line, got %d:\n%s", n, stderr.String())
	}
	if strings.Contains(stdout.String(), "NATIVE OK") {
		t.Errorf("the refusal comes before any child runs:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(slot, "jobs")); err == nil {
		t.Errorf("the refusal comes before any job directory is made:\n%s", stderr.String())
	}
}

// ISSUE #881 (b): a description naming "secret": "<NAME>" takes the key from the
// ENVIRONMENT -- `nova-secrets exec` set it around the run -- so native passes NAME through
// to the harness's environment and writes NO auth file under the job. --auth remains only
// the legacy shape's. The fake harness proves the key is present by length, never by value.
func TestNativeSecretWorkerWritesNoAuthFileAndTheHarnessSeesName(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("a card\nFAKE-FINDINGS 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	desc := nativeWorkerDescription(t, "fake-model", "secret")
	t.Setenv("FAKE_KEY", fakeKey)

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--worker", desc, "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("a secret worker runs, exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	jobDir := filepath.Join(slot, "jobs", "card")
	// THE HARNESS SAW THE NAME: the key reached it by environment, proven by length.
	capture, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
	if err != nil {
		t.Fatalf("the run captured no harness output under the job: %v", err)
	}
	mustContain(t, "the harness capture", string(capture), "the key is present, length")
	// NO AUTH FILE IS WRITTEN UNDER THE JOB: neither the carried copy nor the harness's own.
	for _, p := range []string{
		filepath.Join(slot, "data", "auth.json"),
		filepath.Join(slot, "data", "opencode", "auth.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a secret worker writes no auth file, but %s exists", p)
		}
	}
	// The value is in no file under the slot, and in no line this tool printed.
	if found := grepTree(t, slot, fakeKey); found != "" {
		t.Errorf("the secret is at rest in a file under the slot: %s", found)
	}
	if strings.Contains(stdout.String()+stderr.String(), fakeKey) {
		t.Error("the secret reached an event line")
	}
}

// ISSUE #881 (b), the legacy half: `--auth` with a `--worker` description whose key is a
// key_file still copies the provider secret to the data home -- and says so in ONE NOTE
// line, because a description that named "secret": "<NAME>" would keep the key in the
// environment instead.
func TestNativeAuthWithAWorkerNamesItsLegacyCopy(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("a card\nFAKE-FINDINGS 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	desc := nativeWorkerDescription(t, "fake-model", "key_file")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--worker", desc, "--auth", auth, "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("the legacy shape runs, exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	mustContain(t, "the legacy note", stderr.String(), "NATIVE NOTE: --auth")
	if _, err := os.Stat(filepath.Join(slot, "data", "auth.json")); err != nil {
		t.Errorf("the legacy shape still copies the auth file to the data home: %v", err)
	}
}

// ISSUE #915 (windows leg): the legacy --auth native path refuses an auth source looser than
// 0600 and a copy that does not end 0600 (copyAuth). NTFS carries no unix permission bits --
// os.Stat reports 0666 for every readable file there (0444 when it is read-only) -- so on
// windows-latest a source the test wrote 0600 read 0666 and the check refused it: the legacy
// shape, TestNativeAuthWithAWorkerNamesItsLegacyCopy, died exit 2 on the line that says it
// runs. This is the same class as the execute bit (executable.go) and the key file mode
// (internal/swarm/key.go, which already answers nothing on windows). The rules now ask the
// platform, and because they are written where every platform compiles them, darwin and linux
// hold the windows answer to this contract.
func TestAuthModeRulesAskThePlatform(t *testing.T) {
	// The source question: is any group or other bit set? The answer is "no" on windows,
	// however the file reads, because the bits do not exist there.
	for _, tc := range []struct {
		name string
		goos string
		mode os.FileMode
		want bool
	}{
		{"windows_0600", "windows", 0o600, false},
		{"windows_0644", "windows", 0o644, false},
		{"windows_0666", "windows", 0o666, false},
		{"linux_0600", "linux", 0o600, false},
		{"linux_0400", "linux", 0o400, false},
		{"linux_0644", "linux", 0o644, true},
		{"linux_0666", "linux", 0o666, true},
		{"darwin_0600", "darwin", 0o600, false},
		{"darwin_0604", "darwin", 0o604, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := authModeWiderThanOwner(tc.goos, tc.mode); got != tc.want {
				t.Fatalf("authModeWiderThanOwner(goos=%s, mode=%04o) = %v, want %v; a file written 0600 reads 0666 on windows, so the bits are not a refusal there (#915)", tc.goos, tc.mode, got, tc.want)
			}
		})
	}
	// The copy's own question: did the write end exactly 0600? Windows reports 0666 for
	// every readable file, so the copy cannot be shown owner-only and is not refused.
	for _, tc := range []struct {
		name string
		goos string
		mode os.FileMode
		want bool
	}{
		{"windows_0600", "windows", 0o600, false},
		{"windows_0666", "windows", 0o666, false},
		{"linux_0600", "linux", 0o600, false},
		{"linux_0644", "linux", 0o644, true},
		{"linux_0666", "linux", 0o666, true},
	} {
		t.Run("copy_"+tc.name, func(t *testing.T) {
			if got := authModeNotOwnerOnly(tc.goos, tc.mode); got != tc.want {
				t.Fatalf("authModeNotOwnerOnly(goos=%s, mode=%04o) = %v, want %v (#915)", tc.goos, tc.mode, got, tc.want)
			}
		})
	}
}

// ISSUE #881: secret implies env_var, and the model gate compares provider/model as one
// name -- a description's model without a slash takes the description's provider as its
// prefix. A secret-only description (no env_var) with provider opencode and model
// deepseek-v4-flash runs under --model opencode/deepseek-v4-flash; --model opencode/other
// is refused naming both; --model other/deepseek-v4-flash is refused too, because the
// provider half matters.
func TestNativeWorkerModelGateComparesQualifiedName(t *testing.T) {
	bin := nativeHarness(t)
	writeSecretOnly := func(t *testing.T) string {
		t.Helper()
		home := t.TempDir()
		desc := map[string]any{
			"name": "opencode-1", "provider": "opencode", "model": "deepseek-v4-flash",
			"secret": "CARD881_SECRET", "usage": "opencode",
			"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
			"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		}
		raw, err := json.MarshalIndent(desc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "worker.json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Setenv("CARD881_SECRET", fakeKey)

	t.Run("qualified_match_is_accepted", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		if err := os.WriteFile(cardPath, []byte("a card\nFAKE-FINDINGS 0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		desc := writeSecretOnly(t)
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "opencode/deepseek-v4-flash",
			"--worker", desc, "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "10s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("provider opencode model deepseek-v4-flash under --model opencode/deepseek-v4-flash is accepted, got exit %d:\n%s%s", rc, stdout.String(), stderr.String())
		}
	})

	t.Run("model_mismatch_is_refused_naming_both", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		if err := os.WriteFile(cardPath, []byte("a card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		desc := writeSecretOnly(t)
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "opencode/other",
			"--worker", desc, "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "10s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 2 {
			t.Fatalf("--model opencode/other against model deepseek-v4-flash is refused exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
		}
		line := strings.TrimSpace(stderr.String())
		mustContain(t, "the refusal", line, "opencode/other")
		mustContain(t, "the refusal", line, "deepseek-v4-flash")
	})

	t.Run("provider_mismatch_is_refused", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		if err := os.WriteFile(cardPath, []byte("a card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// A description the CURRENT loader already accepts (env_var present beside
		// secret), so this subtest isolates the gate: the model half matches, only
		// the provider half differs, and the gate must still refuse.
		home := t.TempDir()
		desc := map[string]any{
			"name": "opencode-1", "provider": "opencode", "model": "deepseek-v4-flash",
			"env_var": "CARD881_ENV", "secret": "CARD881_SECRET", "usage": "opencode",
			"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
			"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		}
		t.Setenv("CARD881_SECRET", fakeKey)
		raw, err := json.MarshalIndent(desc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		descPath := filepath.Join(t.TempDir(), "worker.json")
		if err := os.WriteFile(descPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "other/deepseek-v4-flash",
			"--worker", descPath, "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "10s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 2 {
			t.Fatalf("--model other/deepseek-v4-flash against provider opencode is refused exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
		}
	})
}

// nativeWorkerDescription writes a worker description the native run can be pointed at: the
// model it pins, and the key named either by the legacy key_file (for --auth) or by the
// `secret` variable a nova-secrets exec would deliver.
func nativeWorkerDescription(t *testing.T, model, keyShape string) string {
	t.Helper()
	home := t.TempDir()
	desc := map[string]any{
		"name": "fake-1", "provider": "fake", "model": model,
		"env_var": "FAKE_KEY", "usage": "opencode",
		"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
	}
	if keyShape == "secret" {
		desc["secret"] = "FAKE_KEY"
	} else {
		key := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(key, []byte("FAKE_KEY="+fakeKey+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		desc["key_file"] = key
	}
	raw, err := json.MarshalIndent(desc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestNativeWalledJobPathWithSpace: a walled native run whose root, slot and job directory
// all sit under a path holding a space must finish like any other -- the NATIVE OK line is
// printed and RESULT.md is written under the job directory. The wall names the cwd it
// applied on its SANDBOX OK line, and the only field a reader may trust is the machine
// receipt of the raw path bytes: the readable cwd=<dir> field is oneline.Field, which
// renders the space as \x20, and comparing that escaped spelling to the real job directory
// refused a run whose child had already finished (#624).
func TestNativeWalledJobPathWithSpace(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root := filepath.Join(t.TempDir(), "My Bench")
	slot := filepath.Join(root, "slot-1")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-PWD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "space-label", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--sandbox", sandbox}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("a walled run under a root with a space exits 0, got %d:\nstdout: %s\nstderr: %s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK ") {
		t.Fatalf("the NATIVE OK line is printed for a root with a space:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(slot, "jobs", "space-label", "RESULT.md")); err != nil {
		t.Fatalf("RESULT.md is written under a job path with a space: %v", err)
	}
}

// TestNativeHoldsAJobLease: the launcher takes <job>/.lease BEFORE the child starts and
// releases it when the run ends (issue #1499). The child itself is the witness -- it reads
// the lease from inside the job and reports its length -- because the file's whole purpose
// is to exist WHILE the card runs: that is what the bench's hygiene pass reads instead of
// guessing from how long the capture has been quiet. A card in one long model call is
// silent and alive, and the reaper that could not tell the difference deleted two certify
// trees, and a running card's HOME and TMPDIR, on 2026-09-19.
func TestNativeHoldsAJobLease(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "lease-card"
	jobDir := filepath.Join(slot, "jobs", label)
	card := []byte("FAKE-CAT " + filepath.Join(jobDir, swarm.JobLeaseName) + "\n")

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: card, slotDir: slot, root: root, deadline: 30 * time.Second,
		noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}

	raw, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
	if err != nil {
		t.Fatalf("the run wrote no harness output log: %v", err)
	}
	want := "cat " + filepath.Join(jobDir, swarm.JobLeaseName) + ": ok len="
	if !strings.Contains(string(raw), want) {
		t.Fatalf("the child could not read a lease at %s while it ran; the capture says:\n%s",
			filepath.Join(jobDir, swarm.JobLeaseName), raw)
	}
	if strings.Contains(string(raw), want+"0\n") {
		t.Errorf("the lease was empty while the child ran; it must name the launcher's pid:\n%s", raw)
	}
	if _, err := os.Lstat(filepath.Join(jobDir, swarm.JobLeaseName)); !os.IsNotExist(err) {
		t.Errorf("the lease outlived the run (%v); a finished job must leave nothing that claims to be alive", err)
	}
}

// TestNativeArgvGrantsTheDotnetMutexRootAsAWrite is the THIRD KIND on the production argv,
// and the ORDER it must arrive in.
//
// THE KIND. /tmp/.dotnet is the .NET runtime's named-mutex root, hard-coded to /tmp and
// deaf to TMPDIR, so `dotnet build` and `dotnet test` die inside the bare wall on
// `'NuGet-Migrations' ... open("/tmp/.dotnet/shm", 0x80000, 0x0) == -1; errno == EACCES`
// (hulk) and `stat("/tmp/.dotnet", ...) == -1; errno == EPERM` (batman), measured
// 2026-09-20. --read is NOT enough: the runtime creates its session directory there.
//
// THE ORDER. nova-sandbox plants the run's own temp directory in the FIRST --write, and a
// toolchain write root named ahead of the job put `.nova-sandbox-tmp` inside /tmp/.dotnet,
// a bench-shared directory (measured). So the first --write on this argv is the job
// directory, always, and the toolchain roots are appended after every write the run owns.
func TestNativeArgvGrantsTheDotnetMutexRootAsAWrite(t *testing.T) {
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := nativeRunConfig{slotDir: slot, benchHome: home, benchOS: runtime.GOOS}
	argv := nativeSandboxArgv(bin, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))

	// THE FIRST --write IS THE JOB, on every argv this builder makes. This holds on a
	// machine with no /tmp/.dotnet too, which is the point: it is the invariant a future
	// writable root must not break, not a fact about this bench.
	var firstWrite string
	for i, a := range argv {
		if a == "--write" && i+1 < len(argv) {
			firstWrite = argv[i+1]
			break
		}
	}
	if firstWrite != jobDir {
		t.Errorf("the first --write is %s and not the job directory %s; nova-sandbox plants the run's temp directory in the first write root, and a toolchain write root ahead of the job put it inside a bench-shared directory:\n%s",
			firstWrite, jobDir, strings.Join(argv, " "))
	}

	// THE KIND, for every writable root the list declares that is actually on this machine.
	// A bench without /tmp/.dotnet grants nothing (rule 5 refuses a path that is not there,
	// so ToolchainRoots skips it) and there is nothing to assert; the declaration itself is
	// held by internal/swarm's TestTheWallGrantsTheDotnetMutexRoot on every OS regardless.
	var checked int
	for _, root := range swarm.ToolchainRoots(runtime.GOOS, home) {
		if !root.Write {
			continue
		}
		checked++
		if !hasFlagPair(argv, "--write", root.Path) {
			t.Errorf("the toolchain root %s is declared writable and is not on --write:\n%s", root.Path, strings.Join(argv, " "))
		}
		for _, wrong := range []string{"--read", "--read-noexec"} {
			if hasFlagPair(argv, wrong, root.Path) {
				t.Errorf("the writable toolchain root %s is also on %s; a root is on one flag:\n%s", root.Path, wrong, strings.Join(argv, " "))
			}
		}
		// AND IT IS NOT THE FIRST WRITE, which is the same invariant read from the other
		// side: whatever else moves in this builder, the shared root arrives after the
		// run's own.
		if root.Path == firstWrite {
			t.Errorf("the bench-shared toolchain root %s is the FIRST --write, so the run's temp directory would be planted inside it:\n%s", root.Path, strings.Join(argv, " "))
		}
	}
	if checked == 0 {
		t.Logf("no writable toolchain root resolves on this machine (%s): the kind's mapping is held by internal/swarm, and the in-wall proof runs on a provisioned bench", runtime.GOOS)
	}
}

// TestInWallTheProductionArgvBuildsAndLinks is THE PROOF, and it is deliberately not a unit
// test: it takes the argv the runner itself builds -- nativeSandboxArgv, the production
// path, with no grant added by hand -- keeps every wall flag, and swaps only the command
// after `--` for a one-file build. Anything a hand-written nova-sandbox line would prove is
// a proof about the hand-written line.
//
// It is skipped unless NOVA_WALL_PROOF=1, because it needs a bench provisioned to the
// standard (a toolchain under ~/sdk, and /tmp/.dotnet for the cs leg). RED on origin/dev and
// GREEN on this branch, run on hulk and on batman/superman, 2026-09-20.
func TestInWallTheProductionArgvBuildsAndLinks(t *testing.T) {
	if os.Getenv("NOVA_WALL_PROOF") == "" {
		t.Skip("the in-wall proof runs on a provisioned bench: NOVA_WALL_PROOF=1 go test ./cmd/nova-swarm/ -run TestInWallTheProductionArgv")
	}
	wall, err := exec.LookPath("nova-sandbox")
	if err != nil {
		t.Skipf("no nova-sandbox on PATH: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	legs := []struct {
		name string
		os   string
		file string
		body string
		sh   string
		tok  string
	}{
		{name: "cs", file: "Program.cs", body: "System.Console.WriteLine(\"WALL-CS-OK\");\n",
			sh: "dotnet build p.csproj", tok: "Build succeeded"},
		{name: "cc", os: "darwin", file: "a.c", body: "#include <stdio.h>\nint main(void){puts(\"WALL-C-OK\");return 0;}\n",
			sh: "cc -std=c99 -Wall -Werror a.c -o a && ./a", tok: "WALL-C-OK"},
	}
	for _, leg := range legs {
		if leg.os != "" && leg.os != runtime.GOOS {
			continue
		}
		t.Run(leg.name, func(t *testing.T) {
			bin := nativeHarness(t)
			_, slot := aSlot(t)
			jobDir := filepath.Join(slot, "jobs", "wall-proof")
			dataHome := filepath.Join(slot, "data")
			tmpDir := filepath.Join(slot, "tmp", "wall-proof")
			for _, d := range []string{jobDir, dataHome, tmpDir} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(jobDir, leg.file), []byte(leg.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if leg.name == "cs" {
				csproj := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`
				if err := os.WriteFile(filepath.Join(jobDir, "p.csproj"), []byte(csproj), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cfg := nativeRunConfig{slotDir: slot, benchHome: home, benchOS: runtime.GOOS, noSharedCaches: true}
			argv := nativeSandboxArgv(bin, cfg, dataHome, jobDir, tmpDir)
			cut := -1
			for i, a := range argv {
				if a == "--" {
					cut = i
					break
				}
			}
			if cut < 0 {
				t.Fatalf("the production argv carries no --:\n%s", strings.Join(argv, " "))
			}
			// The wall's own flags, verbatim, and then OUR command instead of the harness.
			// The settings a cs card would set for itself go through `env`; everything that
			// is the RUNNER's to decide -- HOME, TMPDIR, PATH, and on darwin DEVELOPER_DIR,
			// which is what replaces a /var/db read root -- comes from nativeChildEnv below,
			// the same function the real run uses. Nothing here is a grant typed by hand,
			// which is the whole point: this file compiles on the commit before the change
			// too, so its RED there is this test and not a different one.
			run := append(append([]string{}, argv[:cut+1]...), "env",
				"DOTNET_CLI_HOME="+filepath.Join(home, "sdk", "dotnet-home"),
				"DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1",
				"NUGET_PACKAGES="+filepath.Join(dataHome, ".nuget", "packages"),
				"sh", "-c", leg.sh)
			cmd := exec.Command(wall, run...)
			cmd.Env = nativeChildEnv(dataHome, jobDir, tmpDir, "", "", "", "")
			out, _ := cmd.CombinedOutput()
			if !strings.Contains(string(out), leg.tok) {
				t.Errorf("the %s leg did not %s inside the wall built by the production argv.\nargv: %s\noutput:\n%s",
					leg.name, leg.tok, strings.Join(run, " "), out)
			}
		})
	}
}
