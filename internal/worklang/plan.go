package worklang

import (
	"fmt"
	"strconv"
	"strings"
)

// The six card classes docs/SPEC-WORKLANG.md fixes, plus the two container
// kinds a node may carry. An unknown :kind is refused, never guessed.
var knownKinds = []string{
	"go-fix", "lisp-replay", "docs", "schema-leg", "audit", "fold",
	"epic", "work-set",
}

// KnownKinds returns the kind names the plan grammar admits, in spec order.
func KnownKinds() []string { return append([]string(nil), knownKinds...) }

func isKnownKind(name string) bool {
	for _, k := range knownKinds {
		if k == name {
			return true
		}
	}
	return false
}

// nodeKeys are the fields a node carries. A key not listed here is preserved in
// Node.Unknown and ignored, exactly as O already does with unknown keys.
var nodeKeys = map[string]bool{
	"id": true, "under": true, "kind": true, "needs": true, "blocks": true,
	"repo": true, "base": true, "inputs": true, "output": true, "budget": true,
	"affinity": true, "as": true, "from": true, "over": true, "acceptance": true,
	"title": true, "type": true,
	// Amendment 1 (2026-09-18): the same keys a unit carries, so one grammar
	// serves the plan form and the work-set form. Parse only -- the reader
	// checks their shape in checkUnitField and schedules nothing.
	"lane": true, "resources": true, "writes": true, "tools": true,
	"collects": true, "warm": true, "attempts": true, "state": true,
	"owner": true, "was": true, "deadline": true,
}

// Node is one `:node` of a plan: its known Fields and the unknown keys beside
// them, preserved so a later slice can read what this one ignores.
type Node struct {
	Fields  map[string]Form
	Unknown map[string]Form
}

// Kind is the node's :kind name, or "" when it carries none.
func (n Node) Kind() string {
	if f, ok := n.Fields["kind"]; ok && (f.Kind == Keyword || f.Kind == Symbol) {
		return f.Value
	}
	return ""
}

// ID is the node's :id string, or "" when it carries none.
func (n Node) ID() string {
	if f, ok := n.Fields["id"]; ok && f.Kind == String {
		return f.Value
	}
	return ""
}

// Plan is the reader's view of a `.work` file. Unknown forms at the top level
// are preserved and ignored.
type Plan struct {
	File    string
	Version int64
	Goal    *Form
	Nodes   []Node
	Unknown []Form
}

// ParsePlan reads a plan and validates the fields the reader owns: every
// `:kind` must be a known kind, refused naming the field otherwise. An unknown
// key beside it is preserved in Node.Unknown and otherwise ignored.
func ParsePlan(file string, data []byte, limits Limits) (*Plan, error) {
	root, err := Read(file, data, limits)
	if err != nil {
		return nil, err
	}
	if root.Kind != List || len(root.List) == 0 || !root.List[0].IsKeyword("plan") {
		return nil, refuse(file, "not a plan: the top form must be (:plan ...)")
	}
	p := &Plan{File: file}
	rest := root.List[1:]
	i := 0
	if i < len(rest) && rest[i].IsKeyword("version") {
		if i+1 >= len(rest) || rest[i+1].Kind != Integer {
			return nil, refuse(file, ":version must be an integer; refusing to guess")
		}
		p.Version = rest[i+1].Int
		i += 2
	}
	for ; i < len(rest); i++ {
		el := rest[i]
		if el.Kind == List && len(el.List) > 0 && el.List[0].Kind == Keyword {
			switch el.List[0].Value {
			case "node":
				n, err := parseNode(file, el)
				if err != nil {
					return nil, err
				}
				p.Nodes = append(p.Nodes, n)
				continue
			case "goal":
				goal := el
				p.Goal = &goal
				continue
			}
		}
		p.Unknown = append(p.Unknown, el)
	}
	return p, nil
}

// parseNode reads one `:node` form as alternating keyword/value pairs. The
// `:kind` value is validated against the known kinds and refused by field name
// when it is not one of them.
func parseNode(file string, form Form) (Node, error) {
	n := Node{Fields: map[string]Form{}, Unknown: map[string]Form{}}
	body := form.List[1:]
	for i := 0; i < len(body); i += 2 {
		key := body[i]
		if key.Kind != Keyword {
			return n, refuse(file, fmt.Sprintf("expected a keyword at byte=%d in a :node", key.Offset))
		}
		if i+1 >= len(body) {
			return n, refuse(file, fmt.Sprintf(":%s has no value; refusing to guess", key.Value))
		}
		val := body[i+1]
		if key.Value == "kind" && ((val.Kind != Keyword && val.Kind != Symbol) || !isKnownKind(val.Value)) {
			return n, refuse(file, fmt.Sprintf(
				":kind %s is not one of %s; refusing to guess",
				renderVal(val), strings.Join(knownKinds[:6], ", ")))
		}
		if err := checkUnitField(file, n.ID(), key, val); err != nil {
			return n, err
		}
		if nodeKeys[key.Value] {
			n.Fields[key.Value] = val
		} else {
			n.Unknown[key.Value] = val
		}
	}
	return n, nil
}

func renderVal(f Form) string {
	switch f.Kind {
	case Keyword, Symbol:
		return f.Value
	case String:
		return strconv.Quote(f.Value)
	case Integer:
		return strconv.FormatInt(f.Int, 10)
	default:
		return "()"
	}
}
