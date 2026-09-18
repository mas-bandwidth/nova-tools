package main

// The install verb (nova-tools #1142): build nova-tools once on --build-bench,
// cache by commit, install on every bench by atomic rename, verify each bench's
// nova-swarm carries the commit. The tests drive a fake runner that records the
// (target, script) of every remote command and answers canned output, so no ssh
// runs and no machine is reached. The shell replacement a review found ignored
// failed cp/mv is why a copy, install or verify failure must be named.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

const (
	installFull = "5f544272a1b0c2d3e4f5a6b7c8d9e0f1a2b3c4d5"
	install12   = "5f544272a1b0"
)

// fakeInstallRunner records every remote command and answers it from answer.
type fakeInstallRunner struct {
	mu     sync.Mutex
	calls  []installCall
	answer func(target, script string) (string, error)
}

type installCall struct {
	target string
	script string
}

func (f *fakeInstallRunner) Run(_ context.Context, target, script string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, installCall{target: target, script: script})
	f.mu.Unlock()
	return f.answer(target, script)
}

func (f *fakeInstallRunner) recorded() []installCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]installCall(nil), f.calls...)
}

// installBuildAnswer is the build script's canned hit: every call whose script
// builds answers INSTALLBUILT; the rest answer as the per-bench default.
func installBuildAnswer(benchAnswer func(target, script string) (string, error)) func(target, script string) (string, error) {
	return func(target, script string) (string, error) {
		if strings.Contains(script, "go build") {
			return "INSTALLBUILT\t" + installFull + "\t16\tno\n", nil
		}
		return benchAnswer(target, script)
	}
}

func runInstall(t *testing.T, home string, benches string, runner pulse.InstallRunner) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := pulse.Install(pulse.InstallInput{
		SHA:        install12,
		BuildBench: "hulk",
		Benches:    strings.Split(benches, ","),
		Home:       home,
		Timeout:    time.Minute,
		Runner:     runner,
		Stdout:     &out,
		Stderr:     &errb,
	})
	if errb.Len() > 0 {
		t.Logf("stderr: %s", errb.String())
	}
	return code, out.String()
}

// install-builds-once-and-installs-everywhere: one build line, one OK per bench,
// one DONE, exit 0, and the build bench's cache is copied to the others.
func TestInstallBuildsOnceAndInstallsEverywhere(t *testing.T) {
	fake := &fakeInstallRunner{answer: installBuildAnswer(func(_, _ string) (string, error) {
		return "INSTALLED\ttools=16\tsha=" + installFull + "\n", nil
	})}
	code, out := runInstall(t, t.TempDir(), "hulk,vision", fake)
	if code != 0 {
		t.Fatalf("install exit = %d, want 0; out=%q", code, out)
	}
	want := "INSTALL BUILD bench=hulk sha=" + installFull + " tools=16 cached=no\n" +
		"INSTALL OK bench=hulk tools=16 sha=" + install12 + "\n" +
		"INSTALL OK bench=vision tools=16 sha=" + install12 + "\n" +
		"INSTALL DONE benches=2 ok=2 skipped=0 failed=0\n"
	if out != want {
		t.Fatalf("install output:\n%q\nwant:\n%q", out, want)
	}
	calls := fake.recorded()
	if len(calls) != 3 {
		t.Fatalf("runner saw %d calls, want build + one per bench = 3: %+v", len(calls), calls)
	}
	if calls[0].target != "hulk" || !strings.Contains(calls[0].script, install12) {
		t.Fatalf("the build did not run on hulk with the sha in the script: %+v", calls[0])
	}
	if !strings.Contains(calls[2].script, "FULL='"+installFull+"'") ||
		!strings.Contains(calls[2].script, "tar -C ~/nova-bench/build/$FULL") {
		t.Fatalf("the vision script does not copy the build bench's cache: %s", calls[2].script)
	}
}

// install-skips-a-bench-already-at-the-sha: a bench whose nova-swarm already
// reports the commit is INSTALL SKIP, not reinstalled.
func TestInstallSkipsABenchAlreadyAtTheSHA(t *testing.T) {
	fake := &fakeInstallRunner{answer: installBuildAnswer(func(target, _ string) (string, error) {
		if target == "vision" {
			return "INSTALLSKIP\t" + installFull + "\n", nil
		}
		return "INSTALLED\ttools=16\tsha=" + installFull + "\n", nil
	})}
	code, out := runInstall(t, t.TempDir(), "hulk,vision", fake)
	if code != 0 {
		t.Fatalf("install exit = %d, want 0; out=%q", code, out)
	}
	if !strings.Contains(out, "INSTALL SKIP bench=vision already at "+install12+"\n") {
		t.Fatalf("install output missing the vision skip:\n%s", out)
	}
	if !strings.Contains(out, "INSTALL DONE benches=2 ok=1 skipped=1 failed=0") {
		t.Fatalf("install summary counts the skip wrong:\n%s", out)
	}
}

// install-fails-when-a-copy-fails: the copy step is named, the bench's reason is
// carried, the exit is 1, and the other benches still ran.
func TestInstallFailsWhenACopyFails(t *testing.T) {
	fake := &fakeInstallRunner{answer: installBuildAnswer(func(target, _ string) (string, error) {
		if target == "vision" {
			return "INSTALLFAIL\tcopy\tcopying the cache from hulk failed\n", nil
		}
		return "INSTALLED\ttools=16\tsha=" + installFull + "\n", nil
	})}
	code, out := runInstall(t, t.TempDir(), "hulk,vision", fake)
	if code != 1 {
		t.Fatalf("install exit = %d, want 1; out=%q", code, out)
	}
	want := "INSTALL FAIL bench=vision step=copy copying the cache from hulk failed\n"
	if !strings.Contains(out, want) {
		t.Fatalf("install output missing %q:\n%s", want, out)
	}
	if !strings.Contains(out, "INSTALL DONE benches=2 ok=1 skipped=0 failed=1") {
		t.Fatalf("install summary counts the failure wrong:\n%s", out)
	}
}

// install-fails-when-a-binary-fails: the install step names the binary and the
// destination directory that failed, the review's exact finding.
func TestInstallFailsWhenABinaryFails(t *testing.T) {
	fake := &fakeInstallRunner{answer: installBuildAnswer(func(target, _ string) (string, error) {
		if target == "vision" {
			return "INSTALLFAIL\tinstall\tnova-bus -> /home/rowan/.local/bin failed\n", nil
		}
		return "INSTALLED\ttools=16\tsha=" + installFull + "\n", nil
	})}
	code, out := runInstall(t, t.TempDir(), "hulk,vision", fake)
	if code != 1 {
		t.Fatalf("install exit = %d, want 1; out=%q", code, out)
	}
	want := "INSTALL FAIL bench=vision step=install nova-bus -> /home/rowan/.local/bin failed\n"
	if !strings.Contains(out, want) {
		t.Fatalf("install output missing %q:\n%s", want, out)
	}
}

// install-fails-when-the-sha-does-not-verify: a bench that installs but reports
// another commit is a verify failure, exit 1.
func TestInstallFailsWhenVerifyFails(t *testing.T) {
	fake := &fakeInstallRunner{answer: installBuildAnswer(func(target, _ string) (string, error) {
		if target == "vision" {
			return "INSTALLFAIL\tverify\tnova-swarm reports: v0.0.0-other\n", nil
		}
		return "INSTALLED\ttools=16\tsha=" + installFull + "\n", nil
	})}
	code, out := runInstall(t, t.TempDir(), "hulk,vision", fake)
	if code != 1 {
		t.Fatalf("install exit = %d, want 1; out=%q", code, out)
	}
	if !strings.Contains(out, "INSTALL FAIL bench=vision step=verify nova-swarm reports: v0.0.0-other\n") {
		t.Fatalf("install output missing the verify failure:\n%s", out)
	}
}

// install-prunes-through-safepath: with seven caches under the build root, only
// the newest five survive; the removal is safepath.RemoveUnder.
func TestInstallPrunesThroughSafepath(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "nova-bench", "build")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"1111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333",
		"4444444444444444444444444444444444444444",
		"5555555555555555555555555555555555555555",
		"6666666666666666666666666666666666666666",
		"7777777777777777777777777777777777777777",
	}
	for i, name := range names {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stamp := time.Date(2026, 9, 1+i, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(dir, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeInstallRunner{answer: installBuildAnswer(func(_, _ string) (string, error) {
		return "INSTALLED\ttools=16\tsha=" + installFull + "\n", nil
	})}
	if code, out := runInstall(t, home, "hulk", fake); code != 0 {
		t.Fatalf("install exit = %d, want 0; out=%q", code, out)
	}
	for i, name := range names {
		_, err := os.Stat(filepath.Join(root, name))
		if i < 2 && err == nil {
			t.Errorf("old cache %s survived the prune", name)
		}
		if i >= 2 && err != nil {
			t.Errorf("new cache %s was pruned: %v", name, err)
		}
	}
}

// install-refuses-bad-inputs-before-any-ssh: a non-hex sha and a bad bench name
// are refused, exit 2, and the fake ssh on PATH is never run.
func TestInstallRefusesBadInputsBeforeAnySSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the marker ssh is a shell script; the validation path is not platform specific")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ssh-ran")
	writeBenchExe(t, filepath.Join(dir, "ssh"), "#!/bin/sh\ntouch "+marker+"\nexit 255\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cases := []struct {
		name string
		args []string
	}{
		{"sha", []string{"install", "--sha", "nothex", "--build-bench", "hulk", "--benches", "hulk,vision"}},
		{"build-bench", []string{"install", "--sha", install12, "--build-bench", "hulk;rm", "--benches", "hulk,vision"}},
		{"benches", []string{"install", "--sha", install12, "--build-bench", "hulk", "--benches", "hulk,bad/name"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb, time.Now().UTC()); code != 2 {
				t.Fatalf("exit = %d, want 2; stderr=%q", code, errb.String())
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("ssh ran before the inputs were validated")
			}
		})
	}
}

// install-missing-flags-refuse: every required flag is named when absent.
func TestInstallMissingFlagsRefuse(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"install"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("install with no flags exit = %d, want 2", code)
	}
	for _, want := range []string{"--sha", "--build-bench", "--benches"} {
		if !strings.Contains(errb.String(), want+" is required") {
			t.Errorf("stderr does not name %s: %q", want, errb.String())
		}
	}
}

// help-lists-the-install-verb.
func TestHelpListsInstall(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	found := false
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nova-pulse" && f[1] == "install" {
			found = true
			if strings.Contains(line, "not yet implemented") {
				t.Errorf("install is shipped and help marks it unshipped: %q", line)
			}
		}
	}
	if !found {
		t.Fatalf("help does not list the install verb:\n%s", out.String())
	}
}
