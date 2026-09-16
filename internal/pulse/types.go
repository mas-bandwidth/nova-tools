package pulse

// CardRow is one row of cards.tsv: the label the card was admitted under, the slot it
// holds ("-" until launch allocates one), the model, the card's admission token bound, and
// the card file's path.
type CardRow struct {
	Label  string
	Slot   string
	Model  string
	Tokens int
	Card   string
}

// Candidate is one row of bounded work: a source kind, an item id, a card kind,
// a title and the template to render it from. pool.tsv, queue.tsv and next.tsv all hold
// candidates in the same five-field shape.
type Candidate struct {
	Source   string
	ID       string
	Kind     string
	Title    string
	Template string
}

// PoolRow is a candidate as pool.tsv holds it: pool.tsv, queue.tsv and next.tsv share the
// Candidate shape, and pool names it for the file it lives in.
type PoolRow = Candidate

// BenchRow is one model route: the cards launch admits under one nova-swarm batch, in
// cards.tsv order.
type BenchRow struct {
	Model string
	Cards []CardRow
}
