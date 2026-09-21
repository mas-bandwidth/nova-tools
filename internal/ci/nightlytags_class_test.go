package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// nightlytags_class_test.go is the class test behind the rule that a test moved
// behind a build tag still runs SOMEWHERE.
//
// A build tag is how this tree takes a test off the per-change path: `slow`
// (#516), `perf`, `novadisk`, and — since unit tests never touch the network —
// `nightly` and `soak`, the two tags CheckNet exempts because the real network
// is allowed in those suites (see ci_net.go and ci_net_test.go cases 3 and 4).
// Every one of those tags is an OPT-IN: `go test ./...` without it compiles the
// file away, silently and with no output. So a tag no scheduled job passes to
// `go test -tags` is not a slower tier, it is a deleted test that still looks
// like a test in the tree.
//
// This is the class test, not the instance: it does not name `slow` or
// `novadisk`. It reads every tag any _test.go opts into, reads every tag the
// scheduled workflows name, and refuses the difference. A tag invented tomorrow
// is covered the day its first test file lands.
//
// A scheduled workflow names a tag in one of two mechanical ways, both of which
// are what actually reaches `go test`:
//
//  1. literally, on a `go test`/`go vet` line: `go test -tags perf ./...`
//     (certification.yml's perf job), or
//  2. as a `tag:` entry of a job's matrix, which the step then expands:
//     `go test -tags ${{ matrix.tag }} ./...` (nightly-slow.yml).
//
// Platform and toolchain constraints are NOT opt-in tags and are not in scope:
// `//go:build darwin` does not hide a test, it says where it runs, and the
// hosted matrices already cover that. Nor is a negation (`!windows`), which
// is on by default everywhere else.
//
// ONE PLATFORM IS THE EXCEPTION, named here so nobody has to find it twice.
// Since 2026-09-18 the CL tier runs no native Windows leg at all (Glenn: "drop
// the native windows CI runners. WSL only from now on."), so a `//go:build
// windows` test file is COMPILED on every change — the lint job's `make
// vet-windows` type-checks the whole tree, test files included, for GOOS=windows
// — and RUN only by the certification tier's `test-windows`. That is a stated
// trade, not a tag falling quietly out of CI, and it is why `windows` stays in
// implicitTags below: it is a GOOS, it hides nothing from `go test` on a Windows
// machine, and a `-tags windows` leg was never what ran those tests.

// implicitTags are the constraints the toolchain sets by itself: the operating
// systems and architectures `go test` already fans out over, plus the ones set
// by a flag or by the build mode rather than by `-tags`. None of these hides a
// test from an ordinary `go test ./...` on the matching machine.
var implicitTags = map[string]bool{
	// GOOS, as of go1.26 (`go tool dist list`), plus the two portability
	// pseudo-tags the toolchain sets from them.
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "hurd": true, "illumos": true, "ios": true, "js": true,
	"linux": true, "nacl": true, "netbsd": true, "openbsd": true, "plan9": true,
	"solaris": true, "wasip1": true, "windows": true, "zos": true,
	"unix": true,

	// GOARCH.
	"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true,
	"mips": true, "mips64": true, "mips64le": true, "mipsle": true,
	"ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true,
	"wasm": true,

	// Set by a flag or by the build mode, never by `-tags`. `race` is the
	// `-race` flag, which TestSomeScheduledJobRunsTheRaceDetector below holds
	// a scheduled job to.
	"race": true, "cgo": true, "msan": true, "asan": true,
	"gc": true, "gccgo": true, "purego": true,
}

// goVersionTag matches the toolchain's own `go1.NN` release tags, which are set
// by the compiler and never passed with `-tags`.
var goVersionTag = regexp.MustCompile(`^go1\.\d+$`)

// buildTagsInTestFiles reads the shared tree and returns every opt-in build tag
// a _test.go carries, mapped to the files that carry it. The four directory
// names the walk used to skip are skipped here by path: .git is not in the
// shared tree at all, and testdata, vendor and node_modules hold files that are
// not this repository's own tests.
func buildTagsInTestFiles(t *testing.T) map[string][]string {
	t.Helper()
	tags := map[string][]string{}
	for _, f := range repoTree(t).Files {
		if !f.Test {
			continue
		}
		if f.HasDirNamed("testdata") || f.HasDirNamed("vendor") || f.HasDirNamed("node_modules") {
			continue
		}
		for _, line := range strings.Split(string(f.Src), "\n") {
			line = strings.TrimSpace(line)
			// A build constraint may only appear before the package clause.
			if strings.HasPrefix(line, "package ") {
				break
			}
			if !strings.HasPrefix(line, "//go:build") {
				continue
			}
			for _, tag := range optInTags(strings.TrimPrefix(line, "//go:build")) {
				tags[tag] = append(tags[tag], f.Rel)
			}
		}
	}
	return tags
}

// optInTags splits one //go:build expression into the tags that HIDE the file
// from an ordinary build: identifiers that are neither negated nor implicit.
func optInTags(expr string) []string {
	expr = strings.NewReplacer("&&", " ", "||", " ", "(", " ", ")", " ").Replace(expr)
	var out []string
	for _, word := range strings.Fields(expr) {
		if strings.HasPrefix(word, "!") {
			// `!windows` is on everywhere else: the file is not hidden.
			continue
		}
		if implicitTags[word] || goVersionTag.MatchString(word) {
			continue
		}
		out = append(out, word)
	}
	return out
}

// tagsNamedBySchedules reads .github/workflows and returns every build tag a
// SCHEDULED workflow hands to `go test`, mapped to the workflow files naming it.
func tagsNamedBySchedules(t *testing.T, root string) map[string][]string {
	t.Helper()
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`-tags[ =]+'?"?([A-Za-z0-9_,]+)`)
	matrixTag := regexp.MustCompile(`^-?\s*tag:\s*'?"?([A-Za-z0-9_]+)`)

	named := map[string][]string{}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		lines := noComments(readFile(t, filepath.Join(dir, e.Name())))
		if !hasLinePrefix(lines, "schedule:") {
			continue
		}
		for _, line := range lines {
			if strings.Contains(line, "go test") || strings.Contains(line, "go vet") {
				for _, m := range literal.FindAllStringSubmatch(line, -1) {
					for _, tag := range strings.Split(m[1], ",") {
						named[tag] = appendOnce(named[tag], e.Name())
					}
				}
			}
			if m := matrixTag.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				named[m[1]] = appendOnce(named[m[1]], e.Name())
			}
		}
	}
	return named
}

// noComments returns the workflow's lines with whole-line YAML comments
// dropped, so prose ABOUT a tag never stands in for a job that runs it.
func noComments(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func hasLinePrefix(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

func appendOnce(in []string, s string) []string {
	for _, have := range in {
		if have == s {
			return in
		}
	}
	return append(in, s)
}

// THE CLASS TEST. Every opt-in build tag in a test file is named by a scheduled
// job, so no tagged test can fall out of CI without the tree saying so.
func TestEveryTestBuildTagIsRunBySomeScheduledJob(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	used := buildTagsInTestFiles(t)
	named := tagsNamedBySchedules(t, root)

	if len(used) == 0 {
		t.Fatal("no opt-in build tag found in any _test.go; the walk is broken, not the tree")
	}

	tags := make([]string, 0, len(used))
	for tag := range used {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	for _, tag := range tags {
		if len(named[tag]) > 0 {
			continue
		}
		files := append([]string(nil), used[tag]...)
		sort.Strings(files)
		t.Errorf("build tag %q hides %d test file(s) and NO scheduled job runs it: %s\n"+
			"\tremedy: add a `tag: %s` leg to nightly-slow.yml's matrix (or `go test -tags %s` to another scheduled workflow), "+
			"or drop the tag from those files",
			tag, len(files), strings.Join(files, ", "), tag, tag)
	}
}

// The two tags CheckNet exempts are the ones where a test may touch the real
// network (ci_net.go). A network suite nobody runs is the worst of both: the
// checker waves the file through and nothing ever executes it. So these two are
// held to a scheduled job whether or not a file carries them TODAY.
func TestTheNetworkExemptTagsHaveAHomeInTheSchedule(t *testing.T) {
	root := repoRoot(t)
	named := tagsNamedBySchedules(t, root)
	for _, tag := range []string{"nightly", "soak"} {
		if len(named[tag]) == 0 {
			t.Errorf("CheckNet exempts //go:build %s, but no scheduled job runs `go test -tags %s`: "+
				"a real-network test could be written and never run once", tag, tag)
		}
	}
}

// The `race` tag is set by the -race flag rather than by -tags, so the class
// test above treats it as implicit. That is only true while something scheduled
// actually passes -race.
func TestSomeScheduledJobRunsTheRaceDetector(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		lines := noComments(readFile(t, filepath.Join(dir, e.Name())))
		if !hasLinePrefix(lines, "schedule:") {
			continue
		}
		for _, line := range lines {
			if strings.Contains(line, "go test") && strings.Contains(line, "-race") {
				return
			}
		}
	}
	t.Error("no scheduled workflow runs `go test -race`, so //go:build race and //go:build !race " +
		"files are no longer both covered")
}
