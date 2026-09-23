package fold

// TemplateDecision holds the A/B test decision for a card type.
type TemplateDecision struct {
	TypeID string // the card type ID
	Winner string // "A", "B", or "" if no winner
	// CostPerA and CostPerB are cost per useful card (cost / score). When a
	// variant's score is zero the ratio is undefined; it is reported as 0
	// (never +Inf or NaN) and Winner is "".
	CostPerA float64
	CostPerB float64
}

// costPerUseful returns cost / score, or 0 when score is zero so the
// exported decision never carries +Inf or NaN.
func costPerUseful(cost, score float64) float64 {
	if score == 0 {
		return 0
	}
	return cost / score
}

// DeclareWinner determines A/B winners for a set of card types and returns
// the recommendations for template selection.
// This is the production path that folds A/B test results into template decisions.
func DeclareWinner(types map[string]*ABTestData, interval float64) []TemplateDecision {
	var decisions []TemplateDecision
	for typeID, data := range types {
		winner := ABWinner(typeID, data.CostA, data.ScoreA, data.CostB, data.ScoreB, interval)
		decisions = append(decisions, TemplateDecision{
			TypeID:   typeID,
			Winner:   winner,
			CostPerA: costPerUseful(data.CostA, data.ScoreA),
			CostPerB: costPerUseful(data.CostB, data.ScoreB),
		})
	}
	return decisions
}

// ABTestData holds the A/B test metrics for a card type.
type ABTestData struct {
	CostA  float64 // total cost for template A
	ScoreA float64 // Jev calibrated usefulness score for A
	CostB  float64 // total cost for template B
	ScoreB float64 // Jev calibrated usefulness score for B
}

// ABWinner determines the A/B test winner for a given card type based on cost per
// useful card. It returns "A" if template A wins, "B" if template B wins, or ""
// if no winner can be named (difference not beyond interval).
//
// The cost per useful card is calculated as: cost / usefulness_score
// where usefulness_score is Jev's calibrated score. Higher scores mean more useful,
// so lower cost-per-usefulness is better.
//
// A winner is named only when the difference in cost per useful card exceeds
// the stated interval (threshold).
func ABWinner(
	typeID string,
	costA, scoreA float64,
	costB, scoreB float64,
	interval float64,
) string {
	// Calculate cost per useful card for each variant
	// Lower cost per useful card is better
	if scoreA == 0 || scoreB == 0 {
		// Avoid division by zero; no winner if either score is zero
		return ""
	}

	costPerUsefulA := costPerUseful(costA, scoreA)
	costPerUsefulB := costPerUseful(costB, scoreB)

	// Determine the absolute difference in cost per useful card
	// The winner is the one with lower cost per useful card
	diff := costPerUsefulA - costPerUsefulB
	if diff < 0 {
		diff = -diff
	}

	// A winner is only named if the difference exceeds the interval (strictly greater)
	if diff <= interval {
		return ""
	}

	// Return the winner (the one with lower cost per useful card)
	if costPerUsefulA < costPerUsefulB {
		return "A"
	}
	return "B"
}
