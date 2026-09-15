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

// PoolRow is one candidate in pool.tsv: where it came from, its id there, its kind,
// its title, and the template its card will be cut from.
type PoolRow struct {
	Source   string
	ID       string
	Kind     string
	Title    string
	Template string
}
