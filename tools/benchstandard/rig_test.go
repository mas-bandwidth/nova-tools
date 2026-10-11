package main

import (
	"os"
	"path/filepath"
)

// The rig of tools/benchstandard (STANDARD.md section 8): bench and fakeHost,
// with their constructors emptyBench and conformingBench and the drift-line
// readers, are the rig bench_test.go lays down. This file holds the scenarios
// and checkers the package's shapes share; each serves at least two of them,
// and the general mechanics stay in pkg/testkit where they fit.

// toolOnPath installs tool at at (relative to the home) with its directory
// first on PATH, and returns the tool's path.
func (b *bench) toolOnPath(tool, at string) string {
	b.t.Helper()
	full := b.write(at, "#!/bin/sh\necho 'stub "+tool+" 0.0'\n", true)
	b.setEnv("PATH=" + filepath.Dir(full))
	return full
}

// linkedTool installs tool at at (relative to the home) and links it into the
// PATH directory, returning the file the link resolves to: a wall check judges
// a linked tool by where it really lives.
func (b *bench) linkedTool(tool, at string) string {
	b.t.Helper()
	real := b.write(at, "#!/bin/sh\n", true)
	if err := os.Symlink(real, filepath.Join(b.bin, tool)); err != nil {
		b.t.Fatal(err)
	}
	return real
}

// wantWallLines runs the witness and requires tool to have drawn exactly want
// wall-toolchain DRIFT lines, naming the path it sits at; it returns the lines.
func (b *bench) wantWallLines(tool, path string, want int) []string {
	b.t.Helper()
	_, output := b.standard()
	lines := driftWith(output, tool+" on PATH is ")
	if len(lines) != want {
		b.t.Fatalf("the %s at %s drew %d wall lines, want %d:\n%s", tool, path, len(lines), want, output)
	}
	return lines
}
