package sprint

import "time"

// RuleAnswersInHour is how many notes in the hour ending at now record a rule
// answer (RuleSaid). view coordinator does not show it: cmd/nova-sprint/view.go:114
// (coordCounts) is outside this card, and the count is not added there.
func RuleAnswersInHour(notes []Note, now time.Time) int {
	n := 0
	from := now.Add(-time.Hour)
	for _, note := range notes {
		if !stringsHasRule(note.What) {
			continue
		}
		if note.At.Before(from) || note.At.After(now) {
			continue
		}
		n++
	}
	return n
}

func stringsHasRule(what string) bool {
	return len(what) >= len(NRuleAnswered)+1 && what[:len(NRuleAnswered)+1] == NRuleAnswered+" "
}
