package docs

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// issue2080_path is the roadmap sexp the criterion lives in.
const issue2080_path = "../../docs/roadmaps/nova-work.sexp"

// issue2080_feature captures the per-feature verification row as it appears in
// the sexp's :verification :by-feature list.
type issue2080_feature struct {
	id       string
	verified int
	total    int
	tests    []string
}

// issue2080_rowRe matches one (:feature "E09-F01" :verified N :total M :tests "...")
var issue2080_rowRe = regexp.MustCompile(`\(\s*:feature\s*"([^"]+)"\s*:verified\s+(\d+)\s*:total\s+(\d+)\s*:tests\s*"([^"]*)"`)

// issue2080_blockRe isolates the `:id "E09-F01" ... :features ...` block.
var issue2080_blockRe = regexp.MustCompile(`(?s):id\s*"E09-F01"\s*.*?:subfeatures\s*\(([^)]*\))`)

// issue2080_subRe finds one :subfeatures string literal entry inside the block.
var issue2080_subRe = regexp.MustCompile(`"([^"]+)"`)

// TestIssue2080 pins the nova-work E09-F01 criteria nova-tools#2080 asks for
// against the roadmap sexp's own verification row.
//
// E09-F01 has three subfeatures (lines 1085-1095 of nova-work.sexp):
//
//	01 Capture stable provider/repository/issue identity, revision and URL
//	02 Preserve body, comments, labels, relationships, attachments and pagination
//	03 Keep inaccessible or unsupported fields explicit
//
// 03 is already verified today (the "archive-completeness" row). This test
// holds the road open: until 01 and 02 are verified too, E09-F01 is not the
// real read-only GitHub adapter nova-tools#2080 is asking for, and the row
// must not claim it is.
func TestIssue2080(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(issue2080_path)
	if err != nil {
		t.Fatalf("%s: %v; the criterion E09-F01 the issue names lives in this sexp", issue2080_path, err)
	}
	content := string(body)

	row := issue2080_findRow(content, "E09-F01")
	if row == nil {
		t.Fatalf("%s: no (:feature \"E09-F01\" ...) row in :by-feature; the criterion the issue asks for is missing from the roadmap", issue2080_path)
	}

	if row.verified < 3 {
		t.Errorf("%s: E09-F01 :verified is %d, want 3 — the three subfeatures the feature lists (lines 1085-1095) are %q, %q and %q, and a verified count below the subfeature count means the issue text (\"capture every issue with stable identity, revision, URL, body, comments, labels, relationships, attachments and pagination\") is still unmet for at least one of them",
			issue2080_path,
			row.verified,
			"Capture stable provider/repository/issue identity, revision and URL",
			"Preserve body, comments, labels, relationships, attachments and pagination",
			"Keep inaccessible or unsupported fields explicit")
	}

	if row.verified > row.total {
		t.Errorf("%s: E09-F01 :verified (%d) > :total (%d); a verified count larger than the feature's criterion count is a count the feature does not have",
			issue2080_path, row.verified, row.total)
	}

	// The two new criteria the issue closes — the ones the issue text names
	// word-for-word. They are the body of the issue: stable identity,
	// revision, URL, body, comments, labels, relationships, attachments,
	// pagination. 03 ("inaccessible fields") is already covered by
	// archive-completeness and must stay covered.
	for _, want := range []string{
		"Capture stable provider/repository/issue identity, revision and URL",
		"Preserve body, comments, labels, relationships, attachments and pagination",
		"Keep inaccessible or unsupported fields explicit",
	} {
		if !issue2080_subfeatureContains(content, want) {
			t.Errorf("%s: the E09-F01 :subfeatures block no longer carries %q; the criterion the issue names is gone from the feature the issue asks for",
				issue2080_path, want)
		}
	}

	// A criterion without a named test is not verified by this repository's
	// own rule (lines 65-66 of the sexp: "a criterion is verified only when
	// a named test in this repository proves it and that test passed at
	// :revision"). The names must be the test names the row lists, separated
	// by "; ", so a single test does not silently cover two criteria.
	for _, name := range []string{
		"stable-identity-revision-and-url",
		"body-comments-labels-relationships-and-pagination",
	} {
		if !issue2080_hasTest(row, name) {
			t.Errorf("%s: E09-F01 :tests does not name %q; a criterion without a named test in this repository is not verified under the sexp's own rule, and the issue asks for it",
				issue2080_path, name)
		}
	}

	// archive-completeness is the existing 03 test. The issue text says
	// 03 must stay verified; if it was silently dropped, E09-F01 is no
	// longer the feature nova-tools#2080 asked for.
	if !issue2080_hasTest(row, "archive-completeness") {
		t.Errorf("%s: E09-F01 :tests no longer names \"archive-completeness\"; the existing 03 test was dropped, and the issue text is explicit that the inaccessible-fields criterion must stay verified",
			issue2080_path)
	}
}

// issue2080_findRow finds the (:feature id :verified N :total M :tests "...")
// row for id in the sexp's :verification :by-feature list.
func issue2080_findRow(content, id string) *issue2080_feature {
	for _, m := range issue2080_rowRe.FindAllStringSubmatch(content, -1) {
		if m[1] != id {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		tot, _ := strconv.Atoi(m[3])
		var tests []string
		for _, name := range strings.Split(m[4], ";") {
			name = strings.TrimSpace(name)
			if name != "" {
				tests = append(tests, name)
			}
		}
		return &issue2080_feature{id: m[1], verified: n, total: tot, tests: tests}
	}
	return nil
}

// issue2080_subfeatureContains reports whether the E09-F01 :subfeatures list
// contains the literal phrase. The list is a flat sequence of double-quoted
// strings, so this is a substring match inside that block.
func issue2080_subfeatureContains(content, phrase string) bool {
	m := issue2080_blockRe.FindStringSubmatch(content)
	if m == nil {
		return false
	}
	for _, s := range issue2080_subRe.FindAllStringSubmatch(m[1], -1) {
		if strings.Contains(s[1], phrase) {
			return true
		}
	}
	return false
}

// issue2080_hasTest reports whether the row's :tests list contains name.
func issue2080_hasTest(row *issue2080_feature, name string) bool {
	for _, t := range row.tests {
		if t == name {
			return true
		}
	}
	return false
}
