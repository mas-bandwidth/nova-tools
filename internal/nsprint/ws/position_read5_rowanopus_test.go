package ws

import (
	"strings"
	"testing"
)

// Rowan-opus cold read of #4410 at 69b5f712e, probe 4: three cards, two
// numbered and one not, in a mixed age order; then the same two cards with
// and without the third. Logs only: the rule's result is a finding, not a
// pass/fail the card pinned.
func TestReadPositionThreeCards_RowanOpus(t *testing.T) {
	t.Parallel()
	ids := func(cs []OrderCard) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return strings.Join(out, " ")
	}
	a := OrderCard{ID: "a5", Issue: 5, Created: 100} // oldest, higher issue
	b := OrderCard{ID: "b3", Issue: 3, Created: 400} // youngest, lower issue
	u := OrderCard{ID: "u", Created: 200}            // no issue, middle age
	t.Logf("{a5,b3,u} -> %s", ids(Position([]OrderCard{a, b, u})))
	t.Logf("{a5,u}    -> %s", ids(Position([]OrderCard{a, u})))
	t.Logf("{b3,u}    -> %s", ids(Position([]OrderCard{b, u})))
	oid := func(os []Ordered) string {
		var out []string
		for _, o := range os {
			out = append(out, o.ID)
		}
		return strings.Join(out, " ")
	}
	o3, _, _ := Order([]OrderCard{a, b, u})
	o2, _, _ := Order([]OrderCard{a, u})
	t.Logf("Order {a5,b3,u} -> %s; Order {a5,u} -> %s", oid(o3), oid(o2))
}
