package pulse

// PoolRow is one candidate in pool.tsv: source, id, kind, title, template, in source order.
type PoolRow struct {
	Source   string
	ID       string
	Kind     string
	Title    string
	Template string
}

// CardRow is one cut card in cards.tsv: label, slot, model, card path, in that order.
type CardRow struct {
	Label string
	Slot  string
	Model string
	Card  string
}
