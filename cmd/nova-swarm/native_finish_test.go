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
	require.NoError(t, completeNativeResult(job, "", "", nativeFinishCommands(repo, "")))
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
	err := completeNativeResult(job, "", "", nativeFinishCommands(repo, ""))
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
			err := completeNativeResult(job, "", "", nativeFinishCommands(repo, ""))
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

// fakeFinish is a finish's two commands answered from a table and no subprocess: git by
// its joined arguments, gofmt by one answer; an argument line the table lacks is an error,
// as the real git's would be.
func fakeFinish(t *testing.T, git map[string]string, gofmtList string) (finishCommands, *[][]string) {
	t.Helper()
	var asked [][]string
	return finishCommands{
		git: func(args ...string) (string, error) {
			key := strings.Join(args, " ")
			out, ok := git[key]
			if !ok {
				return "", errors.New("git " + key + ": not in the table")
			}
			return out, nil
		},
		gofmt: func(files ...string) (string, error) {
			asked = append(asked, files)
			return gofmtList, nil
		},
	}, &asked
}

// finishTable is the git answers of a checkout at head on branch owned, staged at base,
// whose work changed the named Go files.
func finishTable(head, base string, changed ...string) map[string]string {
	return map[string]string{
		"rev-parse --verify HEAD^{commit}":                           head,
		"symbolic-ref --short HEAD":                                  "owned",
		"diff --name-only --diff-filter=d " + base + " HEAD -- *.go": strings.Join(changed, "\n") + "\n",
		"rev-parse --verify --end-of-options " + head + "^{commit}":  head,
		"merge-base --is-ancestor " + head + " " + head:              "",
	}
}

func TestNativeFinishRefusesUnformattedFilesByName(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("head: "+head+"\nbranch: owned\nverdict: ok\ngate: -\noutput: -\nreport: done\n"), 0o644))
	run, asked := fakeFinish(t, finishTable(head, base, "cmd/a.go", "internal/b.go"), "internal/b.go\n")
	err := completeNativeResult(job, "", base, run)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal/b.go")
	assert.Contains(t, err.Error(), "gofmt -w")
	assert.Equal(t, [][]string{{"cmd/a.go", "internal/b.go"}}, *asked, "gofmt -l runs over exactly the changed Go files")
	_, exists := cardcontract.ReadFinish(job)
	assert.False(t, exists, "a refused finish records nothing")
}

func TestNativeFinishRefusesAHeadThatIsNotTheTip(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	head, base, stated := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("head: "+stated+"\nbranch: owned\nverdict: ok\ngate: -\noutput: -\nreport: done\n"), 0o644))
	table := finishTable(head, base)
	table["rev-parse --verify --end-of-options "+stated+"^{commit}"] = stated
	table["merge-base --is-ancestor "+stated+" "+head] = ""
	run, asked := fakeFinish(t, table, "")
	err := completeNativeResult(job, "", base, run)
	require.Error(t, err)
	assert.Contains(t, err.Error(), stated, "the line names the stated head")
	assert.Contains(t, err.Error(), head, "and the checkout's tip")
	assert.Empty(t, *asked, "no Go file changed: gofmt is not run")
	_, exists := cardcontract.ReadFinish(job)
	assert.False(t, exists)
}

func TestNativeFinishWritesTheTipItReadFromGit(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("head: "+head[:9]+"\nbranch: whatever\nverdict: ok\ngate: -\noutput: -\nreport: done\n"), 0o644))
	table := finishTable(head, base, "cmd/a.go")
	table["rev-parse --verify --end-of-options "+head[:9]+"^{commit}"] = head
	run, asked := fakeFinish(t, table, "\n")
	require.NoError(t, completeNativeResult(job, "", base, run))
	assert.Len(t, *asked, 1)
	finish, exists := cardcontract.ReadFinish(job)
	require.True(t, exists)
	assert.Equal(t, head, finish.Head, "the head is git's full tip, not the child's abbreviation")
	assert.Equal(t, "owned", finish.Branch, "the branch is the checkout's, not the child's")
}

func TestNativeFinishRefusesACommittedUnformattedGoFileThenTakesTheFix(t *testing.T) {
	t.Parallel()
	goBin := filepath.Join(runtime.GOROOT(), "bin")
	if _, err := os.Stat(filepath.Join(goBin, "gofmt")); err != nil {
		t.Skipf("no gofmt beside the test's Go: %v", err)
	}
	job := t.TempDir()
	repo := filepath.Join(job, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	runGit(t, repo, "init", "-b", "owned")
	commit := func(msg string) string {
		runGit(t, repo, "add", "-A")
		runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", msg)
		return strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	}
	staged := commit("staged")
	// the one-byte landing red of 2026-10-03: a Go file committed without its final newline
	require.NoError(t, os.WriteFile(filepath.Join(repo, "work.go"), []byte("package work\n\nfunc Work() {}"), 0o644))
	head := commit("work")
	result := func(head string) {
		require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("head: "+head+"\nbranch: owned\nverdict: ok\ngate: -\noutput: -\nreport: done\n"), 0o644))
	}
	result(head)
	err := completeNativeResult(job, "", staged, nativeFinishCommands(repo, goBin))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "work.go")
	_, exists := cardcontract.ReadFinish(job)
	require.False(t, exists)
	// the next turn formats and commits; the same finish takes it
	require.NoError(t, os.WriteFile(filepath.Join(repo, "work.go"), []byte("package work\n\nfunc Work() {}\n"), 0o644))
	head = commit("gofmt")
	result(head)
	require.NoError(t, completeNativeResult(job, "", staged, nativeFinishCommands(repo, goBin)))
	finish, exists := cardcontract.ReadFinish(job)
	require.True(t, exists)
	assert.Equal(t, head, finish.Head)
}
