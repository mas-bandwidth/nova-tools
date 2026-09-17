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
//
// The expected set is DERIVED FROM THE SPEC, never from a frozen list: the
// section names come from the references the top spec carries and the slice
// names from the loader the top Lisp file carries. The directory listing is
// only used for the reverse direction -- an added file must be wired into the
// spec or the loader. Every name is resolved THROUGH THE FILESYSTEM and
// matched with os.SameFile, so an entry and a reference that differ only in
// case are one file on APFS and still two on Linux.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// sectionRef560 is the spelling the top spec uses for a section file. Scanned
// out of the spec itself so the set of sections is never frozen.
var sectionRef560 = regexp.MustCompile(`spec-pulse/([A-Za-z0-9][A-Za-z0-9._-]*\.md)`)

// sliceRef560 is a loaded slice name in the top Lisp loader's canonical list.
var sliceRef560 = regexp.MustCompile(`"([A-Za-z0-9][A-Za-z0-9._-]*\.lisp)"`)

// list560 returns the distinct names a regexp finds, sorted, so every report
// is deterministic on every filesystem.
func list560(re *regexp.Regexp, text string) []string {
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		seen[m[1]] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type slice560 struct {
	name string
	body []byte
}

// wired560 checks that every reference resolves to a regular file in dir and
// that no file in dir is left unreferenced. A reference is resolved with
// os.Stat and matched to the directory entry with os.SameFile, so the on-disk
// spelling wins: on a case-folding APFS a reference and an entry that differ
// only in case still name one file, and on Linux they do not. Files are read
// under their real on-disk name.
func wired560(t *testing.T, dir, suffix, what string, refs []string) []slice560 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%s unreadable: %v", dir, err)
	}
	type entry560 struct {
		name string
		info os.FileInfo
	}
	var disk []entry560
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		disk = append(disk, entry560{e.Name(), info})
	}
	if len(disk) < 2 {
		t.Fatalf("want >=2 %s files in %s, got %d", suffix, dir, len(disk))
	}
	wired := map[int]bool{}
	var out []slice560
	for _, ref := range refs {
		info, err := os.Stat(filepath.Join(dir, ref))
		if err != nil {
			t.Fatalf("the %s names %s, but it is not on disk in %s: %v", what, ref, dir, err)
		}
		idx := -1
		for i, d := range disk {
			if os.SameFile(info, d.info) {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("the %s names %s, which resolves outside %s -- a file belongs in its own directory", what, ref, dir)
		}
		if wired[idx] {
			continue
		}
		wired[idx] = true
		body, err := os.ReadFile(filepath.Join(dir, disk[idx].name))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, slice560{disk[idx].name, body})
	}
	for i, d := range disk {
		if !wired[i] {
			t.Fatalf("%s is not named by the %s -- add it to the canonical list (nova-tools #560)", filepath.Join(dir, d.name), what)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func TestOneFilePerSliceAndSection560(t *testing.T) {
	root := repoRoot560(t)

	// 1+2. The replay slices live one file per slice under acceptance/, and the
	// top file loads exactly those files instead of carrying the cases.
	accDir := filepath.Join(root, "lisp", "nova-work", "tests", "acceptance")
	topRaw, err := os.ReadFile(filepath.Join(root, "lisp", "nova-work", "tests", "acceptance.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	top := string(topRaw)
	if !strings.Contains(top, "acceptance/") {
		t.Fatalf("acceptance.lisp names no acceptance/ slice file -- needs load of lisp/nova-work/tests/acceptance/<section>.lisp")
	}
	if strings.Count(top, "(deftest") > 1 {
		t.Fatalf("acceptance.lisp still carries %d deftests -- needs 0 or 1 with the rest in acceptance/*.lisp", strings.Count(top, "(deftest"))
	}
	loaded := list560(sliceRef560, top)
	if len(loaded) < 2 {
		t.Fatalf("want >=2 slice files loaded by lisp/nova-work/tests/acceptance.lisp, got %d", len(loaded))
	}
	for _, s := range wired560(t, accDir, ".lisp", "lisp/nova-work/tests/acceptance.lisp", loaded) {
		if !strings.Contains(string(s.body), "(deftest") && !strings.Contains(string(s.body), "(defun") {
			t.Fatalf("slice file %s carries no deftest -- needs lisp/nova-work/tests/acceptance/%s:1", s.name, s.name)
		}
	}

	// 3+4. The spec sections live one file per section under docs/spec-pulse/,
	// and SPEC-PULSE.md includes them: it names each file and carries its body.
	secDir := filepath.Join(root, "docs", "spec-pulse")
	spec, err := os.ReadFile(filepath.Join(root, "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatal(err)
	}
	specText := string(spec)
	named := list560(sectionRef560, specText)
	if len(named) < 2 {
		t.Fatalf("SPEC-PULSE.md names %d section files under docs/spec-pulse/ -- needs one file per section (nova-tools #560)", len(named))
	}
	for _, s := range wired560(t, secDir, ".md", "top spec SPEC-PULSE.md", named) {
		text := strings.TrimSpace(string(s.body))
		if text == "" {
			t.Fatalf("section file %s is empty -- needs docs/spec-pulse/%s:1", s.name, s.name)
		}
		// The section file's first non-empty line must appear in the top file:
		// that is the machine-checked meaning of "included by SPEC-PULSE.md".
		first := strings.TrimSpace(strings.Split(text, "\n")[0])
		if len(first) < 4 || !strings.Contains(specText, first) {
			t.Fatalf("SPEC-PULSE.md does not carry the body of %s -- needs docs/SPEC-PULSE.md:1", s.name)
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
