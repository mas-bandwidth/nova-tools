package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// The verb's own surface: the one HYGIENE line, the cap-and-count listing, and the
// exit codes SPEC.md's Conventions give -- 0 clean, 1 findings, 2 could not run.
// SPEC-TOOLWORK.md §3 rule 7 (PR #1637), issue #1647.

func hygGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Rowan", "GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func hygWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hygLab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	hygGit(t, dir, "init", "-q", "-b", "main")
	hygWrite(t, dir, "sign/sign.go", "package sign\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "base")
	hygGit(t, dir, "checkout", "-q", "-b", "card")
	return dir
}

func TestHygieneVerbPassesACleanBranch(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "sign/sign.go", "package sign\n\nfunc F() {}\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "clean")
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>", "--paths", "sign/**"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "HYGIENE OK base=main head=HEAD paths=sign/** findings=0") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestHygieneVerbExitsOneAndNamesTheFinding(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "sign/RESULT.md", "the worker's own report\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "ship the report")
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>", "--paths", "sign/**"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "HYGIENE FINDING reason=stray-file at=sign/RESULT.md") {
		t.Fatalf("stdout = %q, want the finding named", out.String())
	}
	if !strings.Contains(errb.String(), "HYGIENE NO ") || !strings.Contains(errb.String(), "findings=1") {
		t.Fatalf("stderr = %q, want the NO verdict line", errb.String())
	}
}

// A branch with no declared paths says paths=- and skips out-of-path. The field is
// PRINTED rather than omitted: a line that left it out would read as a bound that held.
func TestHygieneVerbSaysPathsDashWhenUnbounded(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "elsewhere/x.go", "package elsewhere\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "a friend's own branch")
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "paths=- findings=0") {
		t.Fatalf("stdout = %q, want paths=-", out.String())
	}
}

// There is no default identity. A range checked against nobody would admit anybody, so
// the missing flag is a refusal and never a fallback to the repository's own config.
func TestHygieneVerbRefusesWithoutAnIdentity(t *testing.T) {
	dir := hygLab(t)
	for _, args := range [][]string{
		{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD"},
		{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan"},
		{"hygiene", "--base", "main", "--head", "HEAD", "--identity", "Rowan <r@e.example>"},
		{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <r@e.example>", "--paths", "../out/**"},
		{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <r@e.example>", "--paths", "**"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Fatalf("%v: exit %d, want 2 (stdout %q stderr %q)", args, code, out.String(), errb.String())
		}
	}
}

func TestHygieneUsageNamesTheVerb(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "nova-check hygiene --repo <dir> --base <ref> --head <ref>") {
		t.Fatalf("the help does not carry the hygiene line:\n%s", out.String())
	}
}

// ---------------------------------------------------------------------------
// The cold read of 2026-09-19 (#1717, finding 9).

// hygWrite and hygGit run with the bench's config blanked, so a fixtureKey built here
// is the only key-SHAPED string anywhere near this test. It is built by parts, at test
// time, so no valid key for any provider is written into this repository.
func hygFixtureKey() string { return "gh" + "p_" + strings.Repeat("A", 36) }

// hygiene-never-prints-the-secret, at the VERB, which is the surface that matters: a
// finding travels into a gate's stdout, a PR body and whatever a coordinator pastes
// into a chat, and the package's own test can only prove that the Finding struct is
// clean. This one runs the verb and searches BOTH streams -- the finding line, the
// verdict line, the refusal path -- for the fixture string.
func TestHygieneVerbNeverPrintsTheKey(t *testing.T) {
	dir := hygLab(t)
	key := hygFixtureKey()
	hygWrite(t, dir, "sign/sign.go", "package sign\n\nconst token = \""+key+"\"\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "oops")
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>", "--paths", "sign/**"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	// The finding must be there: a verb that printed nothing at all would pass the
	// search below and have proved nothing.
	if !strings.Contains(out.String(), "HYGIENE FINDING reason=secret at=sign/sign.go:3") {
		t.Fatalf("stdout = %q, want the secret named by path and line", out.String())
	}
	for name, stream := range map[string]string{"stdout": out.String(), "stderr": errb.String()} {
		if strings.Contains(stream, key) {
			t.Fatalf("the matched text reached %s: %q", name, stream)
		}
	}
}

// ---------------------------------------------------------------------------
// Emma's bench dogfood of `nova-check hygiene`, 2026-09-19 (issues #1804, #1805).

// hygManyFindings is a branch with more findings than any sane --max: five commits
// by somebody who is not in the pool, so the listing caps and the MORE line prints.
// One of them also writes OUTSIDE the card's paths, so `--paths` changes the total
// and a remedy that dropped it would answer a different question.
func hygManyFindings(t *testing.T) string {
	t.Helper()
	dir := hygLab(t)
	for i := 0; i < 5; i++ {
		hygWrite(t, dir, fmt.Sprintf("sign/f%d.go", i), fmt.Sprintf("package sign\n\nfunc F%d() {}\n", i))
		if i == 4 {
			hygWrite(t, dir, "elsewhere/x.go", "package elsewhere\n")
		}
		hygGit(t, dir, "add", "-A")
		hygGit(t, dir, "-c", "user.name=Someone", "-c", "user.email=someone@elsewhere.example",
			"-c", "committer.email=someone@elsewhere.example", "commit", "-q",
			"--author", "Someone <someone@elsewhere.example>", "-m", fmt.Sprintf("c%d", i))
	}
	return dir
}

// hygMore pulls the two halves of the MORE line apart: the total it stands for, and
// the remedy that is meant to print it.
func hygMore(t *testing.T, stdout string) (total int, remedy string) {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "HYGIENE MORE ") {
			continue
		}
		_, rest, ok := strings.Cut(line, " total=")
		if !ok {
			t.Fatalf("the MORE line has no total= field: %q", line)
		}
		n, cmd, ok := strings.Cut(rest, " ")
		if !ok {
			t.Fatalf("the MORE line carries no remedy: %q", line)
		}
		total, err := strconv.Atoi(n)
		if err != nil {
			t.Fatalf("the MORE line's total is not a number: %q", line)
		}
		return total, cmd
	}
	t.Fatalf("no HYGIENE MORE line in:\n%s", stdout)
	return 0, ""
}

// #1804: "the MORE line carries the command that prints the rest" (SPEC.md §Conventions).
// It carried --repo, --base and --head and dropped --identity, --paths and --kind, so
// the one thing a capped listing exists to offer -- the rest of the list -- exited 2
// on `--identity is required` for everyone who pasted it.
func TestHygieneMoreCommandRunsAsPrinted(t *testing.T) {
	dir := hygManyFindings(t)
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD",
		"--identity", "Emma <emma@mas-bandwidth.com>", "--paths", "sign/**", "--kind", "fix-red", "--max", "2"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	total, remedy := hygMore(t, out.String())
	args, err := hygFields(remedy)
	if err != nil {
		t.Fatalf("the remedy %q cannot be split into arguments: %v", remedy, err)
	}
	if len(args) == 0 || args[0] != "nova-check" || args[1] != "hygiene" {
		t.Fatalf("the remedy does not start with `nova-check hygiene`: %q", remedy)
	}
	var out2, errb2 bytes.Buffer
	code2 := run(args[1:], &out2, &errb2)
	if code2 == 2 {
		t.Fatalf("the printed remedy does not run:\n  %s\nexit 2: %s", remedy, errb2.String())
	}
	if code2 != 1 {
		t.Fatalf("the printed remedy exited %d, want 1 (the same findings, uncapped)\nstdout:%s\nstderr:%s", code2, out2.String(), errb2.String())
	}
	// The whole point of the remedy: it prints the rest, and the rest is what the
	// capped run said it was. A remedy missing --paths or --kind would run and answer
	// a DIFFERENT question, which is the same failure one step quieter.
	first := strings.Count(out.String(), "HYGIENE FINDING ")
	all := strings.Count(out2.String(), "HYGIENE FINDING ")
	if all <= first {
		t.Fatalf("the remedy printed %d findings, the capped run printed %d: it is not the command that shows the rest", all, first)
	}
	if all != total {
		t.Fatalf("the remedy printed %d findings and the MORE line stood for %d: the remedy is not the same run with the cap lifted\n  %s", all, total, remedy)
	}
	if strings.Contains(out2.String(), "HYGIENE MORE ") {
		t.Fatalf("the remedy is still capped:\n%s", out2.String())
	}
}

// #1805: the help and the command reference told a reader to write the email inside a
// SECOND pair of angle brackets. `Rowan <<rowan@mas-bandwidth.com>>` parses -- it ends
// in `>` -- and leaves the email as `<rowan@mas-bandwidth.com>`, which equals no git
// author alive. A clean branch came back as an identity finding, and nothing in the
// output said why.
func TestHygieneRefusesAnEmailInAngleBrackets(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "sign/sign.go", "package sign\n\nfunc F() {}\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "clean")
	var out, errb bytes.Buffer
	code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD",
		"--identity", "Rowan <<rowan@example.com>>"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2: a malformed identity must be refused, never silently matched against nobody\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "Name <email>") {
		t.Fatalf("stderr = %q, want the refusal to spell the form it wants", errb.String())
	}
	if strings.Contains(out.String(), "HYGIENE FINDING") {
		t.Fatalf("a malformed identity produced findings about the branch: %q", out.String())
	}
}

// #1805, the other half: neither the help nor the command reference may teach the
// form that breaks. SPEC.md:1396 spells it `Name <email>` and these two must agree.
func TestHygieneIdentityFormIsSpelledTheSameEverywhere(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	cli, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"the help banner": out.String(), "docs/CLI.md": string(cli)} {
		if strings.Contains(text, "<<email>>") {
			t.Errorf("%s still shows `--identity \"<Name> <<email>>\"`; pasted as written it matches no git author (#1805)", name)
		}
		if !strings.Contains(text, `--identity "<Name> <email>"`) {
			t.Errorf("%s does not spell the identity form `--identity \"<Name> <email>\"`", name)
		}
	}
}

// hygFields splits the remedy the way the shell a reader pastes it into would: on
// spaces, except inside a double-quoted value, which holds the spaces and the angle
// brackets an identity is made of. A remedy that cannot survive this is a remedy
// nobody can paste.
func hygFields(cmd string) ([]string, error) {
	var args []string
	rest := strings.TrimSpace(cmd)
	for rest != "" {
		if !strings.HasPrefix(rest, `"`) {
			tok, tail, _ := strings.Cut(rest, " ")
			args = append(args, tok)
			rest = strings.TrimSpace(tail)
			continue
		}
		end := -1
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\\' {
				i++
				continue
			}
			if rest[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("unbalanced quote in %q", rest)
		}
		v, err := strconv.Unquote(rest[:end+1])
		if err != nil {
			return nil, err
		}
		args = append(args, v)
		rest = strings.TrimSpace(rest[end+1:])
	}
	return args, nil
}

// The Opus readers' dogfood over seventeen PRs, 2026-09-19 (issue #1848). `--kind` went
// straight through to hygiene.Check, where it unlocks an allowlisted stray exception and
// nothing else, so a kind the tool does not declare unlocked nothing and the run printed
// `HYGIENE OK`. SPEC-TOOLWORK §5 rule 3: "A kind the table does not hold is refused by
// `cut` and abstained by `accept`; there is no default kind." A clean answer about a
// shape of work that does not exist is the #1805 failure again, one flag along.
func TestHygieneRefusesAKindTheToolDoesNotDeclare(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "sign/sign.go", "package sign\n\nfunc F() {}\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "clean")
	// The two the tools12 cards actually carried, and one nobody could mistake for real.
	for _, kind := range []string{"fix-with-red-test", "docs-fix", "not-a-kind-at-all"} {
		t.Run(kind, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD",
				"--identity", "Rowan <rowan@example.com>", "--kind", kind}, &out, &errb)
			if code != 2 {
				t.Fatalf("--kind %q: exit %d, want 2\nstdout:%s\nstderr:%s", kind, code, out.String(), errb.String())
			}
			if !strings.Contains(errb.String(), kind) {
				t.Errorf("--kind %q: the refusal does not name the kind: %q", kind, errb.String())
			}
			// A refusal a reader can act on names the kinds there are.
			if !strings.Contains(errb.String(), "fix-red") {
				t.Errorf("--kind %q: the refusal does not list the kinds the tool declares: %q", kind, errb.String())
			}
			if out.String() != "" {
				t.Errorf("--kind %q: a refusal must print nothing on stdout, got %q", kind, out.String())
			}
		})
	}
}

// The other half: every kind the tool DOES declare is accepted, and the one the stray
// list's second column names still unlocks its exception. A guard that refused
// everything would pass the test above and break the verb.
func TestHygieneAcceptsEveryDeclaredKind(t *testing.T) {
	dir := hygLab(t)
	hygWrite(t, dir, "sign/sign.go", "package sign\n\nfunc F() {}\n")
	hygGit(t, dir, "add", "-A")
	hygGit(t, dir, "commit", "-q", "-m", "clean")
	kinds := hygiene.Kinds()
	if len(kinds) == 0 {
		t.Fatal("the tool declares no kinds at all")
	}
	for _, kind := range kinds {
		var out, errb bytes.Buffer
		if code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD",
			"--identity", "Rowan <rowan@example.com>", "--kind", kind}, &out, &errb); code != 0 {
			t.Errorf("--kind %q: exit %d, want 0\nstdout:%s\nstderr:%s", kind, code, out.String(), errb.String())
		}
	}
	// No --kind at all stays what it was: the flag is optional, and only a kind that
	// was GIVEN and is not declared is a refusal.
	var out, errb bytes.Buffer
	if code := run([]string{"hygiene", "--repo", dir, "--base", "main", "--head", "HEAD",
		"--identity", "Rowan <rowan@example.com>"}, &out, &errb); code != 0 {
		t.Fatalf("no --kind: exit %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
}

// The stray list's second column names kinds, and until now nothing checked that they
// were kinds at all. A typo there silently grants an exception to nobody.
func TestEveryKindTheStrayListNamesIsDeclared(t *testing.T) {
	for _, kind := range hygiene.StrayKinds() {
		if !hygiene.KindDeclared(kind) {
			t.Errorf("the stray list excuses a file for kind %q, which the tool does not declare: the exception is granted to nobody", kind)
		}
	}
}
