package pulse

// CardRow is one line of cards.tsv as launch reads it: the label, the model route, and the
// path to the card text whose bytes become the batch's task.
type CardRow struct {
	Label string
	Model string
	Card  string
}

// BenchRow is one model route: the cards launch admits under one nova-swarm batch, in
// cards.tsv order.
type BenchRow struct {
	Model string
	Cards []CardRow
}
