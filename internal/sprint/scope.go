package sprint

import (
	"path"
	"slices"
	"strings"
)

// The scope amendment (docs/SPEC-SPRINT.md section 7, the lander's checks): a worker
// whose change needs a file its brief's PATHS does not name asks for it; when that file
// is the test, the fixture or the doc of the same change, the answer is a rule, not a
// message: allowed, and recorded in the land report beside the card (land's scope=),
// so nobody waits on a coordinator for it. Anything else outside PATHS is refused as
// before (E12).

// ScopeAmended splits outside, the files a card's diff changes that its PATHS do not
// name, into amended, each a test, fixture or doc of the same change, and refused, the
// rest, each in the order given. changed is every file the diff changes. A file is of
// the same change when the change also changes a file of its own (in PATHS, not in
// outside) and the file is one of:
//   - a Go test file (_test.go) in the directory of one of those files: the package's test;
//   - a file under the testdata directory of one of those directories: its fixture or golden;
//   - a Markdown file under docs/, or in the directory of one of those files: its doc.
//
// A file that is code, configuration or anything else is never amended.
func ScopeAmended(changed, outside []string) (amended, refused []string) {
	dirs := map[string]bool{}
	for _, f := range changed {
		if !slices.Contains(outside, f) {
			dirs[path.Dir(f)] = true
		}
	}
	for _, f := range outside {
		if len(dirs) > 0 && sameChange(f, dirs) {
			amended = append(amended, f)
			continue
		}
		refused = append(refused, f)
	}
	return amended, refused
}

// sameChange says f is the test, fixture or doc of a change to files in dirs.
func sameChange(f string, dirs map[string]bool) bool {
	dir := path.Dir(f)
	switch {
	case strings.HasSuffix(f, "_test.go"):
		return dirs[dir]
	case strings.HasSuffix(f, ".md") && (strings.HasPrefix(f, "docs/") || dirs[dir]):
		return true
	}
	for d := range dirs {
		if strings.HasPrefix(f, path.Join(d, "testdata")+"/") {
			return true
		}
	}
	return false
}
