package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// S7, nova-tools#2498: the harness wall. A card cannot read outside PATHS and the
// tests of those paths; webfetch is deny; only pre-approved commands; MODE: script
// is --net-deny. These tests go red if the wall admits an outsider path or a
// network fetch. They do not rewrite nativeSandboxArgv — TODO launcher: Rowan.

func s7Card(t *testing.T, extra ...string) []byte {
	t.Helper()
	h := []string{
		"KIND: fix-red",
		"PATHS: keep/in.go",
		"TEST: ./keep TestIn",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#2498",
	}
	return typedCard(append(h, extra...)...)
}

func s7Terms(t *testing.T, extra ...string) CardWallTerms {
	t.Helper()
	terms, err := WallTerms(s7Card(t, extra...))
	if err != nil {
		t.Fatalf("WallTerms: %v", err)
	}
	return terms
}

// TestWallTermsRefuseAPathOutsidePATHS is S7 at the matcher: a file the card did
// not name is not readable, even when it sits next to one it did.
func TestWallTermsRefuseAPathOutsidePATHS(t *testing.T) {
	terms := s7Terms(t)
	if !terms.AdmitsRead("keep/in.go") {
		t.Fatal("PATHS: keep/in.go must admit keep/in.go")
	}
	for _, outsider := range []string{
		"keep/other.go",
		"drop/out.go",
		"docs/SPEC-DECIDE.md",
		"cmd/nova-bus/main.go",
		"../etc/passwd",
		"/etc/passwd",
	} {
		if terms.AdmitsRead(outsider) {
			t.Errorf("the wall admitted %s, which is outside PATHS", outsider)
		}
	}
}

// TestWallTermsAdmitTheTestsOfPATHS is the other half of the read scope: the
// `_test.go` sibling, testdata under the same directory, and the TEST: package's
// tests. A card that cannot read its own test spends a turn on a refusal.
func TestWallTermsAdmitTheTestsOfPATHS(t *testing.T) {
	terms := s7Terms(t)
	for _, want := range []string{
		"keep/in_test.go",
		"keep/testdata/fixture.txt",
		"keep/other_test.go",
	} {
		if !terms.AdmitsRead(want) {
			t.Errorf("the tests of PATHS include %s; Scope=%v", want, terms.Scope)
		}
	}
}

// TestWallTermsRefuseANetworkFetch is S7 no web. A webfetch, named as a URL the
// tests are allowed to write (example.com), is not admitted. The fence's webfetch
// key is deny. A mutation that returns true, or that writes allow, turns this red.
func TestWallTermsRefuseANetworkFetch(t *testing.T) {
	terms := s7Terms(t)
	if AdmitsFetch("https://example.com/") {
		t.Fatal("webfetch of https://example.com/ is admitted; S7 is no web")
	}
	if terms.Webfetch != FenceDeny {
		t.Fatalf("webfetch is %q, want %q", terms.Webfetch, FenceDeny)
	}
	block := FencePermission("/job", nil)
	if block[FenceWebfetch] != FenceDeny {
		t.Errorf("the harness fence webfetch is %v, want deny", block[FenceWebfetch])
	}
}

// TestWallTermsOnlyPreApprovedCommands: a first token on the allowlist runs; curl,
// wget, ssh, nova-sandbox, nova-secrets and a login shell do not.
func TestWallTermsOnlyPreApprovedCommands(t *testing.T) {
	for _, line := range []string{"go test ./keep", "gofmt -l keep/in.go", "git status", "make test", "rg AdmitsRead"} {
		if !AdmitsCommand(line) {
			t.Errorf("pre-approved command %q was denied", line)
		}
	}
	for _, line := range []string{
		"curl https://example.com/",
		"wget https://example.com/",
		"ssh hulk",
		"nova-sandbox --write /tmp -- true",
		"nova-secrets exec -- true",
		"bash -l",
		"sudo go test",
	} {
		if AdmitsCommand(line) {
			t.Errorf("command %q is not pre-approved and was admitted", line)
		}
	}
}

// TestWallTermsScriptIsNetDeny is Johnny's pin: MODE: script is --net-deny, not
// native's nopromise. A model card without the word stays nopromise so the
// provider API is the work.
func TestWallTermsScriptIsNetDeny(t *testing.T) {
	script := s7Terms(t, "MODE: script")
	if !script.NetDeny {
		t.Fatal("MODE: script is --net-deny; NetDeny is false")
	}
	model := s7Terms(t)
	if model.NetDeny {
		t.Fatal("a model card inherited --net-deny; the provider API is the work")
	}
}

// TestWallTermsReadRootsDoNotAdmitAnOutsider: a directory glob can be a --read
// today. PATHS: keep/** names keep and not drop. Feeding those roots to the
// existing sandbox admission (sandbox.Inside) goes red if drop is inside any
// granted root — the over-grant a parent-directory --read of a file glob would
// be. File-level PATHS name no --read root.
func TestWallTermsReadRootsDoNotAdmitAnOutsider(t *testing.T) {
	repo := t.TempDir()
	keep := filepath.Join(repo, "keep")
	drop := filepath.Join(repo, "drop")
	for _, d := range []string{keep, drop} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inFile := filepath.Join(keep, "in.go")
	outFile := filepath.Join(drop, "out.go")
	if err := os.WriteFile(inFile, []byte("package keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outFile, []byte("package drop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := filepath.EvalSymlinks(repo); err == nil {
		repo = got
		keep = filepath.Join(repo, "keep")
		drop = filepath.Join(repo, "drop")
		inFile = filepath.Join(keep, "in.go")
		outFile = filepath.Join(drop, "out.go")
	}

	dirCard := typedCard(
		"KIND: fix-red",
		"PATHS: keep/**",
		"TEST: ./keep TestIn",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#2498",
	)
	terms, err := WallTerms(dirCard)
	if err != nil {
		t.Fatalf("WallTerms: %v", err)
	}
	roots := terms.ReadRoots(repo)
	if len(roots) == 0 {
		t.Fatalf("PATHS keep/** names a --read root under %s", repo)
	}
	keepIn := false
	for _, r := range roots {
		if sandbox.Inside(outFile, r) || sandbox.Inside(drop, r) {
			t.Errorf("ReadRoots admitted outsider %s via %s", drop, r)
		}
		if sandbox.Inside(inFile, r) {
			keepIn = true
		}
	}
	if !keepIn {
		t.Errorf("ReadRoots did not admit keep/in.go; roots=%v", roots)
	}

	fileTerms := s7Terms(t)
	if got := fileTerms.ReadRoots(repo); len(got) != 0 {
		t.Errorf("PATHS keep/in.go is a file glob; a --read of keep would admit keep/other.go; got %v", got)
	}
}

// TestSandboxPATHSReadSetDoesNotAdmitAnOutsider is the same admission through
// sandbox.Build: --read keep, write set beside it, NetDeny from MODE: script.
// A policy whose Reads contain drop, or whose Net() is not denied on a script
// card, turns this red.
func TestSandboxPATHSReadSetDoesNotAdmitAnOutsider(t *testing.T) {
	windowsIsNotABench(t)
	base := t.TempDir()
	write := filepath.Join(base, "w")
	home := filepath.Join(write, "home")
	keep := filepath.Join(base, "keep")
	drop := filepath.Join(base, "drop")
	for _, d := range []string{write, home, keep, drop} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := sandboxEcho(t)
	terms := s7Terms(t, "MODE: script")
	if !terms.NetDeny {
		t.Fatal("the script card did not ask for --net-deny")
	}
	p, bad := sandbox.Build(sandbox.Input{
		Reads:   []string{keep},
		Writes:  []string{write},
		Home:    home,
		NetDeny: terms.NetDeny,
		Argv:    []string{cmd},
	})
	if len(bad) > 0 {
		t.Fatalf("Build refused a sound PATHS --read: %v", bad)
	}
	if p.Net() != "denied" {
		t.Fatalf("MODE: script policy Net()=%s, want denied", p.Net())
	}
	dropFile := filepath.Join(drop, "out.go")
	if err := os.WriteFile(dropFile, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Reads {
		if sandbox.Inside(dropFile, r) || sandbox.Inside(drop, r) {
			t.Errorf("sandbox Reads admitted outsider %s via %s", drop, r)
		}
	}
	keepFile := filepath.Join(keep, "in.go")
	if err := os.WriteFile(keepFile, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inRead := false
	for _, r := range p.Reads {
		if sandbox.Inside(keepFile, r) {
			inRead = true
		}
	}
	if !inRead {
		t.Errorf("sandbox Reads did not admit keep/in.go; Reads=%v", p.Reads)
	}
}

func sandboxEcho(t *testing.T) string {
	t.Helper()
	if fi, err := os.Stat("/bin/echo"); err == nil && !fi.IsDir() {
		return "/bin/echo"
	}
	found, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("no echo to resolve; sandbox.Build resolves the command before a policy exists")
	}
	return found
}

// TestSpecNamesTheHarnessWall is the deliverable in the specs: a rule renamed
// out of SPEC-SWARM or SPEC-SANDBOX is red here before any launcher is trusted.
func TestSpecNamesTheHarnessWall(t *testing.T) {
	swarmDoc := readSpec(t, "SPEC-SWARM.md")
	sandboxDoc := readSpec(t, "SPEC-SANDBOX.md")
	section := swarmSection(t, swarmDoc, "## The harness wall (S7, issue #2498)")
	for _, phrase := range []string{
		"Read scope is PATHS and their tests",
		"No web",
		"Only pre-approved commands",
		"`MODE: script` is `--net-deny`",
		"TODO launcher: Rowan",
		"TestWallTermsRefuseAPathOutsidePATHS",
		"TestWallTermsRefuseANetworkFetch",
		"Do not pin the #599 nested SBPL form",
	} {
		if !strings.Contains(section, phrase) {
			t.Errorf("SPEC-SWARM.md S7 section does not name %q", phrase)
		}
	}
	if strings.Contains(section, "(local ip (host") && !strings.Contains(section, "Do not pin the #599 nested SBPL form") {
		t.Error("the S7 section pins the #599 nested SBPL form")
	}
	sandboxSection := swarmSection(t, sandboxDoc, "### MODE: script is --net-deny (S7, issue #2498)")
	for _, phrase := range []string{
		"TODO launcher: Rowan",
		"--net-deny",
		"Do not pin the #599 nested SBPL form",
		`(remote ip "localhost:PORT")`,
	} {
		if !strings.Contains(sandboxSection, phrase) {
			t.Errorf("SPEC-SANDBOX.md MODE: script section does not name %q", phrase)
		}
	}
}

func readSpec(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	if err != nil {
		t.Fatalf("%s is missing: %s", name, err)
	}
	return string(raw)
}

func swarmSection(t *testing.T, spec, header string) string {
	t.Helper()
	start := strings.Index(spec, header)
	if start < 0 {
		t.Fatalf("the spec has no %q section", header)
		return ""
	}
	rest := spec[start+len(header):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
