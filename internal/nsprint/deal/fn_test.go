package deal

import (
	"fmt"
)

func poolCards(sprint string, n int) []Card {
	cards := make([]Card, n)
	for i := range cards {
		cards[i] = Card{Sprint: sprint, Label: fmt.Sprintf("card-%02d", i), Priority: float64(i)}
	}
	return cards
}
