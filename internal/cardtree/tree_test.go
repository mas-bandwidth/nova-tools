package cardtree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fenced turns the ”' of the raw strings below into the program fence.
func fenced(s string) string { return strings.ReplaceAll(s, "'''", "```") }

const flatCard = `RESULT: flat-1 sha=0123456789ab
REPO: o/r
BASE: dev
Deadline: finish within 30 minutes.

THE TASK. Fix internal/x/x.go.

STEP 1. Enter your worktree with cd repo && git log --oneline -1.
STEP 2. Write the red test TestX in internal/x/x_test.go.
  verdict: the test is red first
STEP 3. Run go test -count=1 ./internal/x/ and commit.
STEP 4. End as JOB.md says; write RESULT.md.
`

const fixture = "row one\nrow two\n"

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// scriptCard is two script steps under a parent: a Go program that upper-cases the generated
// file (its POST the sha256 post) and a regex over b.txt.
func scriptCard(post string) string {
	return fenced(fmt.Sprintf(`RESULT: s1-4 sha=0123456789ab
REPO: o/r
BASE: dev@%s
PATHS: b.txt, gen/table.txt
Deadline: finish within 30 minutes.

STEP 1. Enter your worktree with cd repo && git log --oneline -1.
STEP 2. Regenerate.
STEP 2.1. Upper-case the generated file.
  PATHS: gen/table.txt
  COMMIT: gen: upper-case the table
  VERDICT: the post-condition holds
  SCRIPT: go
  '''go
  package main
  func main() {}
  '''
  POST: sha256 gen/table.txt %s
STEP 2.2. Rename in b.txt.
  PATHS: b.txt
  COMMIT: b: Foo to Bar
  VERDICT: the vet is clean
  SCRIPT: regex
  '''
  s/Foo/Bar/
  '''
  POST: exit0 go vet ./...
STEP 3. End as JOB.md says; write RESULT.md.
`, strings.Repeat("e", 40), post))
}

// ledger is an in-memory commit log: each commit its message and paths, its sha c<n>.
type ledger struct{ commits []string }

func (l *ledger) commit(_ string, paths []string, message string) (string, error) {
	l.commits = append(l.commits, message+" "+strings.Join(paths, ","))
	return fmt.Sprintf("c%d", len(l.commits)), nil
}

// machine is a Sys whose toolchain and wall are recorded, never run: the built program is the
// upper-casing the card's program stands for, applied by the test.
type machine struct {
	ledger
	builds, runs []string
	failRun      string // a run whose first word is this fails
}

func (m *machine) sys(t *testing.T, work string) Sys {
	return Sys{
		Work:   work,
		Commit: m.commit,
		Build: func(dir string, argv ...string) error {
			m.builds = append(m.builds, dir+": "+strings.Join(argv, " "))
			return nil
		},
		Run: func(dir string, argv ...string) error {
			m.runs = append(m.runs, strings.Join(argv, " "))
			if argv[0] == m.failRun {
				return errors.New("exit status 1")
			}
			if filepath.Base(argv[0]) == "step" {
				b, err := os.ReadFile(filepath.Join(dir, "gen", "table.txt"))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "gen", "table.txt"), bytes.ToUpper(b), 0o644))
			}
			return nil
		},
	}
}

func checkout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gen"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gen", "table.txt"), []byte(fixture), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("Foo\n"), 0o644))
	return dir
}

func TestAFlatCardLintsAndRunsAsBefore(t *testing.T) {
	t.Parallel()
	tr := Parse(flatCard)
	assert.False(t, tr.IsTree(), "no dotted step and no upper-case step field: an indented `verdict:` is prose")
	assert.Empty(t, tr.Work())
	assert.Nil(t, Lint(flatCard), "a flat card has no tree finding")
	assert.Empty(t, Guide(tr), "a flat card's child is told nothing new")
}

func TestAScriptCardParsesItsStepsDepthFirst(t *testing.T) {
	t.Parallel()
	tr := Parse(scriptCard(sum(strings.ToUpper(fixture))))
	require.True(t, tr.IsTree())
	var nums []string
	for _, s := range tr.Work() {
		nums = append(nums, s.Num)
	}
	assert.Equal(t, []string{"2.1", "2.2"}, nums, "the work steps, depth first in card order")
	s, ok := tr.Step("2.1")
	require.True(t, ok)
	assert.Equal(t, "package main\nfunc main() {}\n", s.Program, "the program is the fenced block, dedented")
	assert.True(t, tr.AllScript())
	assert.Nil(t, Lint(scriptCard(sum(fixture))), "the script card lints clean")
}

func TestAScriptCardRunsByTheMemberWithNoModel(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, post string
		ok         bool
	}{
		{"the hash holds", sum(strings.ToUpper(fixture)), true},
		{"the hash differs", sum(fixture), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, work, m := checkout(t), t.TempDir(), &machine{}
			got := Walk(Parse(scriptCard(c.post)), func(s Step) Result { return RunScript(dir, s, m.sys(t, work)) })
			bin := filepath.Join(work, "step-2.1", "step")
			assert.Equal(t, []string{filepath.Join(work, "step-2.1") + ": go build -o " + bin + " main.go"}, m.builds, "the toolchain builds the program where it is written")
			b, err := os.ReadFile(filepath.Join(dir, "gen", "table.txt"))
			require.NoError(t, err)
			assert.Equal(t, strings.ToUpper(fixture), string(b), "the program ran in the checkout")
			if !c.ok {
				require.Len(t, got, 1, "the walk stops at the broken step")
				assert.Equal(t, Broken, got[0].Verdict)
				assert.Contains(t, got[0].Words, "sha256 gen/table.txt is "+sum(strings.ToUpper(fixture)))
				assert.Empty(t, m.commits, "a broken step commits nothing")
				return
			}
			require.Len(t, got, 2)
			assert.Equal(t, []string{bin, "go vet ./..."}, m.runs, "the built program, then the exit0 POST, each through Run (the step's wall)")
			assert.Equal(t, []string{"gen: upper-case the table gen/table.txt", "b: Foo to Bar b.txt"}, m.commits, "one commit per step")
			assert.Equal(t, Result{Num: "2.1", Verdict: OK, Sha: "c1", Words: "post holds"}, got[0])
			b, err = os.ReadFile(filepath.Join(dir, "b.txt"))
			require.NoError(t, err)
			assert.Equal(t, "Bar\n", string(b), "the regex ran in process")
		})
	}
}

func TestAnExit0PostRunsAndAFailingOneIsBroken(t *testing.T) {
	t.Parallel()
	dir, m := checkout(t), &machine{failRun: "go"}
	s := Step{Num: "2", Paths: []string{"b.txt"}, Commit: "c", Lang: "regex", Program: "s/Foo/Bar/\n",
		Post: []Post{{Kind: "exit0", Argv: []string{"go", "vet", "./..."}}}}
	r := RunScript(dir, s, m.sys(t, t.TempDir()))
	assert.Equal(t, Broken, r.Verdict)
	assert.Equal(t, []string{"go vet ./..."}, m.runs, "the exit0 command ran")
	assert.Contains(t, r.Words, "post: exit0 go vet ./...: exit status 1")
	assert.Empty(t, m.commits)
}

func TestAScriptStepRefusesAnInterpreterAndAPathOutsideTheCheckout(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		paths []string
		post  Post
		words string
	}{
		{"exit0 bash", []string{"b.txt"}, Post{Kind: "exit0", Argv: []string{"bash", "-c", "true"}}, "never bash or python"},
		{"exit0 /bin/sh", []string{"b.txt"}, Post{Kind: "exit0", Argv: []string{"/bin/sh", "-c", "true"}}, "never bash or python"},
		{"exit0 python3", []string{"b.txt"}, Post{Kind: "exit0", Argv: []string{"python3", "x.py"}}, "never bash or python"},
		{"exit0 env", []string{"b.txt"}, Post{Kind: "exit0", Argv: []string{"env", "bash"}}, "never bash or python"},
		{"a climbing glob", []string{"../b.txt"}, Post{Kind: "exit0", Argv: []string{"go", "vet"}}, "../b.txt is not a relative path inside the checkout"},
		{"an absolute glob", []string{"/etc/hosts"}, Post{Kind: "exit0", Argv: []string{"go", "vet"}}, "/etc/hosts is not a relative path inside the checkout"},
		{"a climbing hash path", []string{"b.txt"}, Post{Kind: "sha256", Path: "../x", Sum: sum("")}, "sha256 ../x is not a relative path"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, m := checkout(t), &machine{}
			r := RunScript(dir, Step{Num: "2", Paths: c.paths, Commit: "c", Lang: "regex", Program: "s/Foo/Bar/\n", Post: []Post{c.post}}, m.sys(t, t.TempDir()))
			assert.Equal(t, Broken, r.Verdict)
			assert.Contains(t, r.Words, c.words)
			assert.Empty(t, m.runs, "nothing ran")
			assert.Empty(t, m.commits, "nothing committed")
		})
	}
}

func TestARegexStepNeverFollowsALinkOutOfTheCheckout(t *testing.T) {
	t.Parallel()
	dir, outside := t.TempDir(), filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("Foo\n"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "b.txt")))
	r := RunScript(dir, Step{Num: "2", Paths: []string{"b.txt"}, Commit: "c", Lang: "regex", Program: "s/Foo/Bar/\n"}, (&machine{}).sys(t, t.TempDir()))
	assert.Equal(t, Broken, r.Verdict)
	b, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "Foo\n", string(b), "the file the link names is untouched")
}

func TestTheStepWallDeniesTheNetworkAndCarriesNoCredential(t *testing.T) {
	t.Parallel()
	w := Wall{Bin: "/w/nova-sandbox", Read: []string{"--read", "/bin-dir"}, Tmp: "/t"}
	assert.Equal(t, []string{"--write", "/t", "--write", "/co", "--tmp", "/t", "--cwd", "/co", "--net-deny", "--read", "/bin-dir", "--", "go", "vet"},
		w.Argv("/co", []string{"go", "vet"}), "the writes are the private temp and the checkout only")
	env := w.Env([]string{"PATH=/bin", "OPENAI_API_KEY=k", "GH_TOKEN=t", "NOVA_SECRET=s", "SSH_AUTH_SOCK=a", "HOME=/u", "GIT_AUTHOR_NAME=n", "MY_PASSWORD=p",
		"REDIS_URL=redis://:pw@10.0.0.1:6379", "MIRROR=https://user:pw@example.com/r.git", "PLAIN_URL=https://example.com/r.git",
		"GOCACHE=/shared/go-build", "GOFLAGS=-toolexec=/x", "GOPROXY=https://example.com/proxy", "GOMODCACHE=/shared/mod"})
	assert.Equal(t, []string{"PATH=/bin", "GIT_AUTHOR_NAME=n", "PLAIN_URL=https://example.com/r.git", "GOMODCACHE=/shared/mod",
		"HOME=/t/home", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GOCACHE=/t/go-build", "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOTMPDIR=/t", "TMPDIR=/t"}, env,
		"no credential by name or in a URL's user:password@, and Go's build cache a private one")
}

// A temp directory the caller names outside the wall is not the step's: the CI runners set
// GOTMPDIR to a directory of their own, and go vet in the step's wall failed "go: creating
// work dir: mkdir <runner>/_cache/go-tmp/...: permission denied" (merge queue run 37094279566).
// The step's temps are its private one.
func TestTheStepWallKeepsGoAndTheShellInItsPrivateTemp(t *testing.T) {
	t.Parallel()
	w := Wall{Bin: "/w/nova-sandbox", Tmp: "/t"}
	env := w.Env([]string{"PATH=/bin", "GOTMPDIR=/runner/_cache/go-tmp", "TMPDIR=/runner/tmp"})
	assert.Equal(t, []string{"PATH=/bin", "HOME=/t/home", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GOCACHE=/t/go-build", "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOTMPDIR=/t", "TMPDIR=/t"}, env)
}

func TestAFailedStepTwoOfThreeLandsStepOneAndWritesTheRemainder(t *testing.T) {
	t.Parallel()
	card := `RESULT: s1-4 sha=0123456789ab
REPO: o/r
BASE: dev@` + strings.Repeat("e", 40) + `
Needs: s1-1

STEP 1. Enter your worktree with cd repo.
STEP 2. One.
  PATHS: a.go
  COMMIT: one
  VERDICT: ok
STEP 3. Two.
STEP 3.1. Two, first half.
  PATHS: b.go
  COMMIT: two
  VERDICT: ok
STEP 4. Three.
  PATHS: c.go
  COMMIT: three
  VERDICT: ok
STEP 5. End as JOB.md says; write RESULT.md.
`
	tr := Parse(card)
	sha, land := strings.Repeat("a", 40), strings.Repeat("b", 40)
	failed, at := Land(tr, ParseVerdicts("- step 2: ok "+sha+" one done\nstep 3.1: broken - TestB is red\n"))
	require.NotNil(t, failed)
	assert.Equal(t, Result{Num: "3.1", Verdict: Broken, Words: "TestB is red"}, *failed)
	assert.Equal(t, sha, at, "steps before the failed one land at the last ok step's commit")

	assert.Equal(t, "s1-4-r3-2", RemainderID("s1-4", "3.2"), "a dotted step's id is one the sprint takes")
	rem, err := Remainder(card, "s1-4", "3.1", land)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(rem, "RESULT: s1-4 sha="+land[:12]+"\nFrom: STEP 3.1\nREPO: o/r\nBASE: dev@"+land+"\nNeeds: s1-1, s1-4\n"), rem)
	rt := Parse(rem)
	var nums []string
	for _, s := range rt.Work() {
		nums = append(nums, s.Num)
	}
	assert.Equal(t, []string{"3.1", "4"}, nums, "the remainder walks from the failed step")
	assert.Nil(t, Lint(rem))
	assert.Contains(t, Guide(rt), "from STEP 3.1")
	_, err = Remainder(card, "s1-4", "3.1", "renamed")
	require.Error(t, err, "a remainder is staged at a full sha only")
	flat, err := Remainder("RESULT: x sha=0123456789ab\n\nSTEP 1.", "x", "2", land)
	require.NoError(t, err)
	assert.Equal(t, "RESULT: x sha="+land[:12]+"\nFrom: STEP 2\nNeeds: x\nbase-sha: "+land+"\n\nSTEP 1.", flat, "a card naming no base gains a base-sha: line")

	none, _ := Land(tr, ParseVerdicts("step 2: ok "+sha+"\nstep 3.1: ok -\nstep 4: ok "+sha[:12]))
	assert.Nil(t, none, "every step ok: nothing failed")
	first, at := Land(tr, ParseVerdicts("step 2: not-done -"))
	require.NotNil(t, first)
	assert.Equal(t, "2", first.Num)
	assert.Empty(t, at, "a failed first step lands nothing")
}

func TestAStepLineWhoseCommitIsAWordIsADefect(t *testing.T) {
	t.Parallel()
	v := ParseVerdicts("step 2: ok renamed Foo\nstep 3: broken - red\n")
	assert.Equal(t, NotDone, v["2"].Verdict)
	assert.Empty(t, v["2"].Sha, "no word is ever a head")
	assert.Contains(t, v["2"].Words, "renamed is no sha")
	tr := Parse("RESULT: x\nSTEP 2. a\n  PATHS: a\n  COMMIT: a\n  VERDICT: a\nSTEP 3. b\n  PATHS: b\n  COMMIT: b\n  VERDICT: b\n")
	failed, land := Land(tr, v)
	require.NotNil(t, failed)
	assert.Equal(t, "2", failed.Num, "the defect line is the failed step")
	assert.Empty(t, land)
}

func TestTheTreeLintNamesEachDefect(t *testing.T) {
	t.Parallel()
	head := "RESULT: t sha=0123456789ab\nPATHS: a.go\n\nSTEP 1. Enter with cd repo.\n"
	script := "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n  VERDICT: v\n"
	for _, c := range []struct{ name, steps, check, excerpt string }{
		{"a child with no parent", "STEP 2.1. x\n", CheckNested, "no STEP 2 above it"},
		{"a gap among children", "STEP 2. x\nSTEP 2.1. y\nSTEP 2.3. z\n", CheckNested, "after 2.1"},
		{"a work step with no verdict", "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n", CheckStep, "has no VERDICT:"},
		{"a glob outside the card's PATHS", "STEP 2. x\n  PATHS: b.go\n  COMMIT: c\n  VERDICT: v\n", CheckStep, "b.go is not in the card's PATHS:"},
		{"a climbing glob", "STEP 2. x\n  PATHS: ../a.go\n  COMMIT: c\n  VERDICT: v\n", CheckStep, "../a.go is not a relative path inside the checkout"},
		{"an absolute glob", "STEP 2. x\n  PATHS: /etc/a.go\n  COMMIT: c\n  VERDICT: v\n", CheckStep, "/etc/a.go is not a relative path inside the checkout"},
		{"fields with no commit", "STEP 2. x\n  PATHS: a.go\n", CheckStep, "carries PATHS: and no COMMIT:"},
		{"a bash script", script + "  SCRIPT: bash\n", CheckScript, "never bash or python"},
		{"an sh script", script + "  SCRIPT: sh\n", CheckScript, "never bash or python"},
		{"a python script", script + "  SCRIPT: python\n", CheckScript, "never bash or python"},
		{"no program", script + "  SCRIPT: go\n  POST: exit0 go vet ./...\n", CheckScript, "no fenced program"},
		{"no post", script + "  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n", CheckScript, "no POST:"},
		{"a bad post", script + "  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n  POST: sha256 a.go nothex\n", CheckScript, "is neither"},
		{"a climbing hash path", script + "  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n  POST: sha256 ../a.go " + sum("") + "\n", CheckScript, "../a.go is not a relative path"},
		{"exit0 bash", script + "  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n  POST: exit0 bash -c true\n", CheckScript, "exit0 bash: never bash or python"},
		{"exit0 sh", script + "  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n  POST: exit0 sh -c true\n", CheckScript, "exit0 sh: never bash or python"},
		{"exit0 python", script + "  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n  POST: exit0 python -c 1\n", CheckScript, "exit0 python: never bash or python"},
		{"a regex that does not compile", script + "  SCRIPT: regex\n  '''regex\n  s/(/b/\n  '''\n  POST: exit0 go vet ./...\n", CheckScript, "regex line 1"},
		{"a fence of another language", script + "  SCRIPT: go\n  '''lisp\n  (print 1)\n  '''\n  POST: exit0 go vet ./...\n", CheckScript, "fence says lisp"},
		{"a script step beside a model step", script + "  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n  POST: exit0 go vet ./...\nSTEP 3. y\n  PATHS: a.go\n  COMMIT: d\n  VERDICT: w\n", CheckScript, "a script step in a card with model steps"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fs := Lint(fenced(head + c.steps))
			require.Len(t, fs, 1, "%v", fs)
			assert.Equal(t, c.check, fs[0].Check)
			assert.Contains(t, fs[0].Excerpt, c.excerpt)
		})
	}
}
