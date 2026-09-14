package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

func TestRootExclusivePacketCannotReplacePublishedFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "packet.md")
	if err := os.WriteFile(p, []byte("first publisher"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(p, []byte("second publisher")); err == nil {
		t.Error("exclusive publication replaced existing file")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "first publisher" {
		t.Fatalf("published artifact changed: %s", b)
	}
}

func TestRootPacketMetadataRejectsDuplicateFields(t *testing.T) {
	h := packetHeader{
		ID:    strings.Repeat("a", 12),
		Entry: "1",
		Head:  strings.Repeat("b", 40),
		Base:  strings.Repeat("c", 40),
		Range: "cccccccccccc...bbbbbbbbbbbb",
		Who:   "Ada",
		Built: "2026-09-14T00:00:00Z",
		Bytes: 100,
		Cut:   0,
	}
	h.ID = packetID(h.Entry, h.Head, h.Base, h.Range)
	if _, err := parsePacketFirstLine(h.String()); err != nil {
		t.Fatalf("baseline header invalid: %v", err)
	}
	if _, err := parsePacketFirstLine(h.String() + " head=" + strings.Repeat("d", 40)); err == nil {
		t.Fatal("duplicate head accepted")
	}
}

func TestRootAdvertisedTripleDotUsesMergeBase(t *testing.T) {
	lane, _ := packetLab(t)
	repo := filepath.Join(lane, merge.RepoDir)
	git := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = repo
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("checkout", "main")
	os.WriteFile(filepath.Join(repo, "base-only.txt"), []byte("not in feature\n"), 0600)
	git("add", "base-only.txt")
	git("commit", "-qm", "base moved independently")
	st, e := merge.Load(lane)
	if e != nil {
		t.Fatal(e)
	}
	st.Base = git("rev-parse", "HEAD")
	if e := st.SaveTo(lane); e != nil {
		t.Fatal(e)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(lane)
	var out, errb bytes.Buffer
	code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "Ada", "--out", "packet.md"}, &out, &errb)
	if code != 0 {
		t.Fatalf("packet %d: %s", code, errb.String())
	}
	b, _ := os.ReadFile("packet.md")
	if strings.Contains(string(b), "base-only.txt") {
		t.Fatalf("advertised triple-dot includes unrelated base deletion:\n%s", b)
	}
}

func TestRootReuseRuleCountIsUniqueNotPerFile(t *testing.T) {
	section := "## Rules touched\na.go: rules=1\nb.go: rules=1\n### docs/SPEC.md:3 rule 1\n> One rule touches both files.\ntouched by: a.go, b.go\n"
	n, err := packetRulesSectionCount(section)
	if err != nil || n != 1 {
		t.Fatalf("want one distinct rule, got %d err=%v", n, err)
	}
}

func TestRootReusePreservesAuthorSectionHeadings(t *testing.T) {
	author := "## This head\nthe author says:\nPlease document this heading:\n## Your prior verdicts on this entry\nThis is author text, preserve me.\n"
	text := "header\n\n" + author + "## Your prior verdicts on this entry\nnone\n## All verdicts at earlier heads\nnone recorded\n## Open findings (answer with `dup <id>` if you see the same thing)\nnone recorded\n## Rules touched\nNo changed files.\n## Diff abc...def\n```diff\n```\n\n## Not included\nnothing\n"
	sections, err := splitPacketSections(text)
	if err != nil {
		t.Fatal(err)
	}
	if sections.thisHead != author {
		t.Fatalf("author text truncated: %q", sections.thisHead)
	}
}

func TestRootExclusivePacketRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")
	symlink := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(symlink, []byte("content")); err == nil {
		t.Error("writeExclusive succeeded on symlink")
	}
}

func TestRootExclusivePacketConcurrentPublishers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "packet.md")
	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = writeExclusive(p, []byte(fmt.Sprintf("publisher %d\n", idx)))
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 successful publisher, got %d", successes)
	}
}

func TestRootPacketMetadataStrictValidation(t *testing.T) {
	baseHdr := packetHeader{
		ID:    strings.Repeat("a", 12),
		Entry: "1",
		Head:  strings.Repeat("b", 40),
		Base:  strings.Repeat("c", 40),
		Range: "cccccccccccc...bbbbbbbbbbbb",
		Who:   "Ada",
		Built: "2026-09-14T00:00:00Z",
		Bytes: 100,
		Cut:   0,
	}

	tests := []struct {
		name    string
		mutate  func(h packetHeader) string
		wantErr string
	}{
		{
			name: "unknown field",
			mutate: func(h packetHeader) string {
				return h.String() + " extra=foo"
			},
			wantErr: "unknown header field \"extra\"",
		},
		{
			name: "short id",
			mutate: func(h packetHeader) string {
				h.ID = "abcd"
				return h.String()
			},
			wantErr: "invalid id field",
		},
		{
			name: "non-hex id",
			mutate: func(h packetHeader) string {
				h.ID = "xyz123456789"
				return h.String()
			},
			wantErr: "invalid id field",
		},
		{
			name: "short head sha",
			mutate: func(h packetHeader) string {
				h.Head = strings.Repeat("b", 39)
				return h.String()
			},
			wantErr: "invalid head field",
		},
		{
			name: "bad timestamp",
			mutate: func(h packetHeader) string {
				h.Built = "not-a-timestamp"
				return h.String()
			},
			wantErr: "invalid built timestamp",
		},
		{
			name: "inconsistent range",
			mutate: func(h packetHeader) string {
				h.Range = "ffffffffffff...bbbbbbbbbbbb"
				return h.String()
			},
			wantErr: "does not match base",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := tt.mutate(baseHdr)
			_, err := parsePacketFirstLine(line)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestRootShellQuote(t *testing.T) {
	if got := shellQuote(""); got != "''" {
		t.Errorf("empty string: got %q, want ''", got)
	}
	if got := shellQuote("clean/path/to/file.go"); got != "clean/path/to/file.go" {
		t.Errorf("clean path: got %q", got)
	}
	if got := shellQuote("path with spaces/file.go"); got != "'path with spaces/file.go'" {
		t.Errorf("path with spaces: got %q", got)
	}
	if got := shellQuote("it's a path"); got != "'it'\\''s a path'" {
		t.Errorf("path with single quote: got %q", got)
	}
}

func TestRootOpenFindingsRecordsParsing(t *testing.T) {
	lane := t.TempDir()
	entryDir := merge.EntryDirName("entry-1")
	reviewsDir := filepath.Join(lane, "reviews", entryDir)
	if err := os.MkdirAll(reviewsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. A review record with a block, a fix, and a nit finding.
	r1 := `{
		"who": "stella",
		"at": "2026-09-14T01:00:00Z",
		"findings": [
			{"id": "f-nit", "path": "cmd/test.go", "line": 10, "state": "nit", "claim": "spelling"},
			{"id": "f-block", "path": "cmd/test.go", "line": 20, "state": "block", "claim": "fatal bug"},
			{"id": "f-fix", "path": "cmd/test.go", "line": 30, "state": "fix", "claim": "logic bug"}
		]
	}`
	if err := os.WriteFile(filepath.Join(reviewsDir, "review-stella.json"), []byte(r1), 0600); err != nil {
		t.Fatal(err)
	}

	// 2. A second review marking f-block as dup (seen by johnny).
	r2 := `{
		"who": "johnny",
		"at": "2026-09-14T02:00:00Z",
		"findings": [
			{"id": "f-block-dup", "state": "dup", "claim": "f-block"}
		]
	}`
	if err := os.WriteFile(filepath.Join(reviewsDir, "review-johnny.json"), []byte(r2), 0600); err != nil {
		t.Fatal(err)
	}

	// 3. A review closing f-nit.
	r3 := `{
		"who": "emma",
		"at": "2026-09-14T03:00:00Z",
		"findings": [
			{"id": "f-nit", "state": "close"}
		]
	}`
	if err := os.WriteFile(filepath.Join(reviewsDir, "review-emma.json"), []byte(r3), 0600); err != nil {
		t.Fatal(err)
	}

	// 4. An author answer for f-fix.
	ans := `{
		"finding": "f-fix",
		"who": "author",
		"as": "will fix in next commit",
		"at": "2026-09-14T04:00:00Z"
	}`
	if err := os.WriteFile(filepath.Join(reviewsDir, "answer-f-fix.json"), []byte(ans), 0600); err != nil {
		t.Fatal(err)
	}

	text, count, err := formatOpenFindings(lane, "entry-1", 10, 0, "feature")
	if err != nil {
		t.Fatalf("formatOpenFindings error: %v", err)
	}
	if count != 2 {
		t.Fatalf("want 2 open findings (f-block, f-fix), got %d:\n%s", count, text)
	}

	// Verify f-nit is not in open findings (it was closed)
	if strings.Contains(text, "f-nit") {
		t.Fatalf("closed finding f-nit should not be present:\n%s", text)
	}

	// Verify order: block comes before fix
	idxBlock := strings.Index(text, "f-block")
	idxFix := strings.Index(text, "f-fix")
	if idxBlock == -1 || idxFix == -1 || !(idxBlock < idxFix) {
		t.Fatalf("expected f-block before f-fix in table:\n%s", text)
	}

	// Verify seen by contains both stella and johnny for f-block
	if !strings.Contains(text, "stella, johnny") {
		t.Fatalf("expected stella, johnny for f-block:\n%s", text)
	}

	// Verify author says is populated for f-fix
	if !strings.Contains(text, "will fix in next commit") {
		t.Fatalf("expected author answer on f-fix:\n%s", text)
	}
}
