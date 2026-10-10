package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// twinRepo is twinRemote with the pieces a test that moves the base needs: the work tree on
// the first branch and the git runner that wrote it, so a later commit can move the branch
// the brief's BASE names.
func twinRepo(t *testing.T, ta *testApp, files map[string]string, branches ...string) (remote, sha, work string, git func(in string, args ...string) string) {
	t.Helper()
	dir := t.TempDir()
	git = func(in string, args ...string) string {
		t.Helper()
		res, err := gitrun.Run(context.Background(), gitrun.Options{C: in, Env: testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1"), OwnRepo: in != ""}, args...)
		require.NoError(t, err, "git %v: %s", args, res.Stderr)
		return strings.TrimSpace(string(res.Stdout))
	}
	remote, work = filepath.Join(dir, "remote.git"), filepath.Join(dir, "work")
	git("", "init", "-q", "--bare", "-b", branches[0], remote)
	git("", "init", "-q", "-b", branches[0], work)
	for name, text := range files {
		p := filepath.Join(work, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	git(work, "add", ".")
	git(work, "commit", "-q", "-m", "base")
	for _, b := range branches {
		git(work, "push", "-q", remote, "HEAD:refs/heads/"+b)
	}
	ta.a.gitEnv = testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	ta.a.landRoot = func() (string, error) { return filepath.Join(dir, "land"), nil }
	return remote, git(work, "rev-parse", "HEAD"), work, git
}

// twinRemote is a bare repository standing in for a card's REPO:, every branch named at one
// commit holding the files given (path to text), and the app set to keep the lander's clones
// under a test directory and run git with the test identity, so add reads a brief's base with
// no network. It returns the repository and the commit.
func twinRemote(t *testing.T, ta *testApp, files map[string]string, branches ...string) (remote, sha string) {
	t.Helper()
	remote, sha, _, _ = twinRepo(t, ta, files, branches...)
	return remote, sha
}

// add runs the brief checks at the card's base (docs/SPEC-SPRINT.md section 11, the brief
// checks; swarm.LintBrief): the repository REPO: names, read in the lander's clone at the tip
// of BASE:, fetched there. A brief that fails one is refused, the one-brief and the many-brief
// form alike, exit 2, nothing written, each finding a LINT DRIFT line with its token and remedy
// and each corrected header line a LINT FIX line; the brief with that line applied is admitted.
// The base checks run here too (a TEST that exists at the base, a base origin does not hold).
func TestAddRunsTheBriefChecksAtTheBase(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	remote, _ := twinRemote(t, ta, map[string]string{
		"internal/x/x.go":            "package x\n",
		"internal/x/x_test.go":       "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n",
		"internal/x/testdata/in.txt": "in\n",
	}, "sprint/s1")
	brief := func(base, paths, test string) string {
		return passingBrief("RESULT: c sha=0123456789ab tier: pro\nREPO: " + remote + "\nBASE: " + base + "\nPATHS: " + paths + "\nTEST: ./internal/x " + test + "\n\nTHE TASK. Fix internal/x/x.go.")
	}
	write := func(dir, id, text string) string {
		t.Helper()
		p := filepath.Join(dir, id+".md")
		require.NoError(t, os.WriteFile(p, []byte(text), 0o600))
		return p
	}

	bad := write(t.TempDir(), "bad", brief("sprint/s1", "internal/x/*.go", "TestOld"))
	before := ta.applies()
	code, out, errs := ta.do("add --stream s1 bad --one --brief-file " + bad)
	assert.Equal(t, 2, code, "%s%s", out, errs)
	assert.Contains(t, errs, "LINT DRIFT card=bad check=paths-cover-testdata line=4: ")
	assert.Contains(t, errs, "remedy=a `<dir>/*.go` entry does not reach `<dir>/testdata`")
	assert.Contains(t, errs, "LINT DRIFT card=bad check=donewhen-test-name line=5: TEST TestOld exists in ./internal/x at ")
	assert.Contains(t, errs, "LINT FIX card=bad PATHS: internal/x/*.go,internal/x/testdata/**\n")
	assert.Contains(t, errs, "nova-sprint add REFUSED: 2 brief finding(s) at the base, the first donewhen-test-name")
	assert.False(t, ta.placed("bad"), "nothing written")
	assert.Equal(t, before, ta.applies(), "no store write")

	gone := write(t.TempDir(), "gone", brief("sprint/gone", "internal/x/*.go,internal/x/testdata/**", "TestNew"))
	code, out, errs = ta.do("add --stream s1 gone --one --brief-file " + gone)
	assert.Equal(t, 2, code, "%s%s", out, errs)
	assert.Contains(t, errs, "check=base-is-live line=3: BASE sprint/gone is no branch of origin "+remote+": deleted, or never pushed")
	assert.Contains(t, errs, "check=paths-at-base line=4: MISSING: origin "+remote+" holds no branch sprint/gone")
	assert.False(t, ta.placed("gone"), "nothing written")

	many := t.TempDir()
	write(many, "m1", brief("sprint/s1", "internal/x/*.go,internal/x/testdata/**", "TestNew"))
	write(many, "m2", brief("sprint/s1", "internal/x/*.go", "TestNew"))
	// --no-fix keeps today's refusal: one red brief refuses the whole call
	code, out, errs = ta.do("add --stream s2 --allow-shared-paths --no-fix --brief-dir " + many)
	assert.Equal(t, 2, code, "%s%s", out, errs)
	assert.Contains(t, errs, "LINT FIX card=m2 PATHS: internal/x/*.go,internal/x/testdata/**\n")
	assert.NotContains(t, errs, "card=m1 ", "m1 passes")
	assert.False(t, ta.placed("m1") || ta.placed("m2"), "one red brief refuses the whole call")

	// without --no-fix add applies the lint's own fix line to the brief it stores, says
	// LINT APPLIED, and admits the card
	code, out, errs = ta.do("add --stream s2 --allow-shared-paths --brief-dir " + many)
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, errs, "LINT APPLIED card=m2 PATHS: internal/x/*.go,internal/x/testdata/**\n")
	assert.Contains(t, out, "MOVED m1 -> ready")
	assert.Contains(t, out, "MOVED m2 -> ready")
	assert.True(t, ta.placed("m1") && ta.placed("m2"), "both cards are admitted")

	// the corrected PATHS line is what the card stores
	assert.Contains(t, ta.ok("card m2 --brief"), "PATHS: internal/x/*.go,internal/x/testdata/**")

	// the one-brief form stores the fixed brief too: add prints LINT APPLIED and admits
	// the card on the corrected PATHS, not on the file's drifted line
	one := write(t.TempDir(), "one", brief("sprint/s1", "internal/x/*.go", "TestNew"))
	code, out, errs = ta.do("add --stream s3 one --one --brief-file " + one)
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, errs, "LINT APPLIED card=one PATHS: internal/x/*.go,internal/x/testdata/**\n")
	assert.Contains(t, out, "MOVED one -> ready")
	assert.True(t, ta.placed("one"), "the card is admitted")
	assert.Contains(t, ta.ok("card one --brief"), "PATHS: internal/x/*.go,internal/x/testdata/**")
}

// The deal-time lint reads the base tip of each tick, not the tip of the tick it first saw:
// the closure a run makes once keeps its evidence and its answers for the tick its snapshot's
// clock belongs to, so a brief that passed at one tip is re-read at the next and parked once
// its PATHS name nothing there. A cache that lived as long as the closure served the stale
// pass (docs/SPEC-SPRINT.md section 11, the brief checks).
func TestTheDealTimeLintReadsTheBaseOfEachTick(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	remote, _, work, git := twinRepo(t, ta, map[string]string{
		"internal/x/x.go": "package x\n",
	}, "sprint/s1")
	brief := passingBrief("RESULT: c1 sha=0123456789ab tier: pro\nREPO: " + remote + "\nBASE: sprint/s1\nPATHS: internal/x/*.go\nTEST: ./internal/x TestNew\n\nTHE TASK. Fix internal/x/x.go.")
	card := &sprint.Card{ID: "c1", Row: "s1", Col: "ready", Fields: map[string]string{"brief": brief}}
	drift := ta.a.briefAtTheBase()
	require.Empty(t, drift(&sprint.Snapshot{Now: time.Unix(1, 0)}, card), "the brief passes at the first tick's base")

	// the base moves: the file the brief's PATHS names is gone
	require.NoError(t, os.Remove(filepath.Join(work, "internal", "x", "x.go")))
	git(work, "add", "-A")
	git(work, "commit", "-q", "-m", "drop x")
	git(work, "push", "-q", remote, "HEAD:refs/heads/sprint/s1")

	second := drift(&sprint.Snapshot{Now: time.Unix(2, 0)}, card)
	assert.Contains(t, second, "BRIEF DRIFT check=paths-at-base", "a new tick reads the base tip again")
}

// brief --fix applies the brief checks' own corrected header lines to the card's stored
// brief in place, and lint prints the same lines on demand: a brief that drifted from its
// base is corrected by the lint, not by the seat (docs/SPEC-SPRINT.md section 11, the brief
// checks).
func TestLintAndBriefFixApplyTheLintLines(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	remote, _ := twinRemote(t, ta, map[string]string{
		"internal/x/x.go":            "package x\n",
		"internal/x/x_test.go":       "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n",
		"internal/x/testdata/in.txt": "in\n",
	}, "sprint/s1")
	brief := func(paths string) string {
		return passingBrief("RESULT: c sha=0123456789ab tier: pro\nREPO: " + remote + "\nBASE: sprint/s1\nPATHS: " + paths + "\nTEST: ./internal/x TestNew\n\nTHE TASK. Fix internal/x/x.go.")
	}
	write := func(name, text string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), name+".md")
		require.NoError(t, os.WriteFile(p, []byte(text), 0o600))
		return p
	}
	good := write("c1", brief("internal/x/*.go,internal/x/testdata/**"))
	require.Contains(t, ta.ok("add --stream s1 c1 --one --brief-file "+good), "MOVED c1 -> ready")

	// brief replaces the stored brief and runs the admission check, not the base lint: the
	// drifted brief is admitted and the deal-time lint is what catches it
	drifted := write("c1-drift", brief("internal/x/*.go"))
	ta.ok("brief c1 --brief-file " + drifted)

	code, out, errs := ta.do("lint c1")
	require.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, out, "LINT DRIFT card=c1 check=paths-cover-testdata line=4: ")
	assert.Contains(t, out, "LINT FIX card=c1 PATHS: internal/x/*.go,internal/x/testdata/**")

	// brief --fix applies the lint's own corrected line to the stored brief
	code, out, errs = ta.do("brief c1 --fix")
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, errs, "LINT APPLIED card=c1 PATHS: internal/x/*.go,internal/x/testdata/**")
	assert.Contains(t, ta.ok("card c1 --brief"), "PATHS: internal/x/*.go,internal/x/testdata/**")

	code, out, errs = ta.do("lint c1")
	require.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, out, "LINT OK")
}
