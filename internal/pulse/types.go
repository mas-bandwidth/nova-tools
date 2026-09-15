package pulse

// CardRow is one line of cards.tsv: the label, the slot (a dash until launch allocates
// one, and read and discarded by launch), the model route, and the path to the card text
// whose bytes become the batch's task.
type CardRow struct {
	Label string
	Slot  string
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
