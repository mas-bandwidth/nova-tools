package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	register(verb{
		name:    "build",
		summary: "build every shipped tool for one platform, named the way a release ships it",
		help: `usage: go run ./tools/ghrelease build [--require-v-tag] <stamp> <goos> <goarch> <out-dir>

Builds every cmd/*/ tool for <goos>/<goarch> into
<out-dir>/<tool>_<stamp>_<goos>_<goarch>[.exe], from the repository root.
Exit 0 built; 1 refused or the build failed; 2 usage.

release.yml's build matrix and certification.yml's release-build matrix both run
this verb and nothing else to compile, so the dry run builds exactly what the
release builds: the same tool set, the same flags, the same names. One leg per
platform, in parallel, each under the two-minute cap; ghrelease sums then checks
and sums the whole set on one machine.

The tool set is cmd/*/, walked rather than listed, so a tool added to the
repository ships without anyone editing this verb. The platform must be a line of release-targets. The
ldflags are composed by "ghrelease ldflags", which refuses a bad stamp once,
before anything is compiled. -trimpath so a binary holds no path from the
runner; CGO_ENABLED=0 so it runs on any machine of its platform.

One go build over every tool, not one per tool: the package graph is loaded once
and the compiles run in parallel. The binaries go to a scratch directory and are
renamed into the release names.

--require-v-tag refuses a stamp that does not begin with v: the release shape
the version stamp assumes.
`,
		do: doBuild,
	})
}

func doBuild(e env, args []string) int {
	requireV := false
	var pos []string
	for _, a := range args {
		if a == "--require-v-tag" {
			requireV = true
			continue
		}
		pos = append(pos, a)
	}
	if len(pos) != 4 {
		fmt.Fprintf(e.stderr, "usage: %s build [--require-v-tag] <stamp> <goos> <goarch> <out-dir>\n", tool)
		fmt.Fprintln(e.stderr, "  builds every cmd/*/ tool for <goos>/<goarch> into <out-dir>/<tool>_<stamp>_<goos>_<goarch>[.exe]")
		return 2
	}
	stamp, t, out := pos[0], target{pos[1], pos[2]}, pos[3]

	if requireV && !strings.HasPrefix(stamp, "v") {
		fmt.Fprintf(e.stdout, "refusing: %s is not a v-prefixed tag\n", stamp)
		return 1
	}

	targets, err := e.shipped()
	if err != nil {
		fmt.Fprintf(e.stderr, "%s build: %v\n", tool, err)
		return 2
	}
	if !containsTarget(targets, t) {
		fmt.Fprintf(e.stderr, "refusing: %s/%s is not a shipped platform (release-targets)\n", t.goos, t.goarch)
		return 1
	}

	ldflags, refusal := composeLdflags(stamp)
	if refusal != nil {
		for _, l := range refusal {
			fmt.Fprintln(e.stderr, l)
		}
		return 1
	}
	fmt.Fprintf(e.stdout, "ldflags: %s\n", ldflags)

	names, err := toolNames(e.root(), "cmd")
	if err != nil || len(names) == 0 {
		fmt.Fprintln(e.stderr, "cmd/ matched nothing: this release would ship an empty set")
		return 1
	}
	pkgs := make([]string, len(names))
	for i, n := range names {
		pkgs[i] = "./cmd/" + n
	}

	scratch, err := os.MkdirTemp("", "ghrelease-build-")
	if err != nil {
		fmt.Fprintf(e.stderr, "%s build: no scratch directory: %v\n", tool, err)
		return 1
	}
	defer os.RemoveAll(scratch)

	rc := e.runner().Stream(command{
		dir:  e.dir,
		env:  []string{"GOOS=" + t.goos, "GOARCH=" + t.goarch, "CGO_ENABLED=0"},
		name: "go",
		args: append([]string{"build", "-trimpath", "-ldflags", ldflags, "-o", scratch + string(os.PathSeparator)}, pkgs...),
	}, e.stdout, e.stderr)
	if rc != 0 {
		return rc
	}

	outDir := out
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(e.root(), outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(e.stderr, "%s build: %v\n", tool, err)
		return 1
	}
	for _, n := range names {
		built := filepath.Join(scratch, n+t.ext())
		if fi, err := os.Stat(built); err != nil || !fi.Mode().IsRegular() {
			fmt.Fprintf(e.stderr, "go build wrote no binary for %s\n", n)
			return 1
		}
		if err := moveFile(built, filepath.Join(outDir, artifactName(n, stamp, t))); err != nil {
			fmt.Fprintf(e.stderr, "%s build: %v\n", tool, err)
			return 1
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, artifactName("nova-bus", stamp, t))); err != nil {
		fmt.Fprintln(e.stderr, "nova-bus is not in the shipped set")
		return 1
	}
	fmt.Fprintf(e.stdout, "built %d tools for %s/%s\n", len(names), t.goos, t.goarch)
	if err := listDir(e.stdout, outDir); err != nil {
		fmt.Fprintf(e.stderr, "%s build: listing %s: %v\n", tool, outDir, err)
		return 1
	}
	return 0
}

func containsTarget(ts []target, t target) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

// moveFile renames src to dst, copying across a filesystem boundary when a
// rename cannot cross it (the scratch directory is the system's temp directory).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	outf, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(outf, in); err != nil {
		outf.Close()
		return err
	}
	if err := outf.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// listDir prints the directory the way a person reads a release's output: one
// line per entry, mode, size, name. A directory it cannot read, or an entry it
// cannot stat, is an error: the build just wrote them, so either is a fault.
func listDir(w io.Writer, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		fi, err := ent.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s %10d %s\n", fi.Mode(), fi.Size(), ent.Name())
	}
	return nil
}
