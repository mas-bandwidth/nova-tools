package sandbox

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An OPTIONAL ROOT's ancestors were granted nothing, and a toolchain under one could not
// resolve its own path.
//
// Measured on the Studio, 2026-09-18, dogfooding `nova-sandbox run` on a real card step:
// a `go build` inside the wall died with Go's own message and nothing else —
//
//	go: cannot find GOROOT directory: 'go' binary is trimmed and GOROOT is not set
//
// The profile granted `(allow file-read* (subpath "/opt/homebrew"))`, so every file of the
// toolchain WAS readable. What was not readable was `/opt`: the `file-read-metadata`
// ancestor literals are built from the caller's `--read`, `--write`, `--cwd` and `--tmp`
// and from NOTHING ELSE, so an optional root that the tool itself added had no ancestors
// at all. Homebrew's `go` is built with `-trimpath`, so it finds GOROOT by resolving its
// own executable — `/opt/homebrew/bin/go` is a symlink into `../Cellar/...`, resolving it
// lstats every leading component, and the lstat of `/opt` was denied. A wall that grants a
// directory and denies the path TO it grants nothing a symlink has to be followed to reach.
//
// The card that hit it worked around it with `--read /opt/homebrew/Cellar/go/1.27.1`, which
// looks like a read grant and is really an ANCESTOR grant: naming any path under /opt adds
// /opt to the literals. That is the whole of the repair, and it belongs in the table rather
// than in every caller's argv.
func TestAnOptionalRootsAncestorsAreGranted(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: the optional-root table and its ancestors are the darwin profile's", runtime.GOOS)
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	p := &Policy{
		Writes:   []string{base},
		Cwd:      base,
		Tmp:      base,
		Home:     base,
		OptRoots: []string{"/opt/homebrew"},
		Command:  "/bin/sh",
		Argv:     []string{"/bin/sh", "-c", "true"},
	}
	got := Ancestors(p.ancestorPaths()...)
	if !hasPath(got, "/opt") {
		t.Fatalf("/opt is not among the file-read-metadata ancestors for an /opt/homebrew root: %v\n"+
			"a toolchain under an optional root resolves its own executable, and resolving a symlink lstats every leading component; without /opt the lstat is denied and the tool reports a fault of its own that names nothing about the wall", got)
	}

	text, _, err := DarwinProfile(p)
	if err != nil {
		t.Fatalf("the profile could not be generated: %s", err)
	}
	if !strings.Contains(text, `(allow file-read-metadata (literal "/opt"))`) {
		t.Errorf("the generated profile carries no file-read-metadata literal for /opt, so the grant above never reaches the kernel")
	}
	// The count on the SANDBOX OK line is the same set, or a reader comparing the line
	// with the profile is comparing two different things.
	if p.AncestorCount() != len(got) {
		t.Errorf("ancestors=%d on the OK line is not the %d literals the profile emits", p.AncestorCount(), len(got))
	}
}

// The ancestors of an optional root are METADATA only, exactly as every other ancestor is:
// stat, never a listing. A subpath grant on /opt would hand the wall every other thing
// installed there, which is the opposite of two lists and no defaults.
func TestAnOptionalRootsAncestorIsMetadataOnly(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: the optional-root table is the darwin profile's", runtime.GOOS)
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	p := &Policy{
		Writes: []string{base}, Cwd: base, Tmp: base, Home: base,
		OptRoots: []string{"/opt/homebrew"},
		Command:  "/bin/sh", Argv: []string{"/bin/sh"},
	}
	text, _, err := DarwinProfile(p)
	if err != nil {
		t.Fatalf("the profile could not be generated: %s", err)
	}
	if strings.Contains(text, `(allow file-read* (subpath "/opt"))`) {
		t.Errorf("the profile grants a read subpath on /opt; the ancestor grant is file-read-metadata and nothing wider")
	}
}

func hasPath(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
