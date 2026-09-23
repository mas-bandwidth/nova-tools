package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// spec_work_record_home_test.go holds docs/SPEC-WORK.md to Glenn's ruling of
// 2026-09-23 3:58 PM ET on where the nova-work record lives: nova-work is a
// tool (cmd/nova-work and lisp/nova-work in nova-tools) and its record is
// nova-tools docs/roadmaps/*.sexp. There is no separate record repository.
//
// PR #3162 (rev 5 and 6 of the primary-source section) put the record in a
// separate private repository on a misremembered ruling, made the lander
// refuse batches that write docs/roadmaps/ record files, and carried a replay,
// record-never-in-nova-tools, that failed on them. This test fails if any of
// that text comes back.
//
// It reads the document as text and runs nothing.

// specWorkRecordHomePath is the document, relative to this package.
const specWorkRecordHomePath = "../../docs/SPEC-WORK.md"

// novaWorkRepoRe finds "nova-work repo" or "nova-work repository" as words, and
// not the verb line "nova-work report".
var novaWorkRepoRe = regexp.MustCompile(`nova-work repo(sitory)?\b`)

// TestSpecWorkRecordLivesInNovaToolsRoadmaps is the ruling's contract.
func TestSpecWorkRecordLivesInNovaToolsRoadmaps(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(specWorkRecordHomePath)
	if err != nil {
		t.Fatalf("read %s: %v", specWorkRecordHomePath, err)
	}
	text := string(data)

	// The wrong home, the refusal of record writes, and the replay that enforced them.
	for _, gone := range []string{
		"mas-bandwidth/nova-work",
		"record-never-in-nova-tools",
		"the record lives in mas-bandwidth",
		"only the\nnova-work writer lands here",
	} {
		if strings.Contains(text, gone) {
			t.Errorf("%s still carries %q: the record lives in nova-tools docs/roadmaps (Glenn, 2026-09-23 3:58 PM ET)", specWorkRecordHomePath, gone)
		}
	}

	if loc := novaWorkRepoRe.FindStringIndex(text); loc != nil {
		t.Errorf("%s still names a nova-work repository at byte %d: %q", specWorkRecordHomePath, loc[0], text[loc[0]:loc[1]])
	}

	// The corrected replay and the lander accepting record writes.
	for _, want := range []string{
		"`record-lives-in-nova-tools-roadmaps`",
		"### 5. Check-in: the sexp in nova-tools `docs/roadmaps/` on `dev` through the lander",
		"The lander accepts record writes",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", specWorkRecordHomePath, want)
		}
	}
}
