package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDepVerbReachesTheKernelAsAStructureEvent is the audit's second pass over
// nova-tools#1673, run the way the issue asks for it: the wire schema's
// `"mutating": true` verbs are checked against the kernel's own text, not read
// off a spec paragraph.
//
// docs/schemas/nova-work-wire-v1.json promises `dep` -- `"event_kind":
// ":structure"`, `"mutating": true` -- and v1's own body fixes the structure
// event's field order (docs/SPEC-WORK.md:987, `dep` -- `:verb`, `:add`,
// `:remove`, `:reason`). The schema is right; the kernel is the defect. Until
// `*kind-field-order*` carries the `:structure` row, an `apply-event` knows how
// to apply it, and `%submit` reaches the verb, a `:deps` edge can only be
// seeded and never edited. This test reads the kernel's source the same way
// lispkernel_class_test.go reads its ASDF system, so the missing verb is red
// here and a later edit cannot quietly drop it again.
func TestDepVerbReachesTheKernelAsAStructureEvent(t *testing.T) {
	t.Parallel()

	src := filepath.Join(repoRoot(t), "lisp", "nova-work", "src")

	event := readFile(t, filepath.Join(src, "event.lisp"))
	if !hasKindFieldOrderRow(event, ":structure") {
		t.Errorf("docs/schemas/nova-work-wire-v1.json promises `dep` as a mutating `:structure` verb, but lisp/nova-work/src/event.lisp's *kind-field-order* has no `:structure` row: v1 promises the record at SPEC-WORK.md:987 and the kernel has no kind to write it, so `:deps` can only be seeded and never edited")
	} else if !strings.Contains(event, ":verb :add :remove :reason") {
		t.Errorf("lisp/nova-work/src/event.lisp has a `:structure` row, but not in SPEC-WORK.md:987's order `:verb :add :remove :reason`")
	}

	state := readFile(t, filepath.Join(src, "state.lisp"))
	if !strings.Contains(state, "(:structure") || !strings.Contains(state, "wnode-deps") {
		t.Errorf("lisp/nova-work/src/state.lisp's apply-event has no `:structure` branch over wnode-deps: a replayed `dep` edit would not rebuild the edge")
	}

	if !dispatchesDep(src) {
		t.Errorf("no source in lisp/nova-work/src dispatches the `dep` verb: the wire schema and SPEC-WORK.md:2362 promise a structure verb that edits a `:deps` edge, and %%submit reaches none")
	}
}

// hasKindFieldOrderRow reports whether the `*kind-field-order*` literal in
// event.lisp carries a row for KIND. The row is spelled `(:kind ...)` inside
// the quoted alist, so the match is anchored to the literal's opening paren.
func hasKindFieldOrderRow(event, kind string) bool {
	start := strings.Index(event, "*kind-field-order*")
	if start < 0 {
		return false
	}
	body := event[start:]
	return strings.Contains(body, "("+kind+" ")
}

// dispatchesDep reports whether any kernel source names %dep-submit, the
// submit function `%submit` routes the `dep` verb to.
func dispatchesDep(dir string) bool {
	entries, err := filepath.Glob(filepath.Join(dir, "*.lisp"))
	if err != nil {
		return false
	}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), "%dep-submit") {
			return true
		}
	}
	return false
}
