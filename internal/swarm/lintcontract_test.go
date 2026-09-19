package swarm

import (
	"os"
	"strings"
	"testing"
)

// THE CONTRACT LINE IS ONE LINE, AND THREE READERS HAVE TO AGREE ON IT (issue #1741).
//
// `cut` renders line 1, `lint --card` checks it on the bench, and `gather` compares the
// `RESULT.md`'s line 1 against the card's. They did not agree: `cut` writes and accepts
// `RESULT <label> sha=<sha12>` (internal/pulse/cut.go, and the example in
// docs/spec-pulse/10-the-card-as-cut-writes-it.md), while the lint's `result-first`
// wanted `RESULT: ` with a colon (docs/WORKER-CARDS.md practice 1). Every card `cut`
// writes therefore drew a `result-first` drift, which is how six cards written this
// session each drew one.
//
// This is a CLASS test, not a card's: it reads the other two readers where they live
// rather than restating them, so the day one of them changes form this goes red. It
// cannot import internal/pulse -- that package imports THIS one -- so it reads its
// source as text, which is the same thing a class test over a document does.

const (
	cutSourcePath     = "../pulse/cut.go"
	harvestSourcePath = "../pulse/harvest.go"
	cardCutDocPath    = "../../docs/spec-pulse/10-the-card-as-cut-writes-it.md"
)

func readOr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("this class test reads %s: %v", path, err)
	}
	return string(b)
}

// The example line 1 of the document `cut` is written from is a line the lint accepts.
func TestTheDocumentsContractLineIsOneTheLintAccepts(t *testing.T) {
	doc := readOr(t, cardCutDocPath)
	want := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "RESULT ") || strings.HasPrefix(line, "RESULT: ") {
			want = line
			break
		}
	}
	if want == "" {
		t.Fatalf("%s no longer shows a RESULT line 1; this test reads that example", cardCutDocPath)
	}
	if !IsCardContractLine(want) {
		t.Fatalf("the lint refuses the very line 1 the document says `cut` writes: %q\nthe lint accepts %v", want, CardContractPrefixes)
	}
}

// `cut`'s own renderer is on dev, so the test reads it rather than the document alone:
// internal/pulse/cut.go refuses a card whose line 1 is not the form it names.
func TestCutsContractLineIsOneTheLintAccepts(t *testing.T) {
	src := readOr(t, cutSourcePath)
	found := false
	for _, prefix := range CardContractPrefixes {
		if strings.Contains(src, `HasPrefix(line1, "`+prefix+`")`) {
			found = true
			if !IsCardContractLine(prefix + "label sha=0123456789ab") {
				t.Errorf("%s accepts %q and the lint does not", cutSourcePath, prefix)
			}
		}
	}
	if !found {
		t.Fatalf("%s no longer tests line 1 with a prefix the lint knows; the lint accepts %v and the two have drifted apart", cutSourcePath, CardContractPrefixes)
	}
}

// `gather` imposes no prefix of its own: it compares the RESULT.md's line 1 against the
// card's line 1 for equality (internal/pulse/harvest.go, classifyResult). So whatever
// form `cut` and the lint settle on, gather follows -- and a `RESULT` literal appearing
// in that file would be a third opinion, which is the defect this test exists to catch.
func TestGatherImposesNoContractPrefixOfItsOwn(t *testing.T) {
	src := readOr(t, harvestSourcePath)
	for _, bad := range []string{`"RESULT "`, `"RESULT: "`} {
		if strings.Contains(src, bad) {
			t.Errorf("%s carries the literal %s: gather compares line 1 for equality and must hold no prefix of its own", harvestSourcePath, bad)
		}
	}
}

// BOTH FORMS ARE ACCEPTED, AND THAT IS A STOPGAP. `cut` writes one form and
// docs/WORKER-CARDS.md practice 1 names the other; until #1741 settles one of them the
// lint accepts both, because refusing either one refuses real cards. When #1741 closes,
// one of these two lines goes.
func TestBothContractFormsAreAcceptedUntilTheIssueSettlesIt(t *testing.T) {
	for _, line := range []string{
		"RESULT card-1 sha=0123456789ab",
		"RESULT: CARD-0000 do the thing",
	} {
		if !IsCardContractLine(line) {
			t.Errorf("the lint refuses %q, which a tool on dev writes today", line)
		}
	}
	for _, line := range []string{
		"# fixed: row 5 writer_bound_count on go",
		"RESULTS card-1 sha=0123456789ab",
		"RESULT",
		" RESULT card-1 sha=0123456789ab",
	} {
		if IsCardContractLine(line) {
			t.Errorf("%q is not a contract line and the lint takes it for one", line)
		}
	}
}
