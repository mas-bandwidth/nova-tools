package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// spec_work_record_home_test.go holds docs/SPEC-WORK.md to Glenn's ruling of
// 2026-09-23 6:35-6:45 PM ET on where the work record lives. The RECORD (the
// cross-repo sexp, the ingest map, fold receipts) lives in the private repo
// mas-bandwidth/work, data only. The TOOL (cmd/nova-work and lisp/nova-work)
// stays in nova-tools, and nova-tools' docs/roadmaps/*.sexp is nova-tools' own
// public roadmap, a different thing from the record. "I don't want any
// confusion between nova-work the tool, and work the repo."
//
// Two texts came before it and this test fails if either comes back: PR #3162
// named the record's home after the tool (mas-bandwidth/nova-work) and made
// nova-tools' lander refuse record paths; the stream repair at 95287935 put the
// record in public nova-tools docs/roadmaps/ and left private repositories
// counted and not ingested.
//
// It reads the document as text and runs nothing.

// specWorkRecordHomePath is the document, relative to this package.
const specWorkRecordHomePath = "../../docs/SPEC-WORK.md"

// novaWorkRepoRe finds "nova-work repo" or "nova-work repository" as words, and
// not the verb line "nova-work report": the data repo is "the work repo".
var novaWorkRepoRe = regexp.MustCompile(`nova-work repo(sitory)?\b`)

// TestSpecWorkRecordLivesInTheWorkRepo is the ruling's contract.
func TestSpecWorkRecordLivesInTheWorkRepo(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(specWorkRecordHomePath)
	if err != nil {
		t.Fatalf("read %s: %v", specWorkRecordHomePath, err)
	}
	text := string(data)

	// #3162's home named after the tool, the 95287935 home in public nova-tools,
	// and the private-repository hold that came with it.
	for _, gone := range []string{
		"mas-bandwidth/nova-work",
		"record-never-in-nova-tools",
		"record-lives-in-nova-tools",
		"The lander accepts record writes",
		":private-repos",
		"no private home",
	} {
		if strings.Contains(text, gone) {
			t.Errorf("%s still carries %q: the record lives in mas-bandwidth/work (Glenn, 2026-09-23 6:35-6:45 PM ET)", specWorkRecordHomePath, gone)
		}
	}

	if loc := novaWorkRepoRe.FindStringIndex(text); loc != nil {
		t.Errorf("%s still names a nova-work repository at byte %d: %q (say \"the work repo\")", specWorkRecordHomePath, loc[0], text[loc[0]:loc[1]])
	}

	// The home, its check-in, its replay, the unaffected nova-tools lander and step 0.
	for _, want := range []string{
		"`record-lives-in-the-work-repo`",
		"### 5. Check-in: the sexp on `mas-bandwidth/work` `main` through the lander",
		"nova-tools' lander is unaffected by record writes",
		"the work repo exists (created 2026-09-23); the sexp moves there in the cutover",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", specWorkRecordHomePath, want)
		}
	}
}

// specWorkSection returns the text of the "### <n>." section of the ingest
// chapter, up to the next "### " heading, or "" when the heading is absent.
func specWorkSection(text, n string) string {
	start := strings.Index(text, "\n### "+n+". ")
	if start < 0 {
		return ""
	}
	rest := text[start+1:]
	if end := strings.Index(rest[4:], "\n### "); end >= 0 {
		return rest[:end+4]
	}
	return rest
}

// TestSpecWorkNamesTheWorkRepo pins the fold of Glenn's evening ruling into
// section 2's home line and #3168's five-path refusal into section 5. The
// manifest is work.sexp, named for the repo and not the tool; section 2 says
// which ruling it supersedes (the 3:58 PM comment 5801899651, which had put the
// record under nova-tools docs/roadmaps/); section 5 quotes #3168's five
// storage-split paths and its two LAND REFUSED lines, rooted at the work repo.
func TestSpecWorkNamesTheWorkRepo(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(specWorkRecordHomePath)
	if err != nil {
		t.Fatalf("read %s: %v", specWorkRecordHomePath, err)
	}
	text := string(data)

	home := specWorkSection(text, "2")
	for _, want := range []string{
		"**The whole record has one home: the work repo, the private repository `mas-bandwidth/work`; its manifest is `work.sexp`; the tool stays `cmd/nova-work` in nova-tools.**",
		"comment 5801899651",
		"the manifest `work.sexp`",
	} {
		if !strings.Contains(home, want) {
			t.Errorf("%s section 2 lacks %q", specWorkRecordHomePath, want)
		}
	}
	if strings.Contains(home, "keeps the name `nova-work.sexp`") {
		t.Errorf("%s section 2 still names the record's manifest after the tool", specWorkRecordHomePath)
	}

	checkin := specWorkSection(text, "5")
	for _, want := range []string{
		"#3168",
		"`docs/roadmaps/nova-work.sexp`, `docs/roadmaps/work/<repo>.sexp`, `docs/roadmaps/work/<repo>.closed.sexp`, `docs/roadmaps/blobs/**` and `docs/roadmaps/ingest-map.sexp`",
		"`work.sexp`, `work/<repo>.sexp`, `work/<repo>.closed.sexp`, `blobs/**` and `ingest-map.sexp`",
		"`LAND REFUSED batch=roadmap path=<p>: not a roadmap path`",
		"`LAND REFUSED path=<p>: written outside the nova-work writer`",
		"`docs/roadmaps/sprint-fixes-2026-09-22.sexp`",
	} {
		if !strings.Contains(checkin, want) {
			t.Errorf("%s section 5 lacks %q", specWorkRecordHomePath, want)
		}
	}
}
