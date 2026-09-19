package wake

import (
	"os"
	"strings"
	"testing"
)

// specWakePath is the document this test reads, relative to this package. It is
// written the way internal/docs/spec_ci_index_test.go writes its own
// specCIPath: two levels up to the module root, then the docs tree.
const specWakePath = "../../docs/SPEC-WAKE.md"

// TestTheSpecNamesEveryWaitShapeTheCodeSuppresses holds the sentence that
// governs the classifier to the classifier itself, in both directions.
//
// #1772 taught waitBookkeeping to suppress three `WAIT` shapes, but
// docs/SPEC-WAKE.md still enumerated the suppressed set as the five `INBOX ...`
// tokens alone and told the reader that wait's lines were classified "exactly
// as `inbox`'s". A reader holding the spec would expect `WAIT TIMEOUT` to print,
// and it does not. So each shape the code suppresses must be NAMED IN THE
// BOOKKEEPING PARAGRAPH ITSELF -- searching the whole file is not enough,
// because `WAIT TIMEOUT` already appears twice elsewhere as an analogy for
// "nothing arrived yet" -- and the `--refresh` bullet that promised the two
// verbs share one classification must say `WAIT`.
//
// The control is the other direction: a `WAIT DONE` for another reason and a
// `WAIT REFUSED` are NOT bookkeeping and still print, and the paragraph must
// not name either as suppressed. Without it a sentence that simply listed every
// `WAIT` line as bookkeeping would pass the three positive checks and the tool
// would go quiet about a refusal.
func TestTheSpecNamesEveryWaitShapeTheCodeSuppresses(t *testing.T) {
	raw, err := os.ReadFile(specWakePath)
	if err != nil {
		t.Fatalf("read %s: %v", specWakePath, err)
	}
	spec := string(raw)
	lines := strings.Split(spec, "\n")
	para := bookkeepingParagraph(lines)

	// (a) the code half and (b) the spec half.
	bookkeeping := []struct {
		line    string
		literal string
	}{
		{"WAIT as=alice timeout=30s interval=5s cursor=abc", "WAIT as="},
		{"WAIT TIMEOUT after=30s polls=6 cursor=abc", "WAIT TIMEOUT"},
		{"WAIT DONE reason=timeout rearm=required next=x", "WAIT DONE reason=timeout"},
	}
	for _, s := range bookkeeping {
		if !waitBookkeeping(strings.Fields(s.line)) {
			t.Errorf("waitBookkeeping(%q) = false; the code suppresses this shape", s.line)
		}
		if !strings.Contains(para, s.literal) {
			t.Errorf("the spec's bookkeeping paragraph does not name %q for the shape %q it suppresses", s.literal, s.line)
		}
	}

	// (c) the control: the code still prints these, and the paragraph must not
	// call them bookkeeping.
	for _, line := range []string{
		"WAIT DONE reason=new cursor=abc",
		"WAIT REFUSED bus=missing",
	} {
		if waitBookkeeping(strings.Fields(line)) {
			t.Errorf("waitBookkeeping(%q) = true; a %q line is not bookkeeping and still prints", line, line)
		}
	}
	if strings.Contains(para, "reason=new") {
		t.Errorf("the spec's bookkeeping paragraph names reason=new as suppressed; a `WAIT DONE` for any other reason still prints")
	}
	if strings.Contains(para, "WAIT REFUSED") {
		t.Errorf("the spec's bookkeeping paragraph names WAIT REFUSED as suppressed; a refusal still prints")
	}

	// (d) the `--refresh` bullet: the line that says `classified exactly as`
	// must qualify it with `WAIT`, or the reader is told wait is classified
	// exactly like inbox and will expect WAIT TIMEOUT to print. The scope is
	// the bullet itself -- another paragraph elsewhere in the file also says
	// `classified exactly as` about a different flag.
	refreshStart := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "- **`--refresh`") {
			refreshStart = i
			break
		}
	}
	switch {
	case refreshStart < 0:
		t.Errorf("no `--refresh` bullet starts a line in %s", specWakePath)
	default:
		sawClause := false
		for _, line := range lines[refreshStart+1:] {
			if strings.HasPrefix(line, "- ") {
				break
			}
			if strings.Contains(line, "classified exactly as") {
				sawClause = true
				if !strings.Contains(line, "WAIT") {
					t.Errorf("the --refresh bullet line %q says `classified exactly as` without naming WAIT", line)
				}
			}
		}
		if !sawClause {
			t.Errorf("the `--refresh` bullet carries no line saying `classified exactly as`")
		}
	}
}

// bookkeepingParagraph cuts the paragraph that contains the bookkeeping-token
// sentence out of the spec: from the line naming `The bookkeeping tokens` to the
// next blank line. A `WAIT TIMEOUT` elsewhere in the file is an analogy, not an
// enumeration, so the check must read this paragraph and not the whole file.
func bookkeepingParagraph(lines []string) string {
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "The bookkeeping tokens") {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	var b strings.Builder
	for _, line := range lines[start:] {
		if strings.TrimSpace(line) == "" {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
