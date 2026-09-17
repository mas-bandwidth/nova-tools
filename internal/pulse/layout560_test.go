package pulse

// Red test first for nova-tools #560: one file per replay slice and per spec
// section so parallel PRs stop conflicting.
//
// Contract under test:
//   - lisp/nova-work/tests/acceptance/<section>.lisp holds the replay slices
//     and the top file lisp/nova-work/tests/acceptance.lisp loads them;
//   - docs/spec-pulse/<section>.md holds the spec sections and
//     docs/SPEC-PULSE.md includes (references and contains) them;
//   - docs/WORKER-CARDS.md carries the practice: an amendment edits one
//     section file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot560(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "docs", "SPEC-PULSE.md")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("repo root with docs/SPEC-PULSE.md not found")
	return ""
}

func TestOneFilePerSliceAndSection560(t *testing.T) {
	root := repoRoot560(t)

	// 1. The replay slices live one file per slice under acceptance/.
	accDir := filepath.Join(root, "lisp", "nova-work", "tests", "acceptance")
	entries, err := os.ReadDir(accDir)
	if err != nil {
		t.Fatalf("lisp/nova-work/tests/acceptance/ unreadable: %v (needs lisp/nova-work/tests/acceptance/<section>.lisp)", err)
	}
	var slices []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".lisp") {
			slices = append(slices, e.Name())
		}
	}
	if len(slices) < 2 {
		t.Fatalf("want >=2 slice files in lisp/nova-work/tests/acceptance/, got %d", len(slices))
	}

	// 2. The top file loads the slice files instead of carrying the cases.
	top, err := os.ReadFile(filepath.Join(root, "lisp", "nova-work", "tests", "acceptance.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(top), "acceptance/") {
		t.Fatalf("acceptance.lisp names no acceptance/ slice file -- needs load of lisp/nova-work/tests/acceptance/<section>.lisp")
	}
	if strings.Count(string(top), "(deftest") > 1 {
		t.Fatalf("acceptance.lisp still carries %d deftests -- needs 0 or 1 with the rest in acceptance/*.lisp", strings.Count(string(top), "(deftest"))
	}
	for _, s := range slices {
		base := strings.TrimSuffix(s, ".lisp")
		if !strings.Contains(string(top), base) {
			t.Fatalf("acceptance.lisp does not load slice file %s -- needs lisp/nova-work/tests/acceptance.lisp:%d", s, 1)
		}
		body, err := os.ReadFile(filepath.Join(accDir, s))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "(deftest") && !strings.Contains(string(body), "(defun") {
			t.Fatalf("slice file %s carries no deftest -- needs lisp/nova-work/tests/acceptance/%s:1", s, s)
		}
	}

	// 3. The spec sections live one file per section under docs/spec-pulse/.
	secDir := filepath.Join(root, "docs", "spec-pulse")
	sentries, err := os.ReadDir(secDir)
	if err != nil {
		t.Fatalf("docs/spec-pulse/ unreadable: %v (needs docs/spec-pulse/<section>.md)", err)
	}
	var sections []string
	for _, e := range sentries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			sections = append(sections, e.Name())
		}
	}
	if len(sections) < 2 {
		t.Fatalf("want >=2 section files in docs/spec-pulse/, got %d", len(sections))
	}

	// 4. SPEC-PULSE.md includes them: it names each file and carries its body.
	spec, err := os.ReadFile(filepath.Join(root, "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sections {
		if !strings.Contains(string(spec), "spec-pulse/"+s) {
			t.Fatalf("SPEC-PULSE.md does not include section file %s -- needs docs/SPEC-PULSE.md:1", s)
		}
		body, err := os.ReadFile(filepath.Join(secDir, s))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.TrimSpace(string(body))
		if text == "" {
			t.Fatalf("section file %s is empty -- needs docs/spec-pulse/%s:1", s, s)
		}
		// The section file's first non-empty line must appear in the top file:
		// that is the machine-checked meaning of "included by SPEC-PULSE.md".
		first := strings.TrimSpace(strings.Split(text, "\n")[0])
		if len(first) < 4 || !strings.Contains(string(spec), first) {
			t.Fatalf("SPEC-PULSE.md does not carry the body of %s -- needs docs/SPEC-PULSE.md:1", s)
		}
	}

	// 5. WORKER-CARDS practice: an amendment edits one section file.
	wc, err := os.ReadFile(filepath.Join(root, "docs", "WORKER-CARDS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wc), "one section file") {
		t.Fatalf("WORKER-CARDS.md names no one-section-file amendment practice -- needs docs/WORKER-CARDS.md:1")
	}
}
