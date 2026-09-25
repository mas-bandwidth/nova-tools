package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// scratch_tracked_test.go holds the 2026-09-18 junk-file incident as a rule.
// The incident left 15 files tracked under scratch/ — one a 3.6 MB compiled
// binary — and .gitignore's `scratch/` line never reached them, because
// .gitignore does not apply to a path git already tracks, so they stay in every
// clone for ever. The rule, therefore, runs where the tree is: no tracked path
// under scratch/, and no tracked file over 1 MB outside the tree's own homes
// for binary content (testdata/ fixtures and assets/ images). It reads
// `git ls-files` from the repository root, reached the way its neighbours here
// reach it: filepath.Join("..", "..").

// scratchPrefix is the directory the incident dirtied; nothing under it may be
// tracked.
const scratchPrefix = "scratch/"

// oneMegabyte is the cap a tracked file may not pass outside a sanctioned home.
const oneMegabyte = 1 << 20

// sanctionedHomes are the tree's own directories for binary content: testdata/
// holds fixtures, and assets/ holds images, one line each in
// docs/ASSET-PROVENANCE.md. A file under one of them is there on purpose; the
// junk this guard exists to refuse has neither home.
var sanctionedHomes = []string{"testdata/", "assets/"}

// TestNoTrackedScratchPathOrOversizedFile is the guard. It fails once for every
// tracked path under scratch/ and once for every tracked file over the cap that
// sits outside a sanctioned home, naming each path so a friend meeting the red
// reads the fix off the refusal.
func TestNoTrackedScratchPathOrOversizedFile(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files from the repository root: %v", err)
	}

	var scratch []string
	var oversized []oversizedFile
	for _, path := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		if strings.HasPrefix(path, scratchPrefix) {
			scratch = append(scratch, path)
		}
		if underHome(path) {
			continue
		}
		info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if statErr != nil {
			continue
		}
		if info.Size() > oneMegabyte {
			oversized = append(oversized, oversizedFile{path: path, size: info.Size()})
		}
	}
	sort.Strings(scratch)
	sort.Slice(oversized, func(i, j int) bool { return oversized[i].path < oversized[j].path })

	for _, path := range scratch {
		t.Errorf("git tracks %s under scratch/; .gitignore never applies to a path git already tracks, so it stays in every clone for ever — run git rm --cached -r scratch/", path)
	}
	for _, f := range oversized {
		t.Errorf("git tracks %s at %d bytes, over the %d-byte cap, outside %s", f.path, f.size, oneMegabyte, strings.Join(sanctionedHomes, " and "))
	}
}

// oversizedFile is a tracked file over the cap, with its size for the message.
type oversizedFile struct {
	path string
	size int64
}

// underHome reports whether path sits under one of the tree's sanctioned homes
// for binary content.
func underHome(path string) bool {
	for _, dir := range sanctionedHomes {
		if path == strings.TrimSuffix(dir, "/") || strings.HasPrefix(path, dir) || strings.Contains(path, "/"+dir) {
			return true
		}
	}
	return false
}
