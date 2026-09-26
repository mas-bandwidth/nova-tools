package ci

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// selectpackages_never_silent_test.go is the class test of card
// ci-select-never-silent. Measured 2026-09-26 ~1:00 PM ET: on PR #4370's final
// head the shards reported `test (nothing)` because
// .github/scripts/select-packages.sh hit `go list` errors on a space runner (a
// shared GOCACHE race: "open /home/ubuntu/.cache/go-build/...: no such file or
// directory") and printed "0 package(s) touched: none" instead of failing, so a
// green run tested nothing.
//
// The rule: a go list failure (non-zero exit, or "cannot" / "no such file" on
// its stderr), and a Go diff that selects zero packages, either FAIL the script
// with the error printed (--on-go-list-error=fail, the default and ci.yml's
// pull_request) or print `WARN select-packages: go list failed (...); testing
// the whole tree` and the whole tree (--on-go-list-error=whole-tree, ci.yml's
// merge_group, push and schedule). Never an empty answer and exit 0.
//
// The script runs for real against a two-commit fixture repository in
// t.TempDir() with a fake `go` first on PATH; no real go list, no remote.

// fakeGo is the `go` on PATH. FAKE_GO picks the failure: cache is the #4370
// race (exit 1), cannot is a zero exit with "cannot" on stderr, empty is a zero
// exit listing nothing, unrelated lists one package the diff never touched.
const fakeGo = `#!/bin/sh
case "$FAKE_GO" in
cache) echo "open /home/ubuntu/.cache/go-build/5e/5e1f-d: no such file or directory" >&2; exit 1 ;;
cannot) echo "go: cannot find main module" >&2; exit 0 ;;
empty) exit 0 ;;
unrelated) echo "github.com/mas-bandwidth/nova-tools/cmd/other"; exit 0 ;;
esac
echo "fake go: unknown FAKE_GO=$FAKE_GO" >&2
exit 3
`

// TestSelectPackagesNeverSilentlySelectsNothing runs select-packages.sh with a
// failing `go` and asserts the failure or the WARN line, never "nothing".
func TestSelectPackagesNeverSilentlySelectsNothing(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("select-packages.sh is a bash script for the self-hosted shards")
	}
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH: %v", tool, err)
		}
	}

	script := readFile(t, filepath.Join(repoRoot(t), ".github", "scripts", "select-packages.sh"))
	repo, base, env := selectFixture(t, script)

	const warn = "WARN select-packages: go list failed (open /home/ubuntu/.cache/go-build/5e/5e1f-d: no such file or directory); testing the whole tree"
	wholeTree := "./cmd/foo\n./internal/bar\n./internal/ci\n./internal/docs\n"

	cases := []struct {
		name, fake string
		args       []string
		wantOK     bool
		wantStdout string
		wantStderr string
	}{
		{"pull_request default fails on the cache race", "cache", []string{base}, false, "", "no such file or directory"},
		{"pull_request fail fails on the cache race", "cache", []string{"--on-go-list-error=fail", base}, false, "", "go list failed"},
		{"a zero exit with cannot on stderr fails", "cannot", []string{"--on-go-list-error=fail", base}, false, "", "cannot find main module"},
		{"a zero exit listing nothing fails", "empty", []string{base}, false, "", "listed no packages"},
		{"a Go diff selecting zero packages fails", "unrelated", []string{base}, false, "", "selected zero packages"},
		{"merge_group falls back to the whole tree", "cache", []string{"--on-go-list-error=whole-tree", base}, true, wholeTree, warn},
		{"push --all falls back to the whole tree", "cache", []string{"--on-go-list-error=whole-tree", "--all"}, true, wholeTree, warn},
		{"--all fails on the cache race by default", "cache", []string{"--all"}, false, "", "go list failed"},
		{"a Go diff selecting zero packages falls back", "unrelated", []string{"--on-go-list-error=whole-tree", base}, true, wholeTree, "WARN select-packages: go list failed (select-packages: the diff touches Go files (cmd/foo/foo.go) but selected zero packages); testing the whole tree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("bash", append([]string{filepath.Join(repo, ".github", "scripts", "select-packages.sh")}, tc.args...)...)
			cmd.Dir = repo
			cmd.Env = append(append([]string{}, env...), "FAKE_GO="+tc.fake)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if tc.wantOK && err != nil {
				t.Fatalf("select-packages.sh %v: %v; want exit 0\nstderr:\n%s", tc.args, err, stderr.String())
			}
			if !tc.wantOK && err == nil {
				t.Fatalf("select-packages.sh %v exited 0 with stdout %q; a go list failure must fail the job, never select nothing silently", tc.args, stdout.String())
			}
			if stdout.String() != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tc.wantStdout)
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

// selectFixture builds a repository holding the script and four packages, with
// a base commit and a HEAD that edits cmd/foo/foo.go, and the environment that
// puts the fake go first on PATH. It returns the repo, the base sha and env.
func selectFixture(t *testing.T, script string) (string, string, []string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	bin := filepath.Join(dir, "bin")
	home := filepath.Join(dir, "home")
	tmp := filepath.Join(dir, "tmp")
	for _, d := range []string{bin, home, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".github/scripts/select-packages.sh": script,
		"cmd/foo/foo.go":                     "package main\n",
		"internal/bar/bar.go":                "package bar\n",
		"internal/ci/ci.go":                  "package ci\n",
		"internal/docs/docs.go":              "package docs\n",
		"internal/tagged/tagged.go":          "//go:build swarmtest\n\npackage tagged\n",
		"internal/bar/testdata/x/x.go":       "package x\n",
	}
	for name, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + tmp,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "cmd", "foo", "foo.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-am", "touch cmd/foo")
	return repo, base, env
}

// selectConsumedByProcessSubstitution is the #4370 shape in ci.yml: the
// script's answer read through `< <(...)`, which throws its exit status away.
var selectConsumedByProcessSubstitution = regexp.MustCompile(`<\s*<\(\s*bash \.github/scripts/select-packages\.sh`)

// TestCIReadsSelectPackagesExitStatus pins the caller: ci.yml reads the script
// by command substitution so its failure stops the step, fails on pull_request
// and falls back to the whole tree elsewhere.
func TestCIReadsSelectPackagesExitStatus(t *testing.T) {
	t.Parallel()
	src := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if selectConsumedByProcessSubstitution.MatchString(src) {
		t.Errorf("ci.yml reads select-packages.sh through `< <(...)`, which discards its exit status: a go list failure became `test (nothing)` on PR #4370; read it with command substitution and stop the step on failure")
	}
	for _, want := range []string{
		`on_go_list_error=--on-go-list-error=whole-tree`,
		`[ "${{ github.event_name }}" = "pull_request" ] && on_go_list_error=--on-go-list-error=fail`,
		`select-packages.sh "$on_go_list_error" "$base") || {`,
		`select-packages.sh "$on_go_list_error" --all) || {`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("ci.yml does not contain %q: pull_request fails the job on a go list failure, merge_group and push fall back to the whole tree", want)
		}
	}
}
