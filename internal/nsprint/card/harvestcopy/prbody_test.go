package harvestcopy_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
)

// TestPRBodyCarriesTheTestLine (#4313): the PR body carries the card's TEST
// line after DONE-WHEN, so a reader sees the class test, or why the card
// has none; a card with no TEST value adds no line.
func TestPRBodyCarriesTheTestLine(t *testing.T) {
	t.Parallel()
	body := harvestcopy.PRBody(harvestcopy.Request{Stream: "swarm: cards", Origin: "issue:nova-tools#4313",
		DoneWhen: "the copy is held to its spec", Test: "none one docs page; the reader checks it"})
	if !strings.Contains(body, "DONE-WHEN: the copy is held to its spec\nTEST: none one docs page; the reader checks it\n\n") {
		t.Fatalf("body:\n%s", body)
	}
	if b := harvestcopy.PRBody(harvestcopy.Request{Stream: "s", DoneWhen: "d"}); strings.Contains(b, "TEST:") {
		t.Fatalf("no TEST value, yet a line:\n%s", b)
	}
}

// TestPRBodyCarriesTheTestsReceiptLine (#4314): the PR body carries the prepush
// test receipt under TESTS: after TEST; a request with no Tests value adds no line.
func TestPRBodyCarriesTheTestsReceiptLine(t *testing.T) {
	t.Parallel()
	body := harvestcopy.PRBody(harvestcopy.Request{
		Stream:   "swarm: cards",
		Origin:   "issue:nova-tools#4314",
		DoneWhen: "prepush tests pass before push",
		Test:     "./internal/sample TestOne",
		Tests:    "make test PKGS=./internal/sample ./internal/ci ./internal/docs GOTEST_COUNT_FLAG= pass",
	})
	want := "TEST: ./internal/sample TestOne\nTESTS: make test PKGS=./internal/sample ./internal/ci ./internal/docs GOTEST_COUNT_FLAG= pass\n\n"
	if !strings.Contains(body, want) {
		t.Fatalf("body:\n%s\nwant substring:\n%s", body, want)
	}
	if b := harvestcopy.PRBody(harvestcopy.Request{Stream: "s", DoneWhen: "d"}); strings.Contains(b, "TESTS:") {
		t.Fatalf("no Tests value, yet a TESTS line:\n%s", b)
	}
}
