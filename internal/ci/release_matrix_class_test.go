package ci

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// release_matrix_class_test.go holds the release build and its dry run to one
// shape under the two-minute cap: one leg per shipped platform, each running
// `go run ./tools/ghrelease build`, then one job that downloads every leg's
// artifact and sums the whole set (`ghrelease sums`; in the release, `ghrelease
// attach`, which sums and then attaches). One runner cross-building every
// platform was cancelled by the cap inside the build (certification run
// 36357749379, 2026-09-27).

const (
	ghreleaseBuild  = "go run ./tools/ghrelease build"
	ghreleaseStamp  = "go run ./tools/ghrelease stamp"
	ghreleaseSums   = "go run ./tools/ghrelease sums"
	ghreleaseAttach = "go run ./tools/ghrelease attach"
	setupGoPrefix   = "actions/setup-go@"
)

type releaseStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

type releaseJob struct {
	Needs    any `yaml:"needs"`
	Strategy struct {
		Matrix struct {
			Include []map[string]string `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []releaseStep `yaml:"steps"`
}

func releaseWorkflowJobs(t *testing.T, file string) map[string]releaseJob {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", file))
	var wf struct {
		Jobs map[string]releaseJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return wf.Jobs
}

// releaseTargets reads tools/ghrelease/release-targets, the file the tool
// embeds: one "<goos> <goarch>" per line, # comments and blank lines skipped.
func releaseTargets(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(readFile(t, filepath.Join(repoRoot(t), "tools", "ghrelease", "release-targets")), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("release-targets: %q is not \"<goos> <goarch>\"", line)
		}
		out = append(out, f[0]+" "+f[1])
	}
	sort.Strings(out)
	return out
}

func matrixTargets(job releaseJob) []string {
	var out []string
	for _, leg := range job.Strategy.Matrix.Include {
		out = append(out, leg["goos"]+" "+leg["goarch"])
	}
	sort.Strings(out)
	return out
}

func needsJob(needs any, name string) bool {
	switch v := needs.(type) {
	case string:
		return v == name
	case []any:
		for _, n := range v {
			if n == name {
				return true
			}
		}
	}
	return false
}

// TestReleaseMatricesAreTheTargetsFile: release.yml's `build` and
// certification.yml's `release-build` are one leg per line of release-targets,
// the same list; the only compile in either is `ghrelease build`, after a Go
// setup; each leg uploads `release-<goos>-<goarch>`.
func TestReleaseMatricesAreTheTargetsFile(t *testing.T) {
	t.Parallel()

	want := releaseTargets(t)
	if len(want) == 0 {
		t.Fatal("release-targets names no platform; a release would ship nothing")
	}
	for _, c := range []struct{ file, job string }{
		{"release.yml", "build"},
		{"certification.yml", "release-build"},
	} {
		job, ok := releaseWorkflowJobs(t, c.file)[c.job]
		if !ok {
			t.Errorf("%s has no %s job; the release build is one leg per platform", c.file, c.job)
			continue
		}
		if got := matrixTargets(job); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s %s matrix is %v, release-targets is %v; the three lists are one list", c.file, c.job, got, want)
		}
		builds, uploads := 0, 0
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "go build") {
				t.Errorf("%s %s step %q runs go build; the only compile is ghrelease build, so the dry run builds what the release builds", c.file, c.job, s.Name)
			}
			if strings.Contains(s.Run, ghreleaseBuild) &&
				strings.Contains(s.Run, "${{ matrix.goos }}") && strings.Contains(s.Run, "${{ matrix.goarch }}") {
				builds++
			}
			if strings.HasPrefix(s.Uses, "actions/upload-artifact@") && s.With["name"] == "release-${{ matrix.goos }}-${{ matrix.goarch }}" {
				uploads++
			}
			if c.file == "release.yml" && strings.HasPrefix(s.Uses, "actions/cache/save@") {
				t.Errorf("release.yml %s saves a build cache; a release restores the certification entry and writes nothing but its release", c.job)
			}
		}
		if builds != 1 {
			t.Errorf("%s %s runs ghrelease build for its leg %d times, want 1", c.file, c.job, builds)
		}
		if uploads != 1 {
			t.Errorf("%s %s uploads release-<goos>-<goarch> %d times, want 1", c.file, c.job, uploads)
		}
	}
}

// TestReleaseSumsAreOneMachineOverTheWholeSet: release.yml's `release` and
// certification.yml's `release-dry-run` need the build legs, download every
// `release-*` artifact into one directory, assert the stamp, then sum the set:
// `ghrelease sums` in the dry run, and in release.yml `ghrelease attach`, the
// step that sums the set and attaches it, so the sums and the attach are one
// step.
func TestReleaseSumsAreOneMachineOverTheWholeSet(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ file, job, build, sumsCall string }{
		{"release.yml", "release", "build", ghreleaseAttach},
		{"certification.yml", "release-dry-run", "release-build", ghreleaseSums},
	} {
		job, ok := releaseWorkflowJobs(t, c.file)[c.job]
		if !ok {
			t.Errorf("%s has no %s job", c.file, c.job)
			continue
		}
		if !needsJob(job.Needs, c.build) {
			t.Errorf("%s %s does not need %s; it would sum a set nobody built", c.file, c.job, c.build)
		}
		download, stamp, sums := -1, -1, -1
		for i, s := range job.Steps {
			if strings.HasPrefix(s.Uses, "actions/download-artifact@") && s.With["pattern"] == "release-*" && s.With["merge-multiple"] == "true" {
				download = i
			}
			if strings.Contains(s.Run, ghreleaseStamp) {
				stamp = i
			}
			if strings.Contains(s.Run, c.sumsCall) {
				sums = i
			}
		}
		if download < 0 || stamp < 0 || sums < 0 || !(download < stamp && stamp < sums) {
			t.Errorf("%s %s: download every release-* artifact (step %d), assert the stamp (step %d), then `%s` (step %d), in that order", c.file, c.job, download, stamp, c.sumsCall, sums)
		}
	}
}

// TestEveryJobThatRunsGhreleaseSetsUpGoFirst: a workflow step that calls
// `go run ./tools/ghrelease` needs a Go toolchain on PATH, and the hosted
// runner has none it can be trusted to pin: the job carries the pinned
// actions/setup-go before its first call, so the release runs the toolchain
// go.mod names and not whatever the image holds.
func TestEveryJobThatRunsGhreleaseSetsUpGoFirst(t *testing.T) {
	t.Parallel()

	calls := 0
	for _, file := range []string{"release.yml", "certification.yml"} {
		for name, job := range releaseWorkflowJobs(t, file) {
			setup := -1
			for i, s := range job.Steps {
				if strings.HasPrefix(s.Uses, setupGoPrefix) && s.With["go-version-file"] == "go.mod" && setup < 0 {
					setup = i
				}
				if strings.Contains(s.Run, "go run ./tools/ghrelease") {
					calls++
					if setup < 0 || setup > i {
						t.Errorf("%s %s step %q runs ghrelease with no actions/setup-go (go-version-file: go.mod) before it", file, name, s.Name)
					}
				}
			}
		}
	}
	if calls == 0 {
		t.Fatal("no workflow step runs ghrelease; the release has lost its tool")
	}
}

// TestNoWorkflowStepRunsAReleaseScript: the release's logic is tools/ghrelease,
// under unit tests; a workflow step that names a shell script for it is the
// logic back in a place no test reaches.
func TestNoWorkflowStepRunsAReleaseScript(t *testing.T) {
	t.Parallel()

	for _, file := range []string{"release.yml", "certification.yml"} {
		for name, job := range releaseWorkflowJobs(t, file) {
			for _, s := range job.Steps {
				if strings.Contains(s.Run, ".github/scripts/release-") || strings.Contains(s.Run, ".github/scripts/assert-version-stamp") {
					t.Errorf("%s %s step %q runs a release shell script; the release verbs are tools/ghrelease", file, name, s.Name)
				}
			}
		}
	}
}
