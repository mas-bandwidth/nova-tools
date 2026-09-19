package pulse

// The typed card header: five lines under the contract line and inside its hash
// (SPEC-TOOLWORK.md §5 rule 1), written by `cut` from the pool row and never by a model.
//
//	KIND: <kind>
//	PATHS: <glob>[, <glob>...]
//	TEST: <package> <TestName>     (or `TEST: none` where the kind allows it)
//	LEGS: <leg>[,<leg>...]
//	SOURCE: <owner>/<repo>#<n> | <file:line at the pinned head>
//	TEST-EDIT: <file>              (optional, repeatable: the eligibility rule, 11)
//
// `accept` reads these from the card file `cut` wrote and from nothing else: not from
// RESULT.md, whose kind, test or paths a worker could widen (§1 rule 2, §5 rule 6).
// T03 (#1648) reads them; T06 (#1651) makes `cut` write them and `lint --card` check
// them.

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// CardHeader is the typed header as read. Absent lines are empty; Missing names them.
type CardHeader struct {
	Label    string // the word after RESULT on line 1
	Kind     string
	Paths    []string
	TestPkg  string // repo-relative package directory; empty with TestNone
	TestName string
	TestNone bool // `TEST: none`
	HasTest  bool // a TEST: line was present at all
	Legs     []string
	Source   string
	// TestEdits are the pre-existing test files the card writer excused by name. A
	// deleted test file is never excused, whatever this holds.
	TestEdits []string
}

var (
	headerLine = regexp.MustCompile(`^([A-Z][A-Z-]*):\s*(.*)$`)
	testName   = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
)

// ReadCardHeader reads line 1 and the header lines that follow it. It stops at the first
// non-empty line that is not `KEY: value`, which is where the card's prose begins. It
// refuses a value the gate could not use safely: a PATHS: that fails hyg.ValidatePaths,
// a TEST: whose package climbs out of the repository or whose name is not a Go test name.
func ReadCardHeader(path string) (CardHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return CardHeader{}, fmt.Errorf("card %s: %v", path, err)
	}
	defer f.Close()
	var h CardHeader
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	seen := map[string]bool{}
	first := true
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if first {
			first = false
			h.Label = contractLabel(line)
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := headerLine.FindStringSubmatch(line)
		if m == nil {
			break
		}
		key, val := m[1], strings.TrimSpace(m[2])
		// A repeated key is refused. It used to be last-wins for KIND:, TEST:, LEGS: and
		// SOURCE: and accumulating for PATHS:, which is a header a card writer can aim
		// at one reader and hide from the other -- the gate and `lint --card` would each
		// pick one (the red team of 98e3f3a9, item 7). TEST-EDIT: repeats because the
		// spec says it repeats.
		if key != "TEST-EDIT" {
			if seen[key] {
				return h, fmt.Errorf("card %s: %s: appears more than once; one line each, so the gate and lint --card cannot read the header two ways", path, key)
			}
			seen[key] = true
		}
		switch key {
		case "KIND":
			h.Kind = val
		case "PATHS":
			if val != "none" {
				for _, p := range strings.Split(val, ",") {
					if p = strings.TrimSpace(p); p != "" {
						h.Paths = append(h.Paths, p)
					}
				}
			}
		case "TEST":
			h.HasTest = true
			if val == "none" {
				h.TestNone = true
				continue
			}
			fields := strings.Fields(val)
			if len(fields) != 2 {
				return h, fmt.Errorf("card %s: TEST: wants `<package> <TestName>` or `none`, got %q", path, val)
			}
			h.TestPkg, h.TestName = strings.Trim(fields[0], "/"), fields[1]
			if h.TestPkg == "" {
				h.TestPkg = "."
			}
			if err := hyg.ValidatePaths([]string{h.TestPkg}); err != nil {
				return h, fmt.Errorf("card %s: TEST: package: %v", path, err)
			}
			if !testName.MatchString(h.TestName) {
				return h, fmt.Errorf("card %s: TEST: %q is not a Go test name", path, h.TestName)
			}
		case "LEGS":
			for _, l := range strings.Split(val, ",") {
				if l = strings.TrimSpace(l); l != "" {
					h.Legs = append(h.Legs, l)
				}
			}
		case "SOURCE":
			h.Source = val
		case "TEST-EDIT":
			if err := hyg.ValidatePaths([]string{val}); err != nil {
				return h, fmt.Errorf("card %s: TEST-EDIT: %v", path, err)
			}
			h.TestEdits = append(h.TestEdits, val)
		}
	}
	if err := sc.Err(); err != nil {
		return h, fmt.Errorf("card %s: %v", path, err)
	}
	if len(h.Paths) > 0 {
		if err := hyg.ValidatePaths(h.Paths); err != nil {
			return h, fmt.Errorf("card %s: %v", path, err)
		}
	}
	return h, nil
}

// Missing names the header lines a gated card must carry and this one does not. LEGS is
// not among them: a card with no LEGS: line gets `go` (SPEC-TOOLWORK §2 rule 3), and
// SOURCE is the card writer's receipt for a reader, which the gate does not use.
func (h CardHeader) Missing() []string {
	var out []string
	if h.Kind == "" {
		out = append(out, "KIND")
	}
	if len(h.Paths) == 0 {
		out = append(out, "PATHS")
	}
	if !h.HasTest {
		out = append(out, "TEST")
	}
	return out
}

// LegsOrDefault is the card's LEGS: line, or the `go` leg a card that names none gets.
func (h CardHeader) LegsOrDefault() []string {
	if len(h.Legs) == 0 {
		return []string{"go"}
	}
	return h.Legs
}

// IsTestEdit says whether the card writer excused this pre-existing test file by name.
func (h CardHeader) IsTestEdit(file string) bool {
	for _, e := range h.TestEdits {
		if e == file {
			return true
		}
	}
	return false
}
