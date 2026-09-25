package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// forestCopy puts the real work-set fixture at <tmp>/docs/roadmaps/units.sexp,
// a forest path, and returns it with the bytes it holds.
func forestCopy(t *testing.T) (string, []byte) {
	t.Helper()
	data, err := os.ReadFile(realSetFixture)
	if err != nil {
		t.Fatalf("read %s: %v", realSetFixture, err)
	}
	dir := filepath.Join(t.TempDir(), "docs", "roadmaps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "units.sexp")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, data
}

// dirNames is the sorted listing of dir: a lock directory or a temp file left
// beside the set is a write, too.
func dirNames(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// assertUntouched fails unless path still holds want and its directory lists
// only the set itself.
func assertUntouched(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s moved: a verb wrote the forest", path)
	}
	if names := dirNames(t, filepath.Dir(path)); names != filepath.Base(path) {
		t.Errorf("%s holds %s; a verb left a lock or a temp file in the forest", filepath.Dir(path), names)
	}
}

// TestForestWrittenOnlyByTheKernel is #3340's kernel boundary on this binary:
// every verb that edits a work set refuses a docs/roadmaps/ path at exit 3 with
// the file byte-identical, the one writer refuses it too, and a work set
// anywhere else is still written whole with its mode.
func TestForestWrittenOnlyByTheKernel(t *testing.T) {
	t.Run("set-check-write-status", func(t *testing.T) {
		gh := useFakeGH(t, map[string]string{})
		path, before := forestCopy(t)
		code, stdout, stderr := runCLI(t, "set", "check", "--file", path, "--write-status", "--cache", t.TempDir())
		if code != 3 || !strings.Contains(stderr, "written only by the nova-work kernel") || strings.Contains(stdout, "SET WROTE") {
			t.Fatalf("exit = %d, want 3\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		if len(gh.calls) != 0 {
			t.Errorf("the refusal came after %d forge calls; it must cost none", len(gh.calls))
		}
		assertUntouched(t, path, before)
	})
	t.Run("set-check-reads-the-forest", func(t *testing.T) {
		path, before := forestCopy(t)
		code, stdout, stderr := runCLI(t, "set", "check", "--file", path)
		if code == 3 || !strings.Contains(stdout, "SET OK units=") {
			t.Fatalf("a read of the forest was refused: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		assertUntouched(t, path, before)
	})
	t.Run("attempt-record", func(t *testing.T) {
		path, before := forestCopy(t)
		code, stdout, stderr := runCLI(t, "attempt", "record", "--file", path, "--unit", "certify:verb",
			"--by", "rowan-child", "--outcome", "ok", "--proof", "8a132e77", "--pr", "1369")
		if code != 3 || !strings.Contains(stderr, "written only by the nova-work kernel") || stdout != "" {
			t.Fatalf("exit = %d, want 3\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		assertUntouched(t, path, before)
	})
	t.Run("next-take", func(t *testing.T) {
		fakeRouter(t, func(u decide.Unit) (decide.RouteResult, error) { return rung("opus", 0.90), nil })
		path, before := forestCopy(t)
		code, stdout, stderr := runCLI(t, "next", "--file", path, "--for", "rowan-child", "--lanes", lanesFixture,
			"--no-jev", "--take", "--started", "2026-09-18T12:00:00Z")
		if code != 3 || !strings.Contains(stderr, "written only by the nova-work kernel") || stdout != "" {
			t.Fatalf("exit = %d, want 3\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		assertUntouched(t, path, before)
		// Without --take, next is a read and answers as it always did.
		code, stdout, stderr = runCLI(t, "next", "--file", path, "--for", "rowan-child", "--lanes", lanesFixture, "--no-jev")
		if code != 0 || !strings.Contains(stdout, "NEXT unit=certify:verb") {
			t.Fatalf("next without --take: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		assertUntouched(t, path, before)
	})
	t.Run("ask-record", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "docs", "roadmaps")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		f := &fakeSender{id: "rowan-93d3cbffc3d0"}
		code, _, stderr := runAsk(t, f,
			"--owner", "Stella", "--unit", "pull:queue",
			"--units", "../../internal/friends/testdata/work-set.lisp", "--record", filepath.Join(dir, "asks.json"),
			"--bus", "/bus", "--as", "Rowan", "--now", "2026-09-18T12:00:00Z")
		if code != 3 || !strings.Contains(stderr, "written only by the nova-work kernel") {
			t.Fatalf("exit = %d, want 3\nstderr: %s", code, stderr)
		}
		if len(f.notes) != 0 {
			t.Errorf("%d notes went out for a record the forest refused; the refusal comes before the send", len(f.notes))
		}
		if names := dirNames(t, dir); names != "" {
			t.Errorf("the forest holds %s after a refused ask", names)
		}
	})
	t.Run("dependencies-graph", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "docs", "roadmaps")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runCLI(t, "dependencies", "--graph", filepath.Join(dir, "deps.sexp"), "--node", "a", "--needs", "b")
		if code != 3 || !strings.Contains(stderr, "written only by the nova-work kernel") || stdout != "" {
			t.Fatalf("exit = %d, want 3\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		if names := dirNames(t, dir); names != "" {
			t.Errorf("the forest holds %s after a refused dependencies write", names)
		}
	})
	t.Run("plan-expand-out", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "docs", "roadmaps")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runCLI(t, "plan", "expand", "--file", realSetFixture, "--out", filepath.Join(dir, "cards"))
		if code != 3 || !strings.Contains(stderr, "written only by the nova-work kernel") || stdout != "" {
			t.Fatalf("exit = %d, want 3\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		if names := dirNames(t, dir); names != "" {
			t.Errorf("the forest holds %s after a refused plan expand", names)
		}
	})
	t.Run("writer-refuses-forest", func(t *testing.T) {
		path, before := forestCopy(t)
		if err := writeWorkSet(path, []byte("(work-set)\n")); !errors.Is(err, errForest) {
			t.Fatalf("writeWorkSet on the forest = %v, want errForest", err)
		}
		assertUntouched(t, path, before)
		// A link from outside the forest onto a forest file is still the forest.
		link := filepath.Join(t.TempDir(), "plan.sexp")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		if err := writeWorkSet(link, []byte("(work-set)\n")); !errors.Is(err, errForest) {
			t.Fatalf("writeWorkSet through a link into the forest = %v, want errForest", err)
		}
		assertUntouched(t, path, before)
	})
	t.Run("forest-paths", func(t *testing.T) {
		for path, want := range map[string]bool{
			"docs/roadmaps/nova-work.sexp":               true,
			"docs/roadmaps/sprint-fixes-2026-09-22.sexp": true,
			"docs/roadmaps/work/e01.sexp":                true,
			"docs/roadmaps/blobs/ab/cd":                  true,
			"./docs/roadmaps/../roadmaps/nova-work.sexp": true,
			"/srv/nova-tools/docs/roadmaps/x.sexp":       true,
			"docs/roadmapsx/nova-work.sexp":              false,
			"plans/docs-roadmaps.sexp":                   false,
			"units.lisp":                                 false,
			"":                                           false,
		} {
			if got := isForestPath(path); got != want {
				t.Errorf("isForestPath(%q) = %v, want %v", path, got, want)
			}
		}
	})
	t.Run("writer-writes-elsewhere", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "plan.sexp")
		if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writeWorkSet(path, []byte("new\n")); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new\n" || info.Mode().Perm() != 0o600 {
			t.Errorf("got %q mode %v, want \"new\\n\" mode 0600", got, info.Mode().Perm())
		}
		if names := dirNames(t, filepath.Dir(path)); names != "plan.sexp" {
			t.Errorf("the write left %s beside the set", names)
		}
	})
}
