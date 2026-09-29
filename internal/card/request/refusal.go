package request

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// Refusal is one thing wrong with a request: the card layer's one refusal shape.
// Index is the entry's index in its array (-1 for the envelope or a scope), ID
// the card ID where it is safe to print, Field the key or dotted path within the
// entry, Found the offending value, quoted and bounded, Limit the bound for a
// bound and Next the remedy.
type Refusal = card.Refusal

// Refusals is every refusal found in one request, at most MaxRefusals, and the
// count of further ones left out. It is the error Parse and Validate return: a
// request that has any refusal is refused whole.
type Refusals = card.Refusals

// quote renders a value from the caller's input for a refusal: quoted, bounded
// and on one line.
func quote(s string) string { return card.Value(s) }

// knownID returns s for a refusal's card ID when it is a valid ID, and "" when
// it is not: an ID that may hold anything is never printed as one.
func knownID(s string) string {
	if !card.ValidID(s) {
		return ""
	}
	return s
}

type refKey struct {
	index int
	field string
}

// collector gathers refusals for one request.
type collector struct {
	card.Collector
	op   Operation
	seen map[refKey]bool
	// typed holds the fields refused as the wrong JSON type: whatever they were
	// meant to hold is absent, and refusing that absence again is noise.
	typed []refKey
}

func newCollector() *collector { return &collector{seen: map[refKey]bool{}} }

// underTyped says the field is, or lies within, a field already refused as the
// wrong type.
func (c *collector) underTyped(index int, field string) bool {
	for _, k := range c.typed {
		if k.index != index {
			continue
		}
		if k.field == "" || field == k.field || strings.HasPrefix(field, k.field+".") || strings.HasPrefix(field, k.field+"[") {
			return true
		}
	}
	return false
}

// add records one refusal. A required refusal for a field that already has one
// is dropped: the field is already refused for what it is.
func (c *collector) add(index int, id, field string, cause Cause, found, limit, next string) {
	k := refKey{index, field}
	if cause != CauseWrongType && c.underTyped(index, field) {
		return
	}
	if cause == CauseRequired && c.seen[k] {
		return
	}
	c.seen[k] = true
	if cause == CauseWrongType {
		c.typed = append(c.typed, k)
	}
	if c.Full() {
		c.Skip()
		return
	}
	c.Add(Refusal{Operation: card.Operation(c.op), Index: index, ID: id, Field: field, Cause: cause, Found: found, Limit: limit, Next: next})
}

func (c *collector) err() error {
	if e := c.Err(); e != nil {
		return e
	}
	return nil
}
