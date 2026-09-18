package ci

// A test file whose NAME ends in a GOOS or GOARCH is compiled on that platform and NOWHERE
// ELSE. Go applies the constraint from the filename alone, with nothing in the file to say
// so and no warning anywhere: `go test ./...` on linux prints `ok` for a package whose
// darwin-named test file it never built.
//
// That is how `fill_darwin_test.go` got here on 2026-09-18. It was named after the SUBJECT
// -- the darwin edges of `nova-pulse fill` -- and it ran on no bench in this fleet's linux
// lane. Every local run was green; the first thing to compile it was the merge group's own
// darwin shard, which went red on an assertion that had never been executed.
//
// The rule closes the gap without taking the constraint away from the files that mean it:
// a platform-named test file must SAY it is platform-specific, with an explicit `//go:build`
// line. Thirteen of the fourteen already did. A file that means the constraint writes one
// line; a file that does not, like that one, is renamed and runs everywhere -- which is
// where a test that is not about the platform belongs.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// implicitPlatformSuffixes is Go's own list, as `go/build` reads a filename: the last
// `_<word>` before `.go` (or before `_test.go`) is a constraint when the word is a known
// GOOS or GOARCH. These are the ones this repo could plausibly hit; a word outside the list
// is just part of a name.
var implicitPlatformSuffixes = []string{
	"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js",
	"linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos",
	"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le", "mipsle",
	"ppc64", "ppc64le", "riscv64", "s390x", "sparc64", "wasm",
}

// TestAPlatformNamedTestFileSaysSo walks every _test.go in the repo and refuses one whose
// name carries an implicit GOOS or GOARCH constraint without an explicit //go:build line
// declaring it.
func TestAPlatformNamedTestFileSaysSo(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		suffix := platformSuffix(strings.TrimSuffix(name, "_test.go"))
		if suffix == "" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if hasBuildLine(string(raw)) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		violations = append(violations, fmt.Sprintf(
			"%s is compiled only on %s, because Go reads the constraint out of the filename and nothing in the file says so; add a `//go:build %s` line if that is meant, or rename the file (drop the `_%s`) so it runs everywhere",
			filepath.ToSlash(rel), suffix, suffix, suffix))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// platformSuffix answers the GOOS or GOARCH a filename's last `_<word>` names, or "".
func platformSuffix(stem string) string {
	i := strings.LastIndexByte(stem, '_')
	if i < 0 || i == len(stem)-1 {
		return ""
	}
	word := stem[i+1:]
	for _, p := range implicitPlatformSuffixes {
		if word == p {
			return p
		}
	}
	return ""
}

// hasBuildLine reports whether the file carries an explicit build constraint, in either
// spelling, before its package clause.
func hasBuildLine(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//go:build ") || strings.HasPrefix(t, "// +build ") {
			return true
		}
		if strings.HasPrefix(t, "package ") {
			return false
		}
	}
	return false
}

// TestThePlatformSuffixReaderReadsWhatItClaims holds the walker against hand-written names,
// so a rule that quietly stopped matching anything cannot pass as a green run.
func TestThePlatformSuffixReaderReadsWhatItClaims(t *testing.T) {
	for _, c := range []struct{ stem, want string }{
		{"fill_darwin", "darwin"},
		{"wrap_linux", "linux"},
		{"lock_windows", "windows"},
		{"bench_arm64", "arm64"},
		{"filldarwin", ""},
		{"power_proc_unix", ""},
		{"fill", ""},
		{"fill_", ""},
	} {
		if got := platformSuffix(c.stem); got != c.want {
			t.Errorf("platformSuffix(%q) = %q, want %q", c.stem, got, c.want)
		}
	}
	if hasBuildLine("package bus\n\n//go:build windows\n") {
		t.Error("a build line after the package clause is not a build constraint")
	}
	if !hasBuildLine("//go:build windows\n\npackage bus\n") {
		t.Error("an explicit //go:build line was not read")
	}
}
