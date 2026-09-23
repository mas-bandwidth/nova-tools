package buildinfo_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// TestIssue2291 reproduces nova-tools#2291 from the buildinfo slice the issue
// names: a binary's source metadata — the repository, revision, dirty flag and
// build host that say WHERE the binary was actually built — travels through the
// version line so a reader can verify the build came from the checkout the
// manifest recorded, not just the checkout that happened to carry the linker
// stamp. The line is the writer/parser pair internal/buildinfo has always
// been, and Source here is the structured shape a stamp verification reads
// out of it.
//
// #2291 is the wider `nova-update apply --sha` job — atomic publish, manifest
// record, verify at every stamp — and the slice of it this package owns is
// this one. apply --sha calls LineWithSource on every binary it builds, and
// apply --sha's postflight, the snapshot, and `moved`'s readback call
// FindSource on every line they read; that pair is what TestIssue2291
// exercises, so the rest of the issue has a writer and a parser to lean on.
func TestIssue2291(t *testing.T) {
	// The four fields the spec demands (#2291, SPEC-VERSION item 6): every
	// one of them round-trips through the version line, and a Source with
	// any of them missing is a Source the reader cannot verify.
	stamp := "20260909112233-0123456789ab"
	for _, tc := range []struct {
		name string
		src  buildinfo.Source
	}{
		{
			"a clean build from a known repo writes and reads back the four fields",
			buildinfo.Source{
				Repository: "github.com/mas-bandwidth/nova-tools",
				Revision:   "0123456789abcdef0123456789abcdef01234567",
				Dirty:      false,
				BuildHost:  "studio-darwin",
			},
		},
		{
			"a dirty build says so; dirty=false and dirty=true are distinct",
			buildinfo.Source{
				Repository: "github.com/mas-bandwidth/nova-tools",
				Revision:   "0123456789abcdef0123456789abcdef01234567",
				Dirty:      true,
				BuildHost:  "ci-runner-04",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := buildinfo.LineWithSource("nova-bus", stamp, tc.src)
			// A version line is one line: a newline in the line would
			// break every reader in the tree that builds its answer on
			// strings.Cut(line, "\n"), and the source fields are not
			// exempted from that.
			if strings.ContainsAny(line, "\n\r") {
				t.Fatalf("the line carries a newline:\n%s", line)
			}
			fields, ok := buildinfo.Parse(line)
			if !ok {
				t.Fatalf("Parse refused a line that obeys the four-token grammar with source extras:\n%s", line)
			}
			got, ok := fields.FindSource()
			if !ok {
				t.Fatalf("FindSource returned !ok for a line carrying source metadata:\n%s", line)
			}
			if got != tc.src {
				t.Errorf("Source did not round-trip:\n  got  %+v\n  want %+v", got, tc.src)
			}
		})
	}

	// A version line that carries NO source metadata is still a version
	// line — old binaries, foreign tools, and a `go install` from a tag
	// never had this — and FindSource says so with ok=false rather than
	// guessing. The stamp stays; only the structured view of WHERE is
	// absent.
	t.Run("a version line without source metadata has no Source", func(t *testing.T) {
		plain := buildinfo.Line("nova-bus", stamp)
		fields, ok := buildinfo.Parse(plain)
		if !ok {
			t.Fatalf("Parse refused a plain version line:\n%s", plain)
		}
		if _, ok := fields.FindSource(); ok {
			t.Errorf("FindSource returned ok=true for a line that carries no source metadata:\n%s", plain)
		}
	})

	// A partial source is a source the reader cannot verify — repository
	// alone, or revision alone, is the same answer as no source at all.
	// The spec names this case directly: a binary whose source metadata is
	// "missing or disagrees" is refused, and a half-present source is a
	// silent disagreement (#2291, SPEC-VERSION item 6).
	t.Run("a partial source is no source", func(t *testing.T) {
		for _, partial := range [][]string{
			{"repo=github.com/mas-bandwidth/nova-tools"},
			{"revision=0123456789abcdef0123456789abcdef01234567"},
			{"dirty=false"},
			{"build_host=studio"},
			{"repo=x", "revision=y"},
			{"repo=x", "dirty=false", "build_host=h"},
		} {
			line := buildinfo.Line("nova-bus", stamp, partial...)
			fields, ok := buildinfo.Parse(line)
			if !ok {
				t.Fatalf("Parse refused a line with partial source: %v\n%s", partial, line)
			}
			if _, ok := fields.FindSource(); ok {
				t.Errorf("FindSource returned ok=true for a partial source %v:\n%s", partial, line)
			}
		}
	})

	// A disagreeing field is a refused one, not a silent one: the reader
	// surfaces the disagreement rather than papering over it. A Source
	// built from the line's four source extras compares field by field,
	// and the test asserts that disagreement is visible.
	t.Run("a disagreeing source is visible to the reader", func(t *testing.T) {
		want := buildinfo.Source{
			Repository: "github.com/mas-bandwidth/nova-tools",
			Revision:   "0123456789abcdef0123456789abcdef01234567",
			Dirty:      false,
			BuildHost:  "studio-darwin",
		}
		other := want
		other.Repository = "github.com/other/repo"
		if other == want {
			t.Fatal("the disagreeing Source was not disagreeing; this case is broken")
		}
		line := buildinfo.LineWithSource("nova-bus", stamp, other)
		fields, ok := buildinfo.Parse(line)
		if !ok {
			t.Fatalf("Parse refused: %s", line)
		}
		got, ok := fields.FindSource()
		if !ok || got == want {
			t.Fatalf("FindSource accepted the disagreeing line as the wanted source: got=%+v want=%+v", got, want)
		}
	})
}
