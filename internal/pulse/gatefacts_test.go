package pulse

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// factsGit is a repo with one base commit. Tests drive G2/G3 off real git and G1
// off a fake rollup -- no gh, no network.
func factsLab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	factsGitCmd(t, dir, "init", "-q", "-b", "main")
	writeFacts(t, dir, "sign/sign.go", "package sign\n")
	writeFacts(t, dir, "other/other.go", "package other\n")
	factsGitCmd(t, dir, "add", "-A")
	factsGitCmd(t, dir, "commit", "-q", "-m", "base")
	return dir
}

func factsGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Rowan", "GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFacts(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRollup(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "rollup.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runFacts(t *testing.T, in GateFactsInput) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout = &out
	in.Stderr = &errb
	if in.Timeout == 0 {
		in.Timeout = 30 * time.Second
	}
	code := GateFacts(in)
	return strings.TrimSpace(out.String()), strings.TrimSpace(errb.String()), code
}

type stubCI struct{ status, job, test string }

func (s stubCI) CI(string, int, string) (string, string, string, error) {
	return s.status, s.job, s.test, nil
}

func successCI() CISource { return stubCI{status: ciOKSuccess} }

// gate-facts-conflict-exits-2: a merge-tree that cannot land against the base
// prints merge-tree=conflict, names the file, and exits 2. This is the red
// control; a stamp that called that tree clean would let harvest open a PR
// a reader then HOLDs for rebase.
func TestGateFactsConflictExits2(t *testing.T) {
	dir := factsLab(t)
	factsGitCmd(t, dir, "checkout", "-q", "-b", "card")
	writeFacts(t, dir, "sign/sign.go", "package sign\n// card\n")
	factsGitCmd(t, dir, "add", "-A")
	factsGitCmd(t, dir, "commit", "-q", "-m", "card")
	factsGitCmd(t, dir, "checkout", "-q", "main")
	writeFacts(t, dir, "sign/sign.go", "package sign\n// main\n")
	factsGitCmd(t, dir, "add", "-A")
	factsGitCmd(t, dir, "commit", "-q", "-m", "main")
	factsGitCmd(t, dir, "checkout", "-q", "card")

	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Source: successCI(),
	})
	if code != 2 {
		t.Fatalf("conflict exit = %d, want 2; out=%q err=%q", code, out, errb)
	}
	if !strings.Contains(out, "merge-tree=conflict") {
		t.Fatalf("want merge-tree=conflict on stdout, got %q", out)
	}
	if !strings.Contains(out, "sign/sign.go") {
		t.Fatalf("conflict receipt does not name the file: %q", out)
	}
	if strings.Contains(out, "\n") {
		t.Fatalf("receipt is more than one line: %q", out)
	}
}

// gate-facts-clean-exits-0: a tree that merges clean against the landing base
// prints merge-tree=clean and exits 0 when ci-ok is success and PATHS cover the diff.
func TestGateFactsCleanExits0(t *testing.T) {
	dir := factsLab(t)
	factsGitCmd(t, dir, "checkout", "-q", "-b", "card")
	writeFacts(t, dir, "sign/sign.go", "package sign\nfunc Sign() {}\n")
	factsGitCmd(t, dir, "add", "-A")
	factsGitCmd(t, dir, "commit", "-q", "-m", "head")

	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Source: successCI(), Paths: "sign/**",
	})
	if code != 0 {
		t.Fatalf("clean exit = %d, want 0; out=%q err=%q", code, out, errb)
	}
	if !strings.Contains(out, "merge-tree=clean") {
		t.Fatalf("want merge-tree=clean, got %q", out)
	}
	if !strings.Contains(out, "ci-ok=success") {
		t.Fatalf("want ci-ok=success, got %q", out)
	}
	if !strings.Contains(out, "paths=ok") {
		t.Fatalf("want paths=ok, got %q", out)
	}
	if strings.Contains(out, "job=") {
		t.Fatalf("success must not carry job=/test=: %q", out)
	}
}

// gate-facts-ci-failure-names-job-and-test: G1 from a fake rollup, never gh.
func TestGateFactsCIFailureNamesJobAndTest(t *testing.T) {
	dir := factsLab(t)
	factsGitCmd(t, dir, "checkout", "-q", "-b", "card")
	writeFacts(t, dir, "sign/sign.go", "package sign\nfunc Sign() {}\n")
	factsGitCmd(t, dir, "add", "-A")
	factsGitCmd(t, dir, "commit", "-q", "-m", "head")
	head := factsGitCmd(t, dir, "rev-parse", "HEAD")
	rollup := writeRollup(t, dir, `{"ci-ok":"failure","job":"studio-fast","test":"TestGateHoldsTheBench","head":"`+head+`"}`)

	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Rollup: rollup, Paths: "sign/**",
	})
	if code != 1 {
		t.Fatalf("ci failure exit = %d, want 1; out=%q err=%q", code, out, errb)
	}
	if !strings.Contains(out, "ci-ok=failure") || !strings.Contains(out, "job=studio-fast") || !strings.Contains(out, "test=TestGateHoldsTheBench") {
		t.Fatalf("want job and test from the rollup, got %q", out)
	}
	if !strings.Contains(out, "merge-tree=clean") {
		t.Fatalf("ci-red with a clean tree still stamps merge-tree=clean: %q", out)
	}
}

func TestGateFactsPendingWhenNoRollup(t *testing.T) {
	dir := factsLab(t)
	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Paths: "none",
	})
	if code != 1 {
		t.Fatalf("pending exit = %d, want 1; out=%q err=%q", code, out, errb)
	}
	if !strings.Contains(out, "ci-ok=pending") || !strings.Contains(out, "job=-") {
		t.Fatalf("want ci-ok=pending job=-, got %q", out)
	}
}

func TestGateFactsExtraPathsNamed(t *testing.T) {
	dir := factsLab(t)
	factsGitCmd(t, dir, "checkout", "-q", "-b", "card")
	writeFacts(t, dir, "sign/sign.go", "package sign\nfunc Sign() {}\n")
	writeFacts(t, dir, "other/other.go", "package other\nfunc Extra() {}\n")
	factsGitCmd(t, dir, "add", "-A")
	factsGitCmd(t, dir, "commit", "-q", "-m", "head")

	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Source: successCI(), Paths: "sign/**",
	})
	if code != 1 {
		t.Fatalf("extra-paths exit = %d, want 1; out=%q err=%q", code, out, errb)
	}
	if !strings.Contains(out, "paths=extra") {
		t.Fatalf("want paths=extra, got %q", out)
	}
	if !strings.Contains(out, "other/other.go") {
		t.Fatalf("want the extra file named, got %q", out)
	}
	if strings.Contains(out, "sign/sign.go") && strings.Contains(out, "extra=sign") {
		t.Fatalf("sign/sign.go matches PATHS and is not extra: %q", out)
	}
}

func TestGateFactsReceiptFileMatchesStdout(t *testing.T) {
	dir := factsLab(t)
	receipt := filepath.Join(dir, "receipt.txt")
	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Source: successCI(), Paths: "none", ReceiptFile: receipt,
	})
	if code != 0 {
		t.Fatalf("exit = %d; out=%q err=%q", code, out, errb)
	}
	raw, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != out {
		t.Fatalf("receipt file %q != stdout %q", got, out)
	}
}

func TestGateFactsRefusesMissingFlags(t *testing.T) {
	var out, errb bytes.Buffer
	code := GateFacts(GateFactsInput{Stdout: &out, Stderr: &errb})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--dir") {
		t.Fatalf("stderr = %q, want --dir named", errb.String())
	}
}

func TestGateFactsRollupFileNeverCallsGh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the PATH gh fake is a shell script")
	}
	dir := factsLab(t)
	called := filepath.Join(dir, "gh-called")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho called > " + called + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	head := factsGitCmd(t, dir, "rev-parse", "HEAD")
	rollup := writeRollup(t, dir, `{"ci-ok":"success","head":"`+head+`"}`)
	_, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Rollup: rollup, Paths: "none", PR: 99, Repo: "owner/name",
	})
	if code != 0 {
		t.Fatalf("exit = %d err=%q", code, errb)
	}
	if _, err := os.Stat(called); err == nil {
		t.Fatal("gh was invoked; --rollup must be the whole G1 source")
	}
}

func TestGateFactsReadsCardPATHS(t *testing.T) {
	dir := factsLab(t)
	factsGitCmd(t, dir, "checkout", "-q", "-b", "card")
	writeFacts(t, dir, "sign/sign.go", "package sign\nfunc Sign() {}\n")
	writeFacts(t, dir, "other/other.go", "package other\nfunc Extra() {}\n")
	factsGitCmd(t, dir, "add", "-A")
	factsGitCmd(t, dir, "commit", "-q", "-m", "card")
	card := filepath.Join(dir, "card.md")
	if err := os.WriteFile(card, []byte("RESULT x sha=aaaaaaaaaaaa\nPATHS: sign/**\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Source: successCI(), Card: card,
	})
	if code != 1 {
		t.Fatalf("exit = %d err=%q out=%q", code, errb, out)
	}
	if !strings.Contains(out, "paths=extra") || !strings.Contains(out, "other/other.go") {
		t.Fatalf("want extra from the card PATHS, got %q", out)
	}
}

func TestGateFactsGhRollupShape(t *testing.T) {
	dir := factsLab(t)
	head := factsGitCmd(t, dir, "rev-parse", "HEAD")
	rollup := writeRollup(t, dir, `{"headRefOid":"`+head+`","statusCheckRollup":[{"name":"ci-ok","status":"COMPLETED","conclusion":"FAILURE"},{"name":"test (1/4 studio)","status":"COMPLETED","conclusion":"FAILURE"}],"test":"TestFoo"}`)
	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Rollup: rollup, Paths: "none",
	})
	if code != 1 {
		t.Fatalf("exit = %d err=%q out=%q", code, errb, out)
	}
	if !strings.Contains(out, "ci-ok=failure") {
		t.Fatalf("want ci-ok=failure from the gh-shaped rollup, got %q", out)
	}
	if !strings.Contains(out, "job=test") {
		t.Fatalf("want the failing job name, got %q", out)
	}
	if !strings.Contains(out, "test=TestFoo") {
		t.Fatalf("want test=TestFoo from the rollup, got %q", out)
	}
}

// gate-facts-mismatched-head: a rollup for another commit must not be stamped
// as this head's ci-ok. The file says success; the receipt must not.
func TestGateFactsMismatchedHeadRefuses(t *testing.T) {
	dir := factsLab(t)
	other := strings.Repeat("ab", 20)
	if factsGitCmd(t, dir, "rev-parse", "HEAD") == other {
		t.Fatal("fixture sha collided with the lab head")
	}
	rollup := writeRollup(t, dir, `{"ci-ok":"success","headRefOid":"`+other+`"}`)
	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Rollup: rollup, Paths: "none",
	})
	if code != 2 {
		t.Fatalf("mismatch exit = %d, want 2; out=%q err=%q", code, out, errb)
	}
	if out != "" {
		t.Fatalf("mismatch must not stamp a receipt, got %q", out)
	}
	if !strings.Contains(errb, other) || !strings.Contains(errb, "not requested head") {
		t.Fatalf("refusal should name both heads, got %q", errb)
	}
}

// An offline rollup with no head is the same hole: success with nothing to bind.
func TestGateFactsRollupWithoutHeadRefuses(t *testing.T) {
	dir := factsLab(t)
	rollup := writeRollup(t, dir, `{"ci-ok":"success"}`)
	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", Rollup: rollup, Paths: "none",
	})
	if code != 2 {
		t.Fatalf("unbound exit = %d, want 2; out=%q err=%q", code, out, errb)
	}
	if out != "" || !strings.Contains(errb, "names no head") {
		t.Fatalf("want a no-head refusal and no receipt, out=%q err=%q", out, errb)
	}
}

func TestGateFactsLiveHeadMismatchRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the PATH gh fake is a shell script")
	}
	dir := factsLab(t)
	other := strings.Repeat("cd", 20)
	body := `{"headRefOid":"` + other + `","statusCheckRollup":[{"name":"ci-ok","status":"COMPLETED","conclusion":"SUCCESS"}]}`
	args := installFactsGH(t, dir, body)
	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", PR: 2503, Repo: "owner/name", Paths: "none",
	})
	if code != 2 {
		t.Fatalf("live mismatch exit = %d, want 2; out=%q err=%q", code, out, errb)
	}
	if out != "" || strings.Contains(out, "ci-ok=success") {
		t.Fatalf("live mismatch stamped a receipt: %q", out)
	}
	if !strings.Contains(errb, other) || !strings.Contains(errb, "not requested head") {
		t.Fatalf("refusal should name the pr head, got %q", errb)
	}
	raw, err := os.ReadFile(args)
	if err != nil {
		t.Fatalf("gh was not invoked: %v; err=%q", err, errb)
	}
	if !strings.Contains(string(raw), "headRefOid") || !strings.Contains(string(raw), "statusCheckRollup") {
		t.Fatalf("gh was not asked for headRefOid, args %q", raw)
	}
}

func TestGateFactsLiveHeadMatchStamps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the PATH gh fake is a shell script")
	}
	dir := factsLab(t)
	head := factsGitCmd(t, dir, "rev-parse", "HEAD")
	body := `{"headRefOid":"` + head + `","statusCheckRollup":[{"name":"ci-ok","status":"COMPLETED","conclusion":"SUCCESS"}]}`
	args := installFactsGH(t, dir, body)
	out, errb, code := runFacts(t, GateFactsInput{
		Dir: dir, Base: "main", Head: "HEAD", PR: 2503, Repo: "owner/name", Paths: "none",
	})
	if code != 0 {
		t.Fatalf("live match exit = %d, want 0; out=%q err=%q", code, out, errb)
	}
	if !strings.Contains(out, "ci-ok=success") || !strings.Contains(out, "head="+head[:12]) {
		t.Fatalf("want this head's success, got %q", out)
	}
	raw, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "headRefOid") {
		t.Fatalf("gh was not asked for headRefOid, args %q", raw)
	}
}

func installFactsGH(t *testing.T, dir, body string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(dir, "gh-args")
	bodyFile := filepath.Join(dir, "gh-body.json")
	if err := os.WriteFile(bodyFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > '" + argsFile + "'\ncat '" + bodyFile + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NOVA_TEST_NO_HOST", "")
	return argsFile
}
