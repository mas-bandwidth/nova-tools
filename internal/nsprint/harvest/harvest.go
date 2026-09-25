package harvest

// HarvestDue checks whether a card is due for harvest according to check binding rules.
func HarvestDue(kind, pushedSha, checkHead string, ended bool, outcome string) bool {
	if kind == "script" {
		return false
	}
	if !ended || outcome != "DONE" {
		return false
	}
	if kind == "report" {
		return true
	}
	// check-bound
	if pushedSha != "-" && checkHead == pushedSha {
		return true
	}
	return false
}
