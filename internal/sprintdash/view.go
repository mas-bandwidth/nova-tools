package sprintdash

import "encoding/json"

// The review split (docs/SPEC-SPRINT.md section 1, "The review reason"): the dashboard's
// copy of `where --json --rows` carries every primary's review_reason field
// (sprint.ReviewReason), "read" or "defect". reviewSplit reads it and sets the copy's
// review_reads and review_defect, so the page's review number is the sum of the machine's
// work (reads) and the seat's (defect). The field is a view of state the sprint keeps
// (columns.go), so a brief defect is never silent: it is counted and named, never buried
// in the one review number.
//
// A copy with no rows (where --json without --rows) is left as it was: the split is
// counted from the cards, and no cards are no split.
func reviewSplit(body json.RawMessage) json.RawMessage {
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil {
		return body
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(top["rows"], &rows) != nil {
		return body
	}
	reads, defect := 0, 0
	for _, r := range rows {
		var col string
		if json.Unmarshal(r["column"], &col) != nil || col != "review" {
			continue
		}
		var fields map[string]string
		if json.Unmarshal(r["fields"], &fields) == nil && fields["review_reason"] == "defect" {
			defect++
		} else {
			reads++
		}
	}
	top["review_reads"] = mustJSON(reads)
	top["review_defect"] = mustJSON(defect)
	return mustJSON(top)
}
