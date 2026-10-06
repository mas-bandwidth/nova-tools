package main

import (
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// issuesPending is where's line of the issues landings have not closed on GitHub yet
// (sprint.PropIssuesPending, the closer's last pass; docs/SPEC-SPRINT.md section 7), read from
// the work table's shape so where reads no card: "" when none is pending.
func issuesPending(shapes []ntable.Table) string {
	i := slices.Index(sprint.ViewOrder, sprint.Work)
	if i < 0 || i >= len(shapes) {
		return ""
	}
	v := shapes[i].Props[sprint.PropIssuesPending]
	if v == "" || v == "0" {
		return ""
	}
	return v
}

// issuesLine is the text frame's line of the pending closes, with its one next command.
func issuesLine(pending string) string {
	return "ISSUES PENDING " + pending + "; the server's loop closes them on GitHub (nova-sprint run)"
}
