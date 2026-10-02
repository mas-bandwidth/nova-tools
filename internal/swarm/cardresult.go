package swarm

// A card's published result and its log's size: the two reads `native` makes of a job
// directory while and after a card runs (native.go, nativeidle.go), and the names a job
// directory's clone and the sandbox binary go by.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// resultLiftDepth is how far below the job root the lookup looks for a result the card wrote
// somewhere else: repo/RESULT.md, and one directory down from there -- repo/<clone>/RESULT.md,
// the cwd of a model that cloned into its clone. Deeper is not searched: a result further
// down than that is a file the card left behind, not the result it published.
const resultLiftDepth = 2

// FindCardResult is THE ONE PLACE a card's published result is looked for: the job root
// first (ResultPath), then `repo/` and one
// directory below it. A path that is not a regular file is not a result: a
// planted symlink is not followed and a FIFO is not a published report. It is
// exported because `native` asks whether the harness published anything at all.
// A framed card's finish that the gh shim recorded (cardcontract.FinishName, in the card
// contract's shape) is a published result too, after the job root's RESULT.md: a child
// whose `gh pr create` or `gh pr review` is its end published (docs/SPEC-CARD-CONTRACT.md).
func FindCardResult(job string) (string, bool) {
	if root := ResultPath(job); isRegularFile(root) {
		return root, true
	}
	if finish := filepath.Join(job, cardcontract.FinishName); isRegularFile(finish) {
		if b, err := os.ReadFile(finish); err == nil && typedrec.ParseCardResult(b).Shaped {
			return finish, true
		}
	}
	return findResultBelow(job, resultLiftDepth)
}

// findResultBelow is the first RESULT.md under dir, breadth first and in name order, no
// deeper than depth directories down: repo/ is looked at before any other name, because
// repo/ is the directory the card's own STEP 1 makes. A symlinked directory is not followed
// -- the wall is not a wall if the thing outside it will fetch (regular.go).
func findResultBelow(dir string, depth int) (string, bool) {
	if depth <= 0 {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	// repo/ first, then the rest in the order the directory was read (ReadDir sorts by name).
	if i := slices.Index(dirs, "repo"); i >= 0 {
		dirs = append([]string{"repo"}, append(dirs[:i:i], dirs[i+1:]...)...)
	}
	for _, name := range dirs {
		if p := filepath.Join(dir, name, "RESULT.md"); isRegularFile(p) {
			return p, true
		}
	}
	for _, name := range dirs {
		if p, ok := findResultBelow(filepath.Join(dir, name), depth-1); ok {
			return p, true
		}
	}
	return "", false
}

// logSize is the byte length of a card's log file, or zero when the file is not there yet.
func logSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// JobRepo is the clone under a job directory: <job>/repo, the name the result lookup
// above tries first.
const JobRepo = "repo"

// SandboxBinary is the name `native` looks for on PATH when --sandbox names no path. It is
// the tool's OWN name and not a path: a default PATH lookup of a named command is what this
// tool already does for the harness, and no directory is guessed.
const SandboxBinary = "nova-sandbox"
