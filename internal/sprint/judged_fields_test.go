package sprint

import "testing"

// Every field CardJudgmentFields names is of a row of 2.2 on a card subject,
// and every such row has a field (but the one no rule raises yet), so a drop
// that closes the list closes every judgment a card holds (DropEff).
func TestCardJudgmentFieldsCoverTheCardRows(t *testing.T) {
	t.Parallel()
	card := map[string]bool{subjSentinel: true, subjWaiter: true, subjPrimary: true, subjCard: true}
	has := map[string]bool{}
	fields := append(CardJudgmentFields(), BlockedJudgmentFields([]string{"n1"})...)
	for _, f := range fields {
		row, ok := Judgments[f.Type]
		if !ok || !card[row.Subject] {
			t.Fatalf("%s is no row of 2.2 on a card", f.Field())
		}
		if f.Cause == "" {
			t.Fatalf("%s has no cause: J refuses a state request with none", f.Type)
		}
		has[f.Type] = true
	}
	for typ, row := range Judgments {
		if card[row.Subject] && !has[typ] && typ != NInvariant {
			t.Errorf("the card row %q has no field", typ)
		}
	}
	if TableWord(NCannotAsk) != TypeCannotAsk || PlannerWord(TypeCIRed) != NCIRed {
		t.Fatal("the planner's words map to the table's")
	}
}
