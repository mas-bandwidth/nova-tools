package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

// selectpackages_never_silent_test.go is the class test of card
// ci-select-never-silent. Measured 2026-09-26 ~1:00 PM ET: on PR #4370's final
// head the shards reported `test (nothing)` because the package selection hit
// `go list` errors on a runner (a shared GOCACHE race: "open
// .../go-build/...: no such file or directory") and printed "0 package(s)
// touched: none" instead of failing, so a green run tested nothing.
//
// The rule: a go list failure (non-zero exit, or "cannot" / "no such file" on
// its stderr), and a Go diff that selects zero packages, either FAIL the
// selection with the error printed (WholeTreeOnError false, the default and
// ci.yml's pull_request) or warn `WARN select-packages: go list failed (...);
// testing the whole tree` and select the whole tree (WholeTreeOnError, ci.yml's
// merge_group, push and schedule). Never an empty answer and no error.
//
// pkgselect.Select runs here over a fixture tree with a runner that answers
// `go list` and `git` from a table: no real go list, no remote.

// fakeGoList answers the commands the selection runs. mode picks the failure:
// cache is the #4370 race (exit 1), cannot is a zero exit with "cannot" on
// stderr, empty is a zero exit listing nothing, unrelated lists one package the
// diff never touched.
func fakeGoList(mode string) pkgselect.Runner {
	return func(dir string, env []string, argv ...string) (pkgselect.Result, error) {
		switch strings.Join(argv, " ") {
		case "git cat-file -e base^{commit}":
			return pkgselect.Result{}, nil
		case "git diff --name-only base HEAD":
			return pkgselect.Result{Stdout: "cmd/foo/foo.go\n"}, nil
		case "git ls-files -z -- cmd/*.go internal/*.go tools/*.go":
			return pkgselect.Result{Stdout: "cmd/foo/foo.go\x00internal/bar/bar.go\x00internal/ci/ci.go\x00internal/docs/docs.go\x00internal/tagged/tagged.go\x00internal/bar/testdata/x/x.go\x00"}, nil
		}
		switch mode {
		case "cache":
			return pkgselect.Result{Stderr: "open /home/ubuntu/.cache/go-build/5e/5e1f-d: no such file or directory\n", Code: 1}, nil
		case "cannot":
			return pkgselect.Result{Stderr: "go: cannot find main module\n"}, nil
		case "empty":
			return pkgselect.Result{}, nil
		case "unrelated":
			return pkgselect.Result{Stdout: "example.com/m/cmd/other\n"}, nil
		}
		return pkgselect.Result{Code: 3}, nil
	}
}

// TestSelectPackagesNeverSilentlySelectsNothing runs the selection with a
// failing `go list` and asserts the failure or the WARN line, never "nothing".
func TestSelectPackagesNeverSilentlySelectsNothing(t *testing.T) {
	t.Parallel()

	repo := selectFixture(t)

	const warn = "WARN select-packages: go list failed (open /home/ubuntu/.cache/go-build/5e/5e1f-d: no such file or directory); testing the whole tree"
	wholeTree := []string{"./cmd/foo", "./internal/bar", "./internal/ci", "./internal/docs"}

	cases := []struct {
		name, fake string
		opts       pkgselect.Options
		wantErr    string
		wantWarn   string
	}{
		{"pull_request default fails on the cache race", "cache", pkgselect.Options{Base: "base"}, "no such file or directory", ""},
		{"pull_request fail fails on the cache race", "cache", pkgselect.Options{Base: "base"}, "go list failed", ""},
		{"a zero exit with cannot on stderr fails", "cannot", pkgselect.Options{Base: "base"}, "cannot find main module", ""},
		{"a zero exit listing nothing fails", "empty", pkgselect.Options{Base: "base"}, "listed no packages", ""},
		{"a Go diff selecting zero packages fails", "unrelated", pkgselect.Options{Base: "base"}, "selected zero packages", ""},
		{"merge_group falls back to the whole tree", "cache", pkgselect.Options{Base: "base", WholeTreeOnError: true}, "", warn},
		{"push --all falls back to the whole tree", "cache", pkgselect.Options{All: true, WholeTreeOnError: true}, "", warn},
		{"--all fails on the cache race by default", "cache", pkgselect.Options{All: true}, "go list failed", ""},
		{"a Go diff selecting zero packages falls back", "unrelated", pkgselect.Options{Base: "base", WholeTreeOnError: true}, "", "WARN select-packages: go list failed (select-packages: the diff touches Go files (cmd/foo/foo.go) but selected zero packages); testing the whole tree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.opts.Root = repo
			out, err := pkgselect.Select(fakeGoList(tc.fake), tc.opts)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Select exited clean with %v; a go list failure must fail the job, never select nothing silently", out.Packages)
				}
				if !strings.Contains(err.Error(), tc.wantErr) || len(out.Packages) != 0 {
					t.Errorf("Select = %v, %v; want no packages and an error containing %q", out.Packages, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Select: %v; want the whole tree with a warning", err)
			}
			if !slices.Equal(out.Packages, wholeTree) {
				t.Errorf("packages = %v, want %v", out.Packages, wholeTree)
			}
			if out.Warning != tc.wantWarn {
				t.Errorf("warning = %q, want %q", out.Warning, tc.wantWarn)
			}
		})
	}
}

// selectFixture builds the tree the whole-tree fallback reads from the tracked
// files: four packages, one behind a build tag, a testdata package, and a
// deprecated list naming one package the fixture does not hold.
func selectFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	files := map[string]string{
		"go.mod":                       "module example.com/m\n",
		pkgselect.DeprecatedFile:       "# the fixture's list\ncmd/gone\n",
		"cmd/foo/foo.go":               "package main\n",
		"internal/bar/bar.go":          "package bar\n",
		"internal/ci/ci.go":            "package ci\n",
		"internal/docs/docs.go":        "package docs\n",
		"internal/tagged/tagged.go":    "//go:build swarmtest\n\npackage tagged\n",
		"internal/bar/testdata/x/x.go": "package x\n",
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
	return repo
}

// selectConsumedByProcessSubstitution is the #4370 shape: the selection's
// answer read through `< <(...)`, which throws its exit status away.
var selectConsumedByProcessSubstitution = regexp.MustCompile(`<\s*<\(\s*go run \./tools/ci (select-packages|test-matrix)`)

// TestCIReadsSelectionExitStatus pins the caller: the workflow runs the verb as
// the step itself, so its failure stops the step; the verb fails on
// pull_request and falls back to the whole tree elsewhere.
func TestCIReadsSelectionExitStatus(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	if selectConsumedByProcessSubstitution.MatchString(src) {
		t.Errorf("ci.yml reads the selection through `< <(...)`, which discards its exit status: a go list failure became `test (nothing)` on PR #4370; run the verb as the step")
	}
	block := jobBody(src, "test-packages")
	if !strings.Contains(block, ciRunner+" test-matrix --event") {
		t.Errorf("test-packages does not run `ci test-matrix` as the step itself, so its exit status is the step's")
	}
	verb := readFile(t, filepath.Join(root, "tools", "ci", "sel_matrix.go"))
	for _, want := range []string{
		`WholeTreeOnError: *event != "pull_request"`,
		`select-packages failed; see its error above`,
	} {
		if !strings.Contains(verb, want) {
			t.Errorf("tools/ci/sel_matrix.go does not contain %q: pull_request fails the job on a go list failure, merge_group and push fall back to the whole tree", want)
		}
	}
	if !strings.Contains(verb, "return 1") {
		t.Error("tools/ci/sel_matrix.go never exits 1 on a failed selection")
	}
}
