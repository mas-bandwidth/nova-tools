package docs

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// issue2080Sexp is the roadmap the E09-F01 row lives in.
const issue2080Sexp = "../../docs/roadmaps/nova-work.sexp"

// issue2080Row matches one (:feature "E.." :verified N :total M :tests "...") row.
var issue2080Row = regexp.MustCompile(`\(:feature "(E\d+-F\d+)" :verified (\d+) :total (\d+) :tests "([^"]*)"`)

// TestIssue2080 holds the E09-F01 row of the roadmap to the tests that prove
// it (nova-tools#2080). The row may claim 3/3 only because it names Go tests
// that exist in this tree -- internal/ghcapture's captured-bundle adapter tests
// for identity/revision/URL and for body/comments/labels/relationships/
// attachments/pagination -- beside the existing archive-completeness; a named
// test that is not in the named file fails here, so the count cannot rise from
// a string. The roadmap's own totals must still add up after the rise.
func TestIssue2080(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(issue2080Sexp)
	if err != nil {
		t.Fatal(err)
	}
	sexp := string(b)

	var sumVerified, fullFeatures int
	var row []string
	for _, m := range issue2080Row.FindAllStringSubmatch(sexp, -1) {
		v, _ := strconv.Atoi(m[2])
		tot, _ := strconv.Atoi(m[3])
		sumVerified += v
		if v == tot {
			fullFeatures++
		}
		if m[1] == "E09-F01" {
			row = m
		}
	}
	if row == nil {
		t.Fatalf("%s: no E09-F01 row", issue2080Sexp)
	}
	if row[2] != "3" || row[3] != "3" {
		t.Fatalf("E09-F01 :verified %s :total %s, want 3/3 from the adapter tests", row[2], row[3])
	}

	// :tests entries are "; "-separated; a Go entry is "<file>: TestA, TestB".
	var named []string
	haveArchive := false
	for _, entry := range strings.Split(row[4], ";") {
		entry = strings.TrimSpace(entry)
		if entry == "archive-completeness" {
			haveArchive = true
			continue
		}
		file, list, ok := strings.Cut(entry, ": ")
		if !ok || !strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile("../../" + file)
		if err != nil {
			t.Errorf("E09-F01 names %s, which is not in this tree: %v", file, err)
			continue
		}
		for _, name := range strings.Split(list, ",") {
			name = strings.TrimSpace(name)
			if !strings.Contains(string(src), "func "+name+"(t *testing.T)") {
				t.Errorf("E09-F01 names %s in %s, which does not define it", name, file)
			}
			named = append(named, name)
		}
	}
	if !haveArchive {
		t.Error("E09-F01 no longer names archive-completeness; E09-F01-03 must stay verified")
	}
	for _, want := range []string{"TestStableIdentityRevisionAndURL", "TestBodyCommentsLabelsRelationshipsAndPagination"} {
		found := false
		for _, n := range named {
			found = found || n == want
		}
		if !found {
			t.Errorf("E09-F01 does not name %s; criteria E09-F01-01 and -02 are verified only by the adapter tests", want)
		}
	}

	// The roadmap's own totals follow the rows.
	for key, want := range map[string]int{":verified-acceptance-items": sumVerified, ":verified-features": fullFeatures} {
		m := regexp.MustCompile(regexp.QuoteMeta(key) + ` (\d+)`).FindStringSubmatch(sexp)
		if m == nil {
			t.Fatalf("%s missing", key)
		}
		if got, _ := strconv.Atoi(m[1]); got != want {
			t.Errorf("%s %d, but the :by-feature rows add up to %d", key, got, want)
		}
	}
}
