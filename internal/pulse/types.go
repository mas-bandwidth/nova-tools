package pulse

// CardRow is one row of cards.tsv: the label the card was admitted under, the slot it
// holds ("-" until launch allocates one), the model, and the card file's path.
type CardRow struct {
	Label string
	Slot  string
	Model string
	Card  string
}

// Candidate is one row of bounded work to cut: a source kind, an item id, a card kind,
// a title and the template to render it from. pool.tsv, queue.tsv and next.tsv all hold
// candidates in the same five-field shape.
type Candidate struct {
	Source   string
	ID       string
	Kind     string
	Title    string
	Template string
}
