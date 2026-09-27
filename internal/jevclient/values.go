package jevclient

import "strconv"

// Kind names which of the three question types q is, or "" for a malformed
// question.
func (q Question) Kind() string {
	switch {
	case q.Choice != nil:
		return "choice"
	case q.Score != nil:
		return "score"
	case q.Noul:
		return "noul"
	default:
		return ""
	}
}

// Value renders one answer as the table's answer column: the chosen option,
// the score, or the noul value.
func (a Answer) Value() string {
	switch a.Type {
	case "score":
		return strconv.FormatFloat(a.Score, 'g', -1, 64)
	case "noul":
		return strconv.FormatFloat(a.Noul, 'g', -1, 64)
	default:
		return a.Choice
	}
}
