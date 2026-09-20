package hygiene

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The four checks, each seen red on the defect it exists for and green on a range that
// does not carry it. SPEC-TOOLWORK.md §3 (PR #1637), issue #1647.

func git(t *testing.T, dir string, args ...string) string {
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

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// lab is a repo with one base commit. The card's identity is Rowan, and its declared
// paths are the sign package.
func lab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "go.mod", "module fixture\n\ngo 1.26\n")
	write(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int { return 1 }\n")
	write(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {}\n")
	write(t, dir, "other/other.go", "package other\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	return dir
}

func rowan() []Identity { return []Identity{{Name: "Rowan", Email: "rowan@example.com"}} }

func check(t *testing.T, dir string, o Options) []Finding {
	t.Helper()
	if o.Repo == "" {
		o.Repo = dir
	}
	if o.Base == "" {
		o.Base = "main"
	}
	if o.Head == "" {
		o.Head = "HEAD"
	}
	if o.Identities == nil {
		o.Identities = rowan()
	}
	if o.Paths == nil {
		o.Paths = []string{"sign/**"}
	}
	fs, err := Check(context.Background(), o)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return fs
}

func tokens(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Token)
	}
	return out
}

func has(fs []Finding, token string) *Finding {
	for i := range fs {
		if fs[i].Token == token {
			return &fs[i]
		}
	}
	return nil
}

// The positive control. Without it every red below could be a check that says no to
// everything, which is the cheapest way to pass a suite and proves nothing at all.
func TestHygienePassesACleanRange(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn 1\n}\n")
	write(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {}\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "fix")
	if fs := check(t, dir, Options{}); len(fs) != 0 {
		t.Fatalf("a clean range drew findings: %v", fs)
	}
}

// hygiene-rejects-a-foreign-committer: nothing anywhere says whose name a card's commit
// carries, so it carries whatever the bench's git config held.
func TestHygieneRejectsAForeignCommitter(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int { return 0 }\n")
	git(t, dir, "add", "-A")
	cmd := exec.Command("git", "commit", "-q", "-m", "somebody else")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Bench", "GIT_COMMITTER_EMAIL=bench@elsewhere.example",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	f := has(check(t, dir, Options{}), "identity")
	if f == nil {
		t.Fatalf("a foreign COMMITTER drew no identity finding: %v", tokens(check(t, dir, Options{})))
	}
	if len(f.At) != 12 {
		t.Fatalf("at=%q, want a sha12", f.At)
	}
}

func TestHygieneRejectsAForeignAuthor(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int { return 0 }\n")
	git(t, dir, "add", "-A")
	cmd := exec.Command("git", "commit", "-q", "-m", "somebody else")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Stranger", "GIT_AUTHOR_EMAIL=stranger@elsewhere.example",
		"GIT_COMMITTER_NAME=Rowan", "GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	if has(check(t, dir, Options{}), "identity") == nil {
		t.Fatal("a foreign AUTHOR drew no identity finding")
	}
}

// hygiene-rejects-a-merge-commit.
func TestHygieneRejectsAMergeCommit(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "side")
	write(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int { return 2 }\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "side")
	git(t, dir, "checkout", "-q", "main")
	write(t, dir, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {}\n\nfunc TestOther(t *testing.T) {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "main moves")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-q", "-b", "card")
	git(t, dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")
	f := has(check(t, dir, Options{Base: base}), "identity")
	if f == nil {
		t.Fatal("a merge commit drew no identity finding")
	}
	if !strings.Contains(f.Why, "merge") {
		t.Fatalf("why=%q, want it to name the merge", f.Why)
	}
}

// hygiene-counts-a-rename-on-both-sides: a rename that moves a file OUT of the declared
// paths is out-of-path on the side that left, not only on the side that arrived.
func TestHygieneCountsARenameOnBothSides(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	git(t, dir, "mv", "sign/sign.go", "other/sign.go")
	git(t, dir, "commit", "-q", "-m", "move it out")
	f := has(check(t, dir, Options{}), "out-of-path")
	if f == nil {
		t.Fatal("a rename out of the declared paths drew no out-of-path finding")
	}
	if f.At != "other/sign.go" {
		t.Fatalf("at=%q, want the arriving side named", f.At)
	}
	// And the reverse: a rename INTO the declared paths from outside is out-of-path
	// on the leaving side, which a --find-renames diff would hide entirely.
	dir2 := lab(t)
	git(t, dir2, "checkout", "-q", "-b", "card")
	git(t, dir2, "mv", "other/other.go", "sign/other.go")
	git(t, dir2, "commit", "-q", "-m", "move it in")
	f2 := has(check(t, dir2, Options{}), "out-of-path")
	if f2 == nil {
		t.Fatal("a rename in from outside the declared paths drew no out-of-path finding")
	}
	if f2.At != "other/other.go" {
		t.Fatalf("at=%q, want the leaving side named", f2.At)
	}
}

func TestHygieneAcceptsAChangeInsideTheDeclaredPaths(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int { return 0 }\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "inside")
	if f := has(check(t, dir, Options{}), "out-of-path"); f != nil {
		t.Fatalf("a change inside the declared paths drew %v", *f)
	}
}

// A batch member that is a friend's own branch has no card and no PATHS:, so the bound
// does not apply to it. It is SKIPPED and said to be skipped -- never silently passed,
// and never failed for not having a card.
func TestHygieneSkipsOutOfPathWhenNoPathsAreDeclared(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "other/other.go", "package other\n\nfunc F() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "a friend's own branch")
	if f := has(check(t, dir, Options{Paths: []string{}}), "out-of-path"); f != nil {
		t.Fatalf("an unbounded member drew %v", *f)
	}
}

// hygiene-rejects-result-md-in-the-diff: the worker's own report is not part of its
// change.
func TestHygieneRejectsResultMDInTheDiff(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/RESULT.md", "line 1\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "ship the report")
	f := has(check(t, dir, Options{}), "stray-file")
	if f == nil {
		t.Fatalf("RESULT.md drew no stray-file finding: %v", tokens(check(t, dir, Options{})))
	}
	if f.At != "sign/RESULT.md" {
		t.Fatalf("at=%q, want the path", f.At)
	}
}

func TestHygieneRejectsTheRestOfTheStrayList(t *testing.T) {
	for _, rel := range []string{"sign/run.log", "sign/sign.go.orig", "sign/sign.go.rej", "sign/sign.test", "sign/out.out", "sign/.DS_Store", "sign/PROMPT.md", "sign/.sign.go.swp", "scratch/note.txt"} {
		t.Run(rel, func(t *testing.T) {
			dir := lab(t)
			git(t, dir, "checkout", "-q", "-b", "card")
			write(t, dir, rel, "x\n")
			git(t, dir, "add", "-A")
			git(t, dir, "commit", "-q", "-m", "stray")
			paths := []string{"sign/**", "scratch/**"}
			if has(check(t, dir, Options{Paths: paths}), "stray-file") == nil {
				t.Fatalf("%s drew no stray-file finding", rel)
			}
		})
	}
}

// hygiene-rejects-a-file-over-one-mebibyte.
func TestHygieneRejectsAFileOverOneMebibyte(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/big.bin", strings.Repeat("a", 1024*1024+1))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "big")
	f := has(check(t, dir, Options{}), "stray-file")
	if f == nil {
		t.Fatal("a file over one mebibyte drew no stray-file finding")
	}
	if !strings.Contains(f.Why, "1 MiB") && !strings.Contains(f.Why, "mebibyte") {
		t.Fatalf("why=%q, want it to name the size rule", f.Why)
	}
}

// hygiene-rejects-a-symlink-and-a-submodule.
func TestHygieneRejectsASymlink(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "sign", "link")); err != nil {
		t.Skipf("this platform cannot make a symlink: %v", err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "a way out")
	f := has(check(t, dir, Options{}), "stray-file")
	if f == nil {
		t.Fatal("a symlink drew no stray-file finding")
	}
	if !strings.Contains(f.Why, "symlink") {
		t.Fatalf("why=%q, want it to name the symlink", f.Why)
	}
}

func TestHygieneRejectsASubmodule(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	// A gitlink is mode 160000 pointing at a commit; it is written into the index
	// directly here because adding a real submodule needs a network or a second repo.
	sha := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+sha+",sign/vendor")
	git(t, dir, "commit", "-q", "-m", "a submodule")
	f := has(check(t, dir, Options{}), "stray-file")
	if f == nil {
		t.Fatal("a submodule drew no stray-file finding")
	}
	if !strings.Contains(f.Why, "submodule") {
		t.Fatalf("why=%q, want it to name the submodule", f.Why)
	}
}

// hygiene-rejects-a-conflict-marker: card-16 left `<<<<<<< HEAD` in a fenced block.
func TestHygieneRejectsAConflictMarker(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/sign.go", "package sign\n\n<<<<<<< HEAD\nfunc Sign(n int) int { return 1 }\n=======\nfunc Sign(n int) int { return 0 }\n>>>>>>> side\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "left a marker")
	f := has(check(t, dir, Options{}), "stray-file")
	if f == nil {
		t.Fatalf("a conflict marker drew no stray-file finding: %v", tokens(check(t, dir, Options{})))
	}
	if !strings.Contains(f.Why, "conflict marker") {
		t.Fatalf("why=%q, want it to name the conflict marker", f.Why)
	}
}

// An allowlisted exception names the card kind it is for, and holds for that kind only.
func TestHygieneStrayExceptionHoldsForItsKindOnly(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/golden.out", "expected\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "a golden file")
	if has(check(t, dir, Options{}), "stray-file") == nil {
		t.Fatal("*.out drew no stray-file finding with no kind")
	}
	if has(check(t, dir, Options{Kind: "transcript-test"}), "stray-file") != nil {
		t.Fatal("the transcript-test exception for *.out did not hold")
	}
	if has(check(t, dir, Options{Kind: "fix-red"}), "stray-file") == nil {
		t.Fatal("the transcript-test exception leaked to fix-red")
	}
}

// fixtureKey builds a key-SHAPED string at test time, by parts, so that no valid key
// for any provider is ever written into this repository -- the spec's own rule for the
// selftest seeds, and the same rule here.
func fixtureKey() string {
	return "gh" + "p_" + strings.Repeat("A", 36)
}

// hygiene-never-prints-the-secret: the output is searched for the fixture string.
func TestHygieneRejectsAKeyShapeAndNeverPrintsIt(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	key := fixtureKey()
	write(t, dir, "sign/sign.go", "package sign\n\nconst token = \""+key+"\"\n\nfunc Sign(n int) int { return 1 }\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "oops")
	fs := check(t, dir, Options{})
	f := has(fs, "secret")
	if f == nil {
		t.Fatalf("a key shape drew no secret finding: %v", tokens(fs))
	}
	if f.At != "sign/sign.go:3" {
		t.Fatalf("at=%q, want sign/sign.go:3", f.At)
	}
	// The whole finding, every field of it, must be printable in a log.
	all := f.Token + " " + f.At + " " + f.Why + " " + f.String()
	if strings.Contains(all, key) {
		t.Fatalf("the matched text reached the finding: %q", all)
	}
	// The shape's NAME is what a person needs; it is not the key.
	if !strings.Contains(f.Why, "forge") && !strings.Contains(f.Why, "token") {
		t.Fatalf("why=%q, want it to name the shape", f.Why)
	}
}

func TestHygieneRejectsAPEMPrivateKeyHeader(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/key.pem", "-----BEGIN"+" OPENSSH PRIVATE KEY-----\nnot-a-key\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "oops")
	if has(check(t, dir, Options{}), "secret") == nil {
		t.Fatal("a PEM private-key header drew no secret finding")
	}
}

// A key shape that was ALREADY in the base is not this card's finding: the check reads
// added lines, because a range is judged by what it added.
func TestHygieneReadsAddedLinesOnly(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "go.mod", "module fixture\n\ngo 1.26\n")
	write(t, dir, "sign/sign.go", "package sign\n\nconst token = \""+fixtureKey()+"\"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base already carries it")
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/other.go", "package sign\n\nfunc F() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "an innocent change")
	if f := has(check(t, dir, Options{}), "secret"); f != nil {
		t.Fatalf("a key shape already in the base was charged to this range: %v", *f)
	}
}

// paths-line-refuses-dotdot-and-bare-doublestar.
func TestValidatePathsRefusesDotDotAndBareDoubleStar(t *testing.T) {
	for _, bad := range [][]string{
		{"../elsewhere/**"},
		{"sign/../../etc/**"},
		{"/etc/passwd"},
		{"**"},
		{"sign/**", "**"},
		{"a/**", "b/**", "c/**", "d/**", "e/**", "f/**", "g/**", "h/**", "i/**"},
		{""},
	} {
		if err := ValidatePaths(bad); err == nil {
			t.Fatalf("ValidatePaths(%v) = nil, want a refusal", bad)
		}
	}
	for _, ok := range [][]string{
		{"sign/**"},
		{"cmd/nova-ci/firstrun_test.go", "cmd/nova-ci/testdata/firstrun/**"},
		{"internal/pulse/*.go"},
	} {
		if err := ValidatePaths(ok); err != nil {
			t.Fatalf("ValidatePaths(%v) = %v, want nil", ok, err)
		}
	}
}

// A check that could not run has found nothing, and must never report clean.
func TestCheckRefusesRatherThanReportingClean(t *testing.T) {
	dir := lab(t)
	if _, err := Check(context.Background(), Options{Repo: dir, Base: "no-such-ref", Head: "HEAD", Identities: rowan()}); err == nil {
		t.Fatal("a bad base returned no error")
	}
	if _, err := Check(context.Background(), Options{Repo: t.TempDir(), Base: "main", Head: "HEAD", Identities: rowan()}); err == nil {
		t.Fatal("a directory that is not a working copy returned no error")
	}
	// An empty identity set would admit anybody.
	if _, err := Check(context.Background(), Options{Repo: dir, Base: "main", Head: "HEAD"}); err == nil {
		t.Fatal("an empty identity set returned no error")
	}
}

// hygiene-rejects-mode-100600, proved where it CAN be proved.
//
// git records exactly four modes and normalises everything else to 100644: `git
// update-index --cacheinfo 100600,<blob>,<path>` and `git mktree` both write 100644,
// measured on git 2.x here. So there is no repository fixture that carries a 100600
// entry, and the end-to-end form of this test cannot be written -- not "is hard to
// write": cannot.
//
// The rule stays, and is proved on the entry itself. A check whose green rests on
// "git would never write that" has assumed away the only case it exists for: a tree
// written by something that is not git. The whole-repository half of the same rule is
// carried by the symlink and submodule tests above, which git DOES write.
func TestModeFindingRejectsAModeGitWillNotWrite(t *testing.T) {
	f, bad := modeFinding(entry{newMode: "100600", path: "sign/private.go", status: "A"})
	if !bad {
		t.Fatal("mode 100600 was accepted")
	}
	if f.Token != "stray-file" || f.At != "sign/private.go" || !strings.Contains(f.Why, "100600") {
		t.Fatalf("finding = %+v, want a stray-file naming the mode", f)
	}
	for _, mode := range []string{"100644", "100755"} {
		if _, bad := modeFinding(entry{newMode: mode, path: "sign/sign.go", status: "A"}); bad {
			t.Fatalf("mode %s was rejected", mode)
		}
	}
	for _, mode := range []string{"120000", "160000", "100664", "040000"} {
		if _, bad := modeFinding(entry{newMode: mode, path: "sign/x", status: "A"}); !bad {
			t.Fatalf("mode %s was accepted", mode)
		}
	}
}

// ---------------------------------------------------------------------------
// The cold read of 2026-09-19 (#1717, findings 1-8 and 10).
//
// The theme of the first three is one sentence: this check reads a repository the
// SUBJECT controls. gitOut already blanks the global and system git config, because a
// bench's configuration is not evidence -- but a job clone's own `.git/config` and its
// own committed `.gitattributes` are the WORKER's, and both of them change what `git
// diff` prints. Every finding below was reproduced as HYGIENE OK findings=0 over a
// range that carries a key.

// hygiene-ignores-the-subject-repos-diff-config: `diff.noprefix` in the checked repo
// drops the `a/` and `b/` from every header, so the parser never learns a file name and
// skips every added line in the range.
func TestHygieneIgnoresTheSubjectReposDiffConfig(t *testing.T) {
	dir := lab(t)
	git(t, dir, "config", "diff.noprefix", "true")
	git(t, dir, "checkout", "-q", "-b", "card")
	key := fixtureKey()
	write(t, dir, "sign/sign.go", "package sign\n\nconst token = \""+key+"\"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "oops")
	fs := check(t, dir, Options{})
	f := has(fs, "secret")
	if f == nil {
		t.Fatalf("the subject repo's diff.noprefix hid the key: %v", tokens(fs))
	}
	if f.At != "sign/sign.go:3" {
		t.Fatalf("at=%q, want sign/sign.go:3", f.At)
	}
}

// hygiene-ignores-the-subject-repos-replace-refs: a worker who can write the job
// clone's `.git` -- the gate's worktree shares it, SPEC-TOOLWORK §1 rule 3 -- can
// `git replace <head> <base>`. That writes one ref under `refs/replace/`, never a
// commit in the range, so `out-of-path` and `stray-file` never look at it; every git
// this package then runs follows the replacement and the four checks report clean over
// a range that still carries a key.
func TestHygieneIgnoresTheSubjectReposReplaceRefs(t *testing.T) {
	dir := lab(t)
	base := git(t, dir, "rev-parse", "main")
	git(t, dir, "checkout", "-q", "-b", "card")
	key := fixtureKey()
	write(t, dir, "sign/sign.go", "package sign\n\nconst token = \""+key+"\"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "oops")
	head := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "replace", head, base)
	fs := check(t, dir, Options{})
	if has(fs, "secret") == nil {
		t.Fatalf("a replace ref in the job clone's .git hid the key: %v", tokens(fs))
	}
}

// hygiene-reads-a-diff-the-subject-repo-marked-binary: a committed `.gitattributes`
// saying `*.go -diff` makes git print "Binary files a/... and b/... differ" instead of
// the lines, so nothing reaches the shapes and `--check` has nothing to look at either.
// It is committable inside the range, and at batch there is no PATHS: line to stop it.
func TestHygieneReadsADiffTheSubjectRepoMarkedBinary(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/.gitattributes", "*.go -diff\n")
	write(t, dir, "sign/sign.go", "package sign\n\nconst token = \""+fixtureKey()+"\"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "nothing to see here")
	fs := check(t, dir, Options{})
	if has(fs, "secret") == nil {
		t.Fatalf("a `-diff` attribute in the range hid the key: %v", tokens(fs))
	}
}

// hygiene-reads-a-path-git-would-quote: one non-ASCII byte in a name and git quotes the
// whole header -- `+++ "b/sign/k\303\251y.go"` -- which the parser read as a file it
// could not name, so every added line in that file was skipped.
func TestHygieneReadsAPathGitWouldQuote(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/kéy.go", "package sign\n\nconst token = \""+fixtureKey()+"\"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "oops")
	fs := check(t, dir, Options{})
	f := has(fs, "secret")
	if f == nil {
		t.Fatalf("a quoted path hid the key: %v", tokens(fs))
	}
	if f.At != "sign/kéy.go:3" {
		t.Fatalf("at=%q, want sign/kéy.go:3", f.At)
	}
}

// hygiene-refuses-when-the-added-lines-cannot-be-read. The first repair caught this on
// `git diff --check`, whose stdout had to be read past a non-zero exit and whose error
// went missing with it. `--check` is gone (cold read 2, N1) and the rule outlived it:
// the one read that now answers both the key shapes and the conflict markers must
// refuse rather than come back empty, because an empty answer reads as a clean range.
func TestHygieneRefusesWhenTheAddedLinesCannotBeRead(t *testing.T) {
	dir := lab(t)
	head := git(t, dir, "rev-parse", "HEAD")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checkAddedLines(ctx, dir, head, head, nil); err == nil {
		t.Fatal("a cancelled context reported a range with nothing added to it")
	}

	absent := strings.Repeat("0", len(head))
	if _, err := checkAddedLines(context.Background(), dir, absent, head, nil); err == nil {
		t.Fatal("a ref git could not resolve reported a range with nothing added to it")
	}
}

// paths-line-refuses-a-glob-that-bounds-nothing: `**` was refused by spelling, so
// `**/*` and `*/**` walked straight past it and matched every file there is. The rule
// is not the spelling; it is that SOMEWHERE in the glob there is a literal character
// the path has to carry.
func TestValidatePathsRefusesAGlobThatBoundsNothing(t *testing.T) {
	for _, bad := range []string{"**", "**/", "**/*", "*/**", "*", "*/*", "**/**", "?", "*/*/**"} {
		if err := ValidatePaths([]string{bad}); err == nil {
			t.Errorf("ValidatePaths([%q]) = nil, want a refusal: it matches every file there is", bad)
		}
	}
	for _, ok := range []string{"sign/**", "*.go", "**/*.go", "sign/*", "internal/*/doc.go", "a/**/b"} {
		if err := ValidatePaths([]string{ok}); err != nil {
			t.Errorf("ValidatePaths([%q]) = %v, want nil", ok, err)
		}
	}
}

// hygiene-reads-full-blob-ids: `--raw` abbreviates the blob id, and an abbreviation is
// ambiguous sooner or later -- at which point `git cat-file -s` refuses and the WHOLE
// Check returns an error over a range that was fine. The id is read in full.
func TestHygieneReadsFullBlobIds(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/sign.go", "package sign\n\nfunc Sign(n int) int { return 0 }\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "one change")
	base := git(t, dir, "rev-parse", "main")
	head := git(t, dir, "rev-parse", "HEAD")
	entries, err := rawDiff(context.Background(), dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the raw diff read no entry")
	}
	for _, e := range entries {
		if len(e.newBlob) < len(head) {
			t.Fatalf("%s: blob id %q is abbreviated; an abbreviation is ambiguous sooner or later", e.path, e.newBlob)
		}
	}
}

// hygiene-sizes-added-files-only: §3 rule 5 is about what a card ADDED. A file that was
// already over a mebibyte at the base and that this card merely edited is not this
// card's finding, and charging it makes every later card in that repository unfixable
// by anybody.
func TestHygieneSizesAddedFilesOnly(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "go.mod", "module fixture\n\ngo 1.26\n")
	write(t, dir, "sign/big.txt", strings.Repeat("a", 1024*1024+1)+"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "the base already carries it")
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/big.txt", strings.Repeat("a", 1024*1024+1)+"\nb\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "one line into a file that was already big")
	if f := has(check(t, dir, Options{}), "stray-file"); f != nil {
		t.Fatalf("a file the card did not add was charged to it: %v", *f)
	}
}

// hygiene-refuses-a-log-row-it-cannot-read: a row that does not carry all six fields
// was skipped, and a skipped row is a commit that went through the identity check
// without being checked. There is no safe way to read half a row.
func TestHygieneRefusesALogRowItCannotRead(t *testing.T) {
	const sep = "\x1f"
	good := strings.Join([]string{"abc123", "Rowan", "rowan@example.com", "Rowan", "rowan@example.com", ""}, sep)
	if _, err := identityFindings(good, rowan()); err != nil {
		t.Fatalf("a whole row was refused: %v", err)
	}
	short := strings.Join([]string{"abc123", "Rowan", "rowan@example.com"}, sep)
	if _, err := identityFindings(short, rowan()); err == nil {
		t.Fatal("a row with three fields was read as a commit that passed")
	}
}

// hygiene-reads-a-file-git-calls-binary: one NUL byte anywhere in a file and git
// prints "Binary files … differ" instead of its lines, whatever the attributes say --
// so a key added below that byte reached nothing. This is the half `--attr-source`
// does not cover, and the reason `--text` is passed as well as it.
func TestHygieneReadsAFileGitCallsBinary(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/blob.go", "package sign\n\x00\nconst token = \""+fixtureKey()+"\"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "oops")
	fs := check(t, dir, Options{})
	f := has(fs, "secret")
	if f == nil {
		t.Fatalf("a NUL byte in the file hid the key: %v", tokens(fs))
	}
	if f.At != "sign/blob.go:3" {
		t.Fatalf("at=%q, want sign/blob.go:3", f.At)
	}
}

// hygiene-reads-a-path-git-must-quote: `core.quotePath` governs NON-ASCII names only.
// A name holding a double quote is quoted whatever that setting says, so the header
// has to be unquoted rather than merely kept unquoted.
func TestHygieneReadsAPathGitMustQuote(t *testing.T) {
	name := "sign/k\"y.go"
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("package sign\n\nconst token = \""+fixtureKey()+"\"\n"), 0o644); err != nil {
		t.Skipf("this platform cannot hold a quote in a file name: %v", err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "oops")
	fs := check(t, dir, Options{})
	f := has(fs, "secret")
	if f == nil {
		t.Fatalf("a name git had to quote hid the key: %v", tokens(fs))
	}
	if f.At != name+":3" {
		t.Fatalf("at=%q, want %q", f.At, name+":3")
	}
}

// ---------------------------------------------------------------------------
// Cold read 2 of 2026-09-19 (#1717, N1).

// hygiene-checks-markers-whatever-the-attributes-say.
//
// The first repair answered the COMMITTED `.gitattributes` and stopped there. The same
// `-diff` attribute reaches git from two more places, and `--attr-source` reads neither:
// `.git/info/attributes` and a local `core.attributesFile`. The gate's worktree shares
// the job clone's `.git` (§1 rule 3), so both of them are the worker's to write, and on
// a git older than 2.42 the committed file was open again too.
//
// Reproduced by the reader as 2 secret findings and 0 markers: the added LINES were
// read -- `--text` sees to that -- and only `git diff --check` was blind, because it
// honours the attribute whatever `--text` says. So the marker check stops asking git
// and reads the lines this package already has.
func TestHygieneChecksMarkersWhateverTheAttributesSay(t *testing.T) {
	const marked = "package sign\n\n<<<<<<< HEAD\nfunc Sign(n int) int { return 1 }\n=======\nfunc Sign(n int) int { return 0 }\n>>>>>>> side\n"
	for _, how := range []struct {
		name string
		hide func(t *testing.T, dir string)
	}{
		{"git/info/attributes", func(t *testing.T, dir string) {
			write(t, dir, ".git/info/attributes", "*.go -diff\n")
		}},
		{"core.attributesFile", func(t *testing.T, dir string) {
			p := filepath.Join(t.TempDir(), "attributes")
			if err := os.WriteFile(p, []byte("*.go -diff\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			git(t, dir, "config", "core.attributesFile", p)
		}},
		{"committed .gitattributes", func(t *testing.T, dir string) {
			write(t, dir, "sign/.gitattributes", "*.go -diff\n")
		}},
	} {
		t.Run(how.name, func(t *testing.T) {
			dir := lab(t)
			git(t, dir, "checkout", "-q", "-b", "card")
			write(t, dir, "sign/sign.go", marked)
			how.hide(t, dir)
			git(t, dir, "add", "-A")
			git(t, dir, "commit", "-q", "-m", "a marker the subject would rather git did not print")
			// All three markers, each at its own line. The line number is what a
			// person navigates by, and it used to be git's own: `--check` counted
			// it, and now this package does, off the hunk header. Pinning the
			// three says the counter did not drift when the counting moved.
			var at []string
			for _, f := range check(t, dir, Options{}) {
				if strings.Contains(f.Why, "conflict marker") {
					at = append(at, f.At)
				}
			}
			want := "sign/sign.go:3 sign/sign.go:5 sign/sign.go:7"
			if got := strings.Join(at, " "); got != want {
				t.Fatalf("markers at %q, want %q", got, want)
			}
		})
	}
}

// The other half of the same line: a marker in a file the range did not touch is not
// this range's finding, and a line that merely looks like one is not a marker. git's
// own rule is exactly seven characters followed by a space or the end of the line.
func TestHygieneReadsAMarkerAsGitSpellsIt(t *testing.T) {
	dir := lab(t)
	git(t, dir, "checkout", "-q", "-b", "card")
	write(t, dir, "sign/sign.go", "package sign\n\n// <<<<<<<< eight is a rule in a comment, not a marker\nconst bar = \"<<<<<<<\"\nfunc Sign(n int) int { return 1 }\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "lines that only look like markers")
	for _, f := range check(t, dir, Options{}) {
		if strings.Contains(f.Why, "conflict marker") {
			t.Fatalf("a line that is not a marker drew one: %v", f)
		}
	}
}
