package pulse

import "testing"

func TestHowPRNamesIssue(t *testing.T) {
	for _, c := range []struct {
		title, body, branch string
		issue               int
		want                string
	}{
		{"the widget", "Fixes #1979\n\nthe repair.", "rowan/the-widget", 1979, "Fixes #1979"},
		{"the widget", "Closes #1979", "rowan/x", 1979, "Closes #1979"},
		{"cut: skip a card already in flight (#1979)", "the repair", "rowan/cut-skip", 1979, "title #1979"},
		{"the widget", "see also #1979 in passing", "rowan/x", 1979, "body #1979"},
		{"the widget", "no issue ref here", "johnny/1979-the-widget", 1979, "branch=johnny/1979-the-widget"},
		{"the widget", "no issue ref here", "rowan/issue-1979-foo", 1979, "branch=rowan/issue-1979-foo"},
		{"unrelated", "Fixes #2", "rowan/unrelated", 1979, ""},
		{"the widget", "Fixes #19790", "johnny/19790-the-widget", 1979, ""},
		{"the widget", "Fixes #1979", "rowan/x", 19790, ""},
	} {
		got := howPRNamesIssue(c.title, c.body, c.branch, c.issue)
		if got != c.want {
			t.Errorf("howPRNamesIssue(%q, %q, %q, %d) = %q, want %q",
				c.title, c.body, c.branch, c.issue, got, c.want)
		}
	}
}
