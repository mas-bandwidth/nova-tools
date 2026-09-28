package merge

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #1607: NO GIT THIS TOOL RUNS LEAVES A GIT BEHIND IT.
//
// `git commit`, `git fetch`, `git merge` and `git receive-pack` end by forking
// `git maintenance run --auto --quiet --detach`, and --detach means the parent does not
// wait for it. The tool's own working directories are removed and rebuilt -- `batch` does
// it with safepath.RemoveUnder on every run, and every test here does it with t.TempDir --
// so a git the tool left running is a git still writing into a directory something else is
// removing: `unlinkat .../work/.git/objects: directory not empty`, on a test whose
// assertions had already passed.
//
// This watches git's own trace2 stream, which records every child a git starts, so what is
// read here is what git did rather than what this file hopes it did.
func TestNoGitThisToolRunsStartsABackgroundGit(t *testing.T) {
	// No t.Parallel: this sets GIT_TRACE2_EVENT for the process, and Go runs the
	// sequential tests with every parallel one paused.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on this machine")
	}
	dir := t.TempDir()
	repo, trace := filepath.Join(dir, "repo"), filepath.Join(dir, "trace")
	for _, d := range []string{repo, trace} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The bench's own git configuration decides nothing here: a runner with
	// maintenance.auto already off would make this test pass by accident.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "no-such-gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "no-such-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TRACE2_EVENT", trace)

	g := NewGit(repo, time.Minute, nil)
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main", "."},
		{"add", "-A"},
	} {
		if args[0] == "add" {
			if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if out, err := g.Run(args...); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	// The commit is the one that forks the maintenance child.
	if out, err := g.Run(Identity("commit", "--quiet", "-m", "one")...); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	started, files := gitChildrenStarted(t, trace)
	if files == 0 {
		t.Skip("this git wrote no trace2 events, so this bench cannot see the children git starts")
	}
	var background []string
	for _, argv := range started {
		for _, word := range argv {
			if word == "maintenance" || word == "gc" || word == "fsmonitor--daemon" {
				background = append(background, strings.Join(argv, " "))
				break
			}
		}
	}
	if len(background) > 0 {
		t.Errorf("a git this tool ran started %d background git(s) it does not wait for:\n\t%s\nevery git the tool starts carries noBackgroundGit, so that nothing is still writing into a repository the tool or a test is about to remove (#1607)",
			len(background), strings.Join(background, "\n\t"))
	}
}

// Every setting in noBackgroundGit is a setting git really has under that name. A typo in
// one of them is silent -- git accepts any `-c key=value` it does not know -- so the list
// is read back out of git itself rather than trusted.
func TestNoBackgroundGitNamesSettingsGitHas(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on this machine")
	}
	dir := t.TempDir()
	if len(noBackgroundGit)%2 != 0 {
		t.Fatalf("noBackgroundGit is %d words; it is pairs of -c and key=value", len(noBackgroundGit))
	}
	for i := 0; i < len(noBackgroundGit); i += 2 {
		if noBackgroundGit[i] != "-c" {
			t.Fatalf("noBackgroundGit[%d] is %q, want -c", i, noBackgroundGit[i])
		}
		key, value, ok := strings.Cut(noBackgroundGit[i+1], "=")
		if !ok {
			t.Fatalf("noBackgroundGit[%d] is %q, want key=value", i+1, noBackgroundGit[i+1])
		}
		// git answers with the type it parses the value as, so a value git cannot read
		// as that setting's type is an error here rather than a quiet default.
		out, err := NewGit(dir, time.Minute, nil).Out("config", "--default", value, "--type", typeOf(value), "--get", key)
		if err != nil {
			t.Errorf("git cannot read %s=%s: %v\n%s", key, value, err, out)
		}
	}
}

// typeOf is the git config type a value is written in: the four settings are two booleans,
// one integer and one boolean.
func typeOf(value string) string {
	if value == "true" || value == "false" {
		return "bool"
	}
	return "int"
}

// gitChildrenStarted reads git's trace2 event files and returns the argument list of every
// child a git started, with the number of trace files it read -- zero means the bench's
// git wrote no events and the caller has seen nothing.
func gitChildrenStarted(t *testing.T, dir string) ([][]string, int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the trace directory: %v", err)
	}
	var out [][]string
	files := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading a trace file: %v", err)
		}
		files++
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, `"child_start"`) {
				continue
			}
			var ev struct {
				Event string   `json:"event"`
				Argv  []string `json:"argv"`
			}
			if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Event != "child_start" {
				continue
			}
			out = append(out, ev.Argv)
		}
	}
	return out, files
}

// #1607, THE SECOND NAME THE SAME CHILD HAS. The landing gate on hulk refused the first
// version of this change, and it was right: `TestTheFixtureStartsNoGitItDoesNotWaitFor`
// was red there with
//
//	the fixture started 5 background git(s) nothing waits for:
//		git gc --auto --quiet
//
// hulk is git 2.43.0; vision and space are 2.53.0 and the Studio is 2.55.0. On 2.53+
// `receive-pack` reaches the maintenance run through run_auto_maintenance(), which obeys
// `maintenance.auto=false`. On 2.43 `receive-pack` forks `git gc --auto --quiet` itself,
// gated ONLY by `receive.autogc` -- and `gc.auto=0` makes that child a no-op without
// stopping it being started. A started git is a git writing in the directory, which is the
// whole of #1607, so a no-op child is not good enough.
//
// The one above drives a COMMIT, which forks nothing on 2.43. This one drives a PUSH,
// which is the operation that has the second name, into a receiving repository carrying
// noBackgroundGit's settings in its own config -- exactly what the fixtures and the
// production remotes do, since git clears a `-c` out of the environment before starting a
// git on a repository that is not this one (local_repo_env).
//
// WITHOUT `receive.autogc=false` this test is RED on git 2.43 and green on 2.53+, which is
// how the defect reached a green CI in the first place: the pull request's test legs run on
// space and studio only.
func TestAPushThisToolMakesLeavesNoGitInTheReceivingRepository(t *testing.T) {
	// No t.Parallel: this sets GIT_TRACE2_EVENT for the process, and Go runs the
	// sequential tests with every parallel one paused.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on this machine")
	}
	dir := t.TempDir()
	work, remote, trace := filepath.Join(dir, "work"), filepath.Join(dir, "remote.git"), filepath.Join(dir, "trace")
	for _, d := range []string{work, remote, trace} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The bench's own git configuration decides nothing here: a runner that already had
	// receive.autogc off would make this pass by accident.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "no-such-gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "no-such-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	g := NewGit(work, time.Minute, nil)
	bare := NewGit(remote, time.Minute, nil)
	if out, err := bare.Run("init", "--quiet", "--bare", "-b", "main", "."); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	// The receiving repository carries the settings itself. This is the fixture's
	// quietRepo and the production remote's own config, written here through the same
	// list so that the day a sixth setting is added this test carries it too.
	quietRepo(t, remote)
	if out, err := g.Run("init", "--quiet", "-b", "main", "."); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := g.Run("add", "-A"); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := g.Run(Identity("commit", "--quiet", "-m", "one")...); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// The trace is switched on for the PUSH alone, so what is counted is the child that
	// push's receive-pack started and nothing the set-up did.
	t.Setenv("GIT_TRACE2_EVENT", trace)
	for _, target := range []string{remote, "file://" + filepath.ToSlash(remote)} {
		// Both spellings: `git push <path>` and `git push file://<path>` take different
		// code paths inside git, and the gc child was measured on hulk under both.
		if out, err := g.Run("push", "--quiet", target, "HEAD:refs/heads/"+refFor(target)); err != nil {
			t.Fatalf("git push %s: %v\n%s", target, err, out)
		}
	}

	started, files := gitChildrenStarted(t, trace)
	if files == 0 {
		t.Skip("this git wrote no trace2 events, so this bench cannot see the children git starts")
	}
	var background []string
	for _, argv := range started {
		for _, word := range argv {
			if word == "maintenance" || word == "gc" || word == "fsmonitor--daemon" {
				background = append(background, strings.Join(argv, " "))
				break
			}
		}
	}
	if len(background) > 0 {
		t.Errorf("a push this tool made left %d background git(s) in the receiving repository:\n\t%s\nthe receiving repository carries noBackgroundGit, and on a git older than 2.53 the setting that stops this one is receive.autogc=false (#1607)",
			len(background), strings.Join(background, "\n\t"))
	}
}

// refFor keeps the two pushes off one another's ref, so neither is a no-op that would
// start no receive-pack at all and pass for the wrong reason.
func refFor(target string) string {
	if strings.HasPrefix(target, "file://") {
		return "by-url"
	}
	return "by-path"
}
