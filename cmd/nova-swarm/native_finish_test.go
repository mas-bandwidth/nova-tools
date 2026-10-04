package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeCompletesStepCommitsWithoutChangingTheRawResult(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	repo := filepath.Join(job, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	runGit(t, repo, "init", "-b", "owned")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "actual work")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	raw := "head: " + head[:9] + "\nbranch: guessed\nverdict: ok\ngate: -\noutput: -\nreport: measured\n\n## Body\nstep 3: ok " + head[:13] + " completed\n"
	path := filepath.Join(job, "RESULT.md")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))
	require.NoError(t, completeNativeResult(job, "", head, "", nativeFinishCommands(repo, "")))
	finish, exists := cardcontract.ReadFinish(job)
	require.True(t, exists)
	assert.Equal(t, head, finish.Head)
	assert.Equal(t, "owned", finish.Branch)
	assert.Equal(t, head, cardtree.ParseVerdicts(finish.Body)["3"].Sha)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, raw, string(unchanged))
}

func TestNativeRefusesACommitOutsideTheResultHistory(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	repo := filepath.Join(job, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	runGit(t, repo, "init", "-b", "owned")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "first")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "-b", "other")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "other")
	other := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "owned")
	raw := "head: " + head + "\nbranch: owned\nverdict: ok\ngate: -\noutput: -\nreport: measured\n\n## Body\nstep 3: ok " + other[:14] + " claimed\n"
	require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(raw), 0o644))
	err := completeNativeResult(job, "", head, "", nativeFinishCommands(repo, ""))
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "outside HEAD's history"), err)
	_, exists := cardcontract.ReadFinish(job)
	assert.False(t, exists, "unknown execution gets no completed finish")
}

func TestNativeCompletionChecksTheOutputArtifact(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, output string
		wantOK       bool
	}{
		{"existing output", "gate.log", true},
		{"missing output", "missing.log", false},
		{"outside output", "outside/gate.log", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			job := t.TempDir()
			repo := filepath.Join(job, "repo")
			require.NoError(t, os.Mkdir(repo, 0o755))
			runGit(t, repo, "init", "-b", "owned")
			runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "work")
			head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
			require.NoError(t, os.WriteFile(filepath.Join(repo, "gate.log"), []byte("actual output"), 0o644))
			outside := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(outside, "gate.log"), []byte("unrelated"), 0o644))
			require.NoError(t, os.Symlink(outside, filepath.Join(repo, "outside")))
			raw := "head: " + head + "\nbranch: owned\nverdict: not-done\ngate: gate command\noutput: " + tc.output + "\nreport: actual failure\n"
			require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(raw), 0o644))
			err := completeNativeResult(job, "", head, "", nativeFinishCommands(repo, ""))
			if tc.wantOK {
				require.NoError(t, err)
				finish, exists := cardcontract.ReadFinish(job)
				require.True(t, exists)
				assert.Equal(t, "not-done", finish.Verdict)
			} else {
				require.Error(t, err)
				_, exists := cardcontract.ReadFinish(job)
				assert.False(t, exists)
			}
		})
	}
}

// fakeFinish is a finish's two commands answered from tables and no subprocess: git by its
// joined arguments, each answer consumed in order and the last repeated (a HEAD that moves
// after a commit); gofmt by one answer per flag. An argument line the table lacks is an
// error, as the real git's would be. Every call is recorded.
type fakeFinish struct {
	git    map[string][]string
	list   string // gofmt -l's answer
	gofmts [][]string
	gits   [][]string
}

func (f *fakeFinish) commands() finishCommands {
	return finishCommands{
		git: func(args ...string) (string, error) {
			f.gits = append(f.gits, args)
			key := strings.Join(args, " ")
			out, ok := f.git[key]
			if !ok || len(out) == 0 {
				return "", errors.New("git " + key + ": not in the table")
			}
			if len(out) > 1 {
				f.git[key] = out[1:]
			}
			return out[0], nil
		},
		gofmt: func(write bool, files ...string) (string, error) {
			f.gofmts = append(f.gofmts, append([]string{map[bool]string{false: "-l", true: "-w"}[write]}, files...))
			if write {
				return "", nil
			}
			return f.list, nil
		},
	}
}

// finishTable is the git answers of a checkout at head on branch owned, staged at base,
// whose work changed the named Go files.
func finishTable(head, base string, changed ...string) map[string][]string {
	return map[string][]string{
		"rev-parse --verify HEAD^{commit}":                           {head},
		"symbolic-ref --short HEAD":                                  {"owned"},
		"diff --name-only --diff-filter=d " + base + " HEAD -- *.go": {strings.Join(changed, "\n") + "\n"},
		"rev-parse --verify --end-of-options " + head + "^{commit}":  {head},
		"merge-base --is-ancestor " + head + " " + head:              {""},
	}
}

func aResult(t *testing.T, job, head string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("head: "+head+"\nbranch: whatever\nverdict: ok\ngate: -\noutput: -\nreport: done\n"), 0o644))
}

func TestNativeFinishFormatsAndCommitsTheUnformattedFilesAsTheMember(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	head, base, tip := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("d", 40)
	aResult(t, job, head)
	f := &fakeFinish{git: finishTable(head, base, "cmd/a.go", "internal/b.go"), list: "internal/b.go\n"}
	f.git["rev-parse --verify HEAD^{commit}"] = []string{tip} // HEAD after the finish's commit
	f.git["rev-parse --verify --end-of-options "+head+"^{commit}"] = []string{head}
	f.git["merge-base --is-ancestor "+head+" "+tip] = []string{""}
	f.git["add -- internal/b.go"] = []string{""}
	f.git["-c commit.gpgsign=false commit -q -m gofmt at finish: internal/b.go"] = []string{""}
	require.NoError(t, completeNativeResult(job, "", base, "", f.commands()))
	assert.Equal(t, [][]string{{"-l", "cmd/a.go", "internal/b.go"}, {"-w", "internal/b.go"}}, f.gofmts, "gofmt -l over exactly the changed Go files, -w over the ones it named")
	assert.Contains(t, f.gits, []string{"add", "--", "internal/b.go"})
	assert.Contains(t, f.gits, []string{"-c", "commit.gpgsign=false", "commit", "-q", "-m", "gofmt at finish: internal/b.go"})
	finish, exists := cardcontract.ReadFinish(job)
	require.True(t, exists)
	assert.Equal(t, tip, finish.Head, "the head is the tip after the gofmt commit")
	assert.Contains(t, finish.Body, "finish: the result named head "+head+", not the checkout's tip "+tip)
}

func TestNativeFinishRecordsTheTipAndNotesAHeadThatIsNotIt(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	head, base, stated := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	aResult(t, job, stated)
	f := &fakeFinish{git: finishTable(head, base)}
	f.git["rev-parse --verify --end-of-options "+stated+"^{commit}"] = []string{stated}
	f.git["merge-base --is-ancestor "+stated+" "+head] = []string{""}
	require.NoError(t, completeNativeResult(job, "", base, "", f.commands()))
	assert.Empty(t, f.gofmts, "no Go file changed: gofmt is not run")
	finish, exists := cardcontract.ReadFinish(job)
	require.True(t, exists)
	assert.Equal(t, head, finish.Head, "git's tip, not the child's")
	assert.Equal(t, "owned", finish.Branch, "the checkout's branch, not the child's")
	assert.Contains(t, finish.Body, "finish: the result named head "+stated+", not the checkout's tip "+head)
}

func TestNativeFinishWritesTheTipItReadFromGit(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	aResult(t, job, head[:9])
	f := &fakeFinish{git: finishTable(head, base, "cmd/a.go"), list: "\n"}
	f.git["rev-parse --verify --end-of-options "+head[:9]+"^{commit}"] = []string{head}
	require.NoError(t, completeNativeResult(job, "", base, "", f.commands()))
	assert.Equal(t, [][]string{{"-l", "cmd/a.go"}}, f.gofmts, "every file formatted: nothing written, nothing committed")
	assert.NotContains(t, f.gits, []string{"add", "--", "cmd/a.go"})
	finish, exists := cardcontract.ReadFinish(job)
	require.True(t, exists)
	assert.Equal(t, head, finish.Head, "the head is git's full tip, not the child's abbreviation")
	assert.NotContains(t, finish.Body, "finish:", "a stated head that is the tip gets no note")
}

func TestNativeFinishResolvesTheStartFromTheBaseRefWhenNoShaWasStaged(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	aResult(t, job, head)
	f := &fakeFinish{git: finishTable(head, base, "cmd/a.go"), list: ""}
	f.git["merge-base HEAD refs/remotes/origin/main"] = []string{base + "\n"}
	require.NoError(t, completeNativeResult(job, "", "", "main", f.commands()))
	assert.Equal(t, [][]string{{"-l", "cmd/a.go"}}, f.gofmts, "the diff is from the ref's merge base, never the whole tree")
	for _, call := range f.gits {
		assert.NotEqual(t, "ls-files", call[0])
	}
}

func TestNativeFinishFaultsAreTheMachinesNotTheCards(t *testing.T) {
	t.Parallel()
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for name, tc := range map[string]struct {
		table    func(map[string][]string)
		staged   string
		baseRef  string
		gofmtBin bool
		want     string
	}{
		"no staged sha and no base ref": {func(map[string][]string) {}, "", "", true, "no staged commit and no BASE ref"},
		"the base ref does not resolve": {func(map[string][]string) {}, "", "gone", true, "the work's start from BASE gone"},
		"git cannot diff":               {func(g map[string][]string) { delete(g, "diff --name-only --diff-filter=d "+base+" HEAD -- *.go") }, base, "", true, "the Go files the work changed"},
		"git cannot name HEAD":          {func(g map[string][]string) { delete(g, "rev-parse --verify HEAD^{commit}") }, base, "", true, "the checkout's HEAD"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			job := t.TempDir()
			aResult(t, job, head)
			f := &fakeFinish{git: finishTable(head, base, "cmd/a.go")}
			tc.table(f.git)
			err := completeNativeResult(job, "", tc.staged, tc.baseRef, f.commands())
			require.Error(t, err)
			assert.ErrorIs(t, err, errFinishFault)
			assert.Contains(t, err.Error(), tc.want)
			_, exists := cardcontract.ReadFinish(job)
			assert.False(t, exists)
		})
	}
}

func TestNativeFinishWithoutGofmtOnTheBenchIsAFault(t *testing.T) {
	t.Parallel()
	run := nativeFinishCommands(t.TempDir(), t.TempDir()) // a goBin with no gofmt in it
	_, err := run.gofmt(false, "a.go")
	require.Error(t, err)
	assert.ErrorIs(t, err, errFinishFault)
	assert.Contains(t, err.Error(), "no gofmt on the bench")
}

// The real git and gofmt: the one-byte landing red of 2026-10-03 (a committed Go file
// without its final newline) is formatted and committed by the finish, and the head
// recorded is that commit; a Go file gofmt will not parse is the card's refusal, by name.
func TestNativeFinishFormatsACommittedGoFileWithRealGitAndGofmt(t *testing.T) {
	t.Parallel()
	goBin := filepath.Join(runtime.GOROOT(), "bin")
	if _, err := os.Stat(filepath.Join(goBin, "gofmt")); err != nil {
		t.Skipf("no gofmt beside the test's Go: %v", err)
	}
	job := t.TempDir()
	repo := filepath.Join(job, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	runGit(t, repo, "init", "-b", "owned")
	runGit(t, repo, "config", "user.name", "pool")
	runGit(t, repo, "config", "user.email", "pool@example.com")
	commit := func(msg string) string {
		runGit(t, repo, "add", "-A")
		runGit(t, repo, "commit", "-q", "--allow-empty", "-m", msg)
		return strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	}
	staged := commit("staged")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "work.go"), []byte("package work\n\nfunc Work() {}"), 0o644))
	head := commit("work")
	aResult(t, job, head)
	require.NoError(t, completeNativeResult(job, "", staged, "", nativeFinishCommands(repo, goBin)))
	tip := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	assert.NotEqual(t, head, tip, "the finish committed")
	assert.Equal(t, "gofmt at finish: work.go", strings.TrimSpace(runGit(t, repo, "log", "-1", "--format=%s")))
	assert.Equal(t, "pool", strings.TrimSpace(runGit(t, repo, "log", "-1", "--format=%an")), "the checkout's own identity")
	assert.Empty(t, strings.TrimSpace(runGit(t, repo, "status", "--porcelain")), "nothing left unstaged")
	b, err := os.ReadFile(filepath.Join(repo, "work.go"))
	require.NoError(t, err)
	assert.Equal(t, "package work\n\nfunc Work() {}\n", string(b))
	finish, exists := cardcontract.ReadFinish(job)
	require.True(t, exists)
	assert.Equal(t, tip, finish.Head)
	assert.Contains(t, finish.Body, "finish: the result named head "+head+", not the checkout's tip "+tip)

	// a Go file that does not parse is the card's refusal, never formatted or committed
	require.NoError(t, os.WriteFile(filepath.Join(repo, "broken.go"), []byte("package work\n\nfunc (\n"), 0o644))
	head = commit("broken")
	aResult(t, job, head)
	require.NoError(t, os.Remove(filepath.Join(job, cardcontract.FinishName)))
	err = completeNativeResult(job, "", staged, "", nativeFinishCommands(repo, goBin))
	require.Error(t, err)
	assert.NotErrorIs(t, err, errFinishFault)
	assert.Contains(t, err.Error(), "broken.go")
	assert.Equal(t, head, strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")), "nothing committed")
	_, exists = cardcontract.ReadFinish(job)
	assert.False(t, exists)
}
