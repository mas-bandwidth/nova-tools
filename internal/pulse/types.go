package pulse

// PoolRow is one candidate in pool.tsv: where it came from, its id there, its kind,
// its title, and the template its card will be cut from.
type PoolRow struct {
	Source   string
	ID       string
	Kind     string
	Title    string
	Template string
}
