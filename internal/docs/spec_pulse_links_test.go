package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// spec_pulse_links_test.go holds the section files under docs/spec-pulse/
// against the links they carry. docs/spec-pulse/ is one file per section of
// docs/SPEC-PULSE.md (#560); the bodies were cut out of the top file and
// pasted into their own files, and each link moved with its section without
// being re-based. A link resolves relative to the file the link stands in,
// never to the top file, so a link copied verbatim from docs/SPEC-PULSE.md
// points one directory too shallow. The one relative target that is not a
// path is an anchor-only `#...` target, which names a heading in the same
// document and so has nothing on disk to stat.

// specPulseDir is the section directory, relative to this package.
const specPulseDir = "../../docs/spec-pulse"

// specPulseLinkRe reads the inline link target out of `](<target>)`, with an
// optional title after the target.
var specPulseLinkRe = regexp.MustCompile(`\]\(([^)\s]+)(?:\s[^)]*)?\)`)

// TestEverySpecPulseSectionLinkResolves is the section files' link contract.
func TestEverySpecPulseSectionLinkResolves(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(specPulseDir)
	if err != nil {
		t.Fatalf("%s: %v; docs/spec-pulse/ holds one file per section of docs/SPEC-PULSE.md (#560)", specPulseDir, err)
	}
	var sections []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		sections = append(sections, entry.Name())
	}
	if len(sections) < 2 {
		t.Fatalf("%s: %d markdown files; docs/spec-pulse/ holds one file per section of docs/SPEC-PULSE.md (#560) and there are many more than two", specPulseDir, len(sections))
	}

	broken, err := specPulseBrokenLinks(specPulseDir)
	if err != nil {
		t.Fatalf("%s: %v; docs/spec-pulse/ holds one file per section of docs/SPEC-PULSE.md (#560)", specPulseDir, err)
	}
	for _, finding := range broken {
		t.Errorf("%s", finding)
	}
}

// specPulseBrokenLinks scans the markdown files in dir and returns one
// message for every inline link whose written target does not resolve
// relative to the file the link stands in.
func specPulseBrokenLinks(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var broken []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(string(body), "\n") {
			for _, m := range specPulseLinkRe.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if strings.HasPrefix(target, "http://") ||
					strings.HasPrefix(target, "https://") ||
					strings.HasPrefix(target, "mailto:") {
					continue
				}
				statTarget := target
				if cut := strings.IndexAny(statTarget, "#?"); cut >= 0 {
					statTarget = statTarget[:cut]
				}
				if statTarget == "" {
					continue
				}
				resolved := filepath.Join(filepath.Dir(path), statTarget)
				if _, err := os.Stat(resolved); err != nil {
					broken = append(broken, fmt.Sprintf("%s:%d: link target %q resolves to %s, which does not exist; a section file's link is relative to the file it stands in, never to docs/SPEC-PULSE.md (#560), so %q must be re-based onto %s",
						path, i+1, target, resolved, target, filepath.Dir(path)))
				}
			}
		}
	}
	return broken, nil
}
