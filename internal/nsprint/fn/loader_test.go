package fn

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var localDecl = regexp.MustCompile(`^local\s+(function\s+[A-Za-z_][A-Za-z0-9_]*|[A-Za-z_][A-Za-z0-9_]*(\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)`)

// countLocals returns the most locals the assembled chunk's main function
// holds at once: the column-0 locals outside every file block, plus the
// largest count inside one block (a block's locals leave scope at its end).
// sum is what the chunk would hold with no blocks, for the log line.
func countLocals(t *testing.T, source string) (active, sum int, largest string) {
	t.Helper()
	outside, most := 0, 0
	block, inBlock, header := 0, "", ""
	for i, line := range strings.Split(source, "\n") {
		prev := header
		header = ""
		switch {
		case strings.HasPrefix(line, fileHeader+"lua/"):
			header = strings.TrimPrefix(line, fileHeader)
			continue
		case line == blockOpen && prev != "":
			if inBlock != "" {
				t.Fatalf("line %d: block %q opens inside %q", i+1, prev, inBlock)
			}
			inBlock, block = prev, 0
			continue
		case strings.HasPrefix(line, blockClose):
			if name := strings.TrimPrefix(line, blockClose); name != inBlock {
				t.Fatalf("line %d: block %q closes, open is %q", i+1, name, inBlock)
			}
			if block > most {
				most, largest = block, inBlock
			}
			inBlock = ""
			continue
		}
		m := localDecl.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n := 1
		if !strings.HasPrefix(m[1], "function") {
			n = strings.Count(m[1], ",") + 1
		}
		sum += n
		if inBlock != "" {
			block += n
		} else {
			outside += n
		}
	}
	if inBlock != "" {
		t.Fatalf("block %q never closes", inBlock)
	}
	return outside + most, sum, largest
}

// TestLibraryLocalsUnderLimit is the guard for dev red at 8d12fb19: #3487
// took the concatenated library from 199 to 206 top-level locals and Redis
// refused it ("main function has more than 200 local variables"). It counts
// the locals the assembled chunk's main function holds at once and fails
// above MaxLocals (180), so a merge that nears the limit fails here, in the
// package that owns the chunk, not at FUNCTION LOAD on the fleet.
func TestLibraryLocalsUnderLimit(t *testing.T) {
	t.Parallel()

	source, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	active, sum, largest := countLocals(t, source)
	t.Logf("active locals %d (largest file %s), unscoped sum %d, limit %d", active, largest, sum, MaxLocals)
	if active > MaxLocals {
		t.Fatalf("nova_sprint main function holds %d locals at once (largest file %s), over %d (Lua refuses above 200); move helpers into a table or split the file", active, largest, MaxLocals)
	}
	if active == sum && sum > 1 {
		t.Fatalf("no file is block-scoped (active %d = sum %d); Source must wrap each file in its own do-block", active, sum)
	}
}

// TestCountLocalsSeesTheOldShape is the guard's own control: the same files
// concatenated with no blocks (the shape that broke dev) count over the limit.
func TestCountLocalsSeesTheOldShape(t *testing.T) {
	t.Parallel()

	source, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	var flat []string
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		if line == blockOpen && i > 0 && strings.HasPrefix(lines[i-1], fileHeader+"lua/") {
			continue
		}
		if strings.HasPrefix(line, blockClose+"lua/") {
			continue
		}
		flat = append(flat, line)
	}
	active, _, _ := countLocals(t, strings.Join(flat, "\n"))
	if active <= MaxLocals {
		t.Fatalf("unblocked chunk counts %d locals, want > %d (the guard must see the pre-fix shape)", active, MaxLocals)
	}
}

// TestFunctionsForProfileReflectsGating verifies that Spec().Functions() and
// FunctionsForProfile accurately reflect profile filtering without requiring
// a Redis server:
//   - Spec().Functions() (and FunctionsForProfile("")) omits all 6 profile-gated
//     names (TSetFunctions and SprintFunctions).
//   - FunctionsForProfile(TSetStandalone) and FunctionsForProfile(TSetComposed)
//     include TSetFunctions (4) and omit SprintFunctions (2).
//   - FunctionsForProfile(TSetSprint) includes both TSetFunctions and SprintFunctions.
func TestFunctionsForProfileReflectsGating(t *testing.T) {
	t.Parallel()

	legacy, err := Spec().Functions()
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy) == 0 {
		t.Fatal("legacy Functions() is empty")
	}

	legacySet := map[string]bool{}
	for _, n := range legacy {
		legacySet[n] = true
	}

	// Verify all 6 profile-gated names are absent from legacy Spec().Functions().
	for _, name := range append(append([]string(nil), TSetFunctions...), SprintFunctions...) {
		if legacySet[name] {
			t.Errorf("legacy Spec().Functions() includes profile-gated function %q", name)
		}
	}

	// FunctionsForProfile("") matches Spec().Functions().
	fromEmpty, err := FunctionsForProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(legacy, fromEmpty) {
		t.Fatalf("FunctionsForProfile(\"\") = %v, want %v", fromEmpty, legacy)
	}

	// Standalone and Composed admit TSetFunctions, but not SprintFunctions.
	for _, prof := range []TSetProfile{TSetStandalone, TSetComposed} {
		names, err := FunctionsForProfile(prof)
		if err != nil {
			t.Fatalf("FunctionsForProfile(%q): %v", prof, err)
		}
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		for _, name := range TSetFunctions {
			if !set[name] {
				t.Errorf("profile %q missing TSetFunction %q", prof, name)
			}
		}
		for _, name := range SprintFunctions {
			if set[name] {
				t.Errorf("profile %q unexpectedly includes SprintFunction %q", prof, name)
			}
		}
		if len(names) != len(legacy)+len(TSetFunctions) {
			t.Errorf("profile %q function count %d, want %d", prof, len(names), len(legacy)+len(TSetFunctions))
		}
	}

	// Sprint profile admits both TSetFunctions and SprintFunctions.
	sprintNames, err := FunctionsForProfile(TSetSprint)
	if err != nil {
		t.Fatalf("FunctionsForProfile(sprint): %v", err)
	}
	sprintSet := map[string]bool{}
	for _, n := range sprintNames {
		sprintSet[n] = true
	}
	for _, name := range TSetFunctions {
		if !sprintSet[name] {
			t.Errorf("sprint profile missing TSetFunction %q", name)
		}
	}
	for _, name := range SprintFunctions {
		if !sprintSet[name] {
			t.Errorf("sprint profile missing SprintFunction %q", name)
		}
	}
	if len(sprintNames) != len(legacy)+len(TSetFunctions)+len(SprintFunctions) {
		t.Errorf("sprint profile function count %d, want %d", len(sprintNames), len(legacy)+len(TSetFunctions)+len(SprintFunctions))
	}

	// Unknown profile returns an error.
	if _, err := FunctionsForProfile("unknown"); err == nil {
		t.Error("FunctionsForProfile(\"unknown\") accepted; want error")
	}
}
