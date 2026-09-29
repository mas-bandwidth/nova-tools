package request

import (
	"bytes"
	"sort"
)

// Role is what a request does with a card it names.
type Role string

// The roles of a card in a request.
const (
	// RoleChanged is a card the request carries a change for: an admission, a
	// lifecycle input or an evidence entry.
	RoleChanged Role = "changed"
	// RoleGuardOnly is a card the request selects or reads without a change of
	// its own: the cards of a scope.
	RoleGuardOnly Role = "guard-only"
	// RoleOld is the old side of a replacement: the card to end.
	RoleOld Role = "old"
	// RoleNew is the new side of a replacement: the card to admit.
	RoleNew Role = "new"
	// RoleDependency is a card a request depends on: a prerequisite of an
	// admission, or the failed prerequisite of a dependency-failed input.
	RoleDependency Role = "dependency"
)

// CardRef is one card a request names: its ID, its role and, where the request
// gives them, its expected revision and place.
type CardRef struct {
	ID     ID
	Role   Role
	Expect *Expect
}

// Checked is what every validated request satisfies, whatever its operation: one
// uniform view for the layers above, so that no consumer switches on the
// operation or loops per entry type. Only a Valid satisfies it, and a Valid is
// only made by Validate or Parse.
type Checked interface {
	Kind() Operation
	OperationID() string
	Table() string
	Epoch() string
	ObservedRevision() string
	Actor() string
	Hash() Digest
	HashWithoutOperationID() Digest
	Canonical() []byte
	Cards() []CardRef
}

// Valid is a request that passed validation, with its canonical bytes and its
// hash. It has no exported field and no constructor but Validate and Parse, so a
// later layer cannot be handed a request that was not checked, and it holds a
// copy: whoever built the Request cannot change it afterwards.
type Valid struct {
	req    Request
	canon  []byte
	hash   Digest
	noOpID Digest
	opID   string
}

var _ Checked = (*Valid)(nil)

func newValid(req *Request) *Valid {
	cp := cloneRequest(req)
	canon := Canonical(&cp)
	v := &Valid{req: cp, canon: canon, hash: sum(canon), noOpID: HashWithoutOperationID(&cp)}
	if cp.Operation.Mutating() {
		v.opID = cp.OperationID
		if v.opID == "" {
			v.opID = DefaultOperationID(v.noOpID)
		}
	}
	return v
}

// DefaultOperationID is the operation ID a request that gives none is known by:
// "op-" and the first 16 hexadecimal characters of its hash without the
// operation ID. The same request asked again has the same ID, so its replay is
// recognised; a different request has a different one.
func DefaultOperationID(hashWithoutOperationID Digest) string {
	return "op-" + string(hashWithoutOperationID)[:16]
}

func cloneRequest(r *Request) Request {
	c := *r
	c.Admissions = append([]Admission(nil), r.Admissions...)
	for i := range c.Admissions {
		c.Admissions[i].DependsOn = append([]ID(nil), r.Admissions[i].DependsOn...)
	}
	if r.Admissions == nil {
		c.Admissions = nil
	}
	c.Inputs = cloneList(r.Inputs)
	c.Evidence = cloneList(r.Evidence)
	for i := range c.Evidence {
		c.Evidence[i].Records = cloneList(r.Evidence[i].Records)
	}
	c.Replacements = cloneList(r.Replacements)
	for i := range c.Replacements {
		c.Replacements[i].New.DependsOn = cloneList(r.Replacements[i].New.DependsOn)
	}
	if r.Scope != nil {
		s := *r.Scope
		s.IDs, s.Rows = cloneList(r.Scope.IDs), cloneList(r.Scope.Rows)
		c.Scope = &s
	}
	return c
}

// cloneList copies a slice, keeping nil as nil (an absent array and an empty one
// are not the same value to Validate).
func cloneList[T any](l []T) []T {
	if l == nil {
		return nil
	}
	return append([]T{}, l...)
}

// Kind is the request's operation.
func (v *Valid) Kind() Operation { return v.req.Operation }

// OperationID is the operation ID the request gives, or the default derived from
// its hash when it gives none. A read has none.
func (v *Valid) OperationID() string { return v.opID }

// Table is the table the request is for.
func (v *Valid) Table() string { return v.req.Table }

// Epoch is the epoch the request was issued at; empty for a read.
func (v *Valid) Epoch() string { return v.req.Epoch }

// ObservedRevision is the table revision the request observed; empty for a read.
func (v *Valid) ObservedRevision() string { return v.req.TableRevision }

// Actor is who made the request; empty for a read.
func (v *Valid) Actor() string { return v.req.Actor }

// Hash is the SHA-256 of the canonical bytes.
func (v *Valid) Hash() Digest { return v.hash }

// HashWithoutOperationID is the SHA-256 of the canonical bytes with the operation
// ID left out.
func (v *Valid) HashWithoutOperationID() Digest { return v.noOpID }

// Canonical returns the canonical bytes, as a copy.
func (v *Valid) Canonical() []byte { return append([]byte(nil), v.canon...) }

// Identity is the operation's identity for replay: table, epoch and the
// operation ID, given or derived.
func (v *Valid) Identity() Identity {
	return Identity{Table: v.req.Table, Epoch: v.req.Epoch, OperationID: v.opID}
}

// Request returns a copy of the request document.
func (v *Valid) Request() *Request { c := cloneRequest(&v.req); return &c }

// Cards enumerates every card the request names, with its role and its expected
// revision and place where the request gives them, sorted by ID and role. An
// admission is RoleChanged with no expectation (the card must be absent); the
// prerequisites it names outside the request's own admissions are
// RoleDependency, once each. A replacement gives RoleOld with its expectation and
// RoleNew. The IDs of a scope are RoleGuardOnly; a row scope, a whole-table scope
// and a check name no card.
func (v *Valid) Cards() []CardRef {
	r := &v.req
	var out []CardRef
	add := func(id ID, role Role, e *Expect) {
		if e != nil {
			c := *e
			e = &c
		}
		out = append(out, CardRef{ID: id, Role: role, Expect: e})
	}
	deps := func(admitted map[ID]bool, as ...*Admission) {
		seen := map[ID]bool{}
		for _, a := range as {
			for _, d := range a.DependsOn {
				if !admitted[d] && !seen[d] {
					seen[d] = true
					add(d, RoleDependency, nil)
				}
			}
		}
	}
	switch r.Operation {
	case OpAdmit:
		admitted := map[ID]bool{}
		var as []*Admission
		for i := range r.Admissions {
			admitted[r.Admissions[i].ID] = true
			as = append(as, &r.Admissions[i])
			add(r.Admissions[i].ID, RoleChanged, nil)
		}
		deps(admitted, as...)
	case OpApplyEvents:
		for i := range r.Inputs {
			e := r.Inputs[i].Expect
			add(r.Inputs[i].ID, RoleChanged, &e)
			if r.Inputs[i].Type.NamesDependency() {
				add(r.Inputs[i].Dependency, RoleDependency, nil)
			}
		}
	case OpRecordEvidence:
		for i := range r.Evidence {
			e := r.Evidence[i].Expect
			add(r.Evidence[i].ID, RoleChanged, &e)
		}
	case OpReplace:
		admitted := map[ID]bool{}
		var as []*Admission
		for i := range r.Replacements {
			e := r.Replacements[i].Old.Expect
			add(r.Replacements[i].Old.ID, RoleOld, &e)
			add(r.Replacements[i].New.ID, RoleNew, nil)
			admitted[r.Replacements[i].New.ID] = true
			as = append(as, &r.Replacements[i].New)
		}
		deps(admitted, as...)
	case OpResolve, OpInspect, OpCheck:
		if r.Scope != nil {
			for _, id := range r.Scope.IDs {
				add(id, RoleGuardOnly, nil)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Role < out[j].Role
	})
	return out
}

// SameRequest says whether an incoming validated request is the request that was
// recorded for an operation identity. recorded is the canonical bytes stored with
// the operation. It refuses recorded bytes that are not themselves canonical (they
// must parse as a valid request and re-canonicalise to exactly themselves) and
// compares bytes, never caller-supplied digests. Empty input is never the same
// request.
func SameRequest(recorded []byte, incoming *Valid) bool {
	if incoming == nil || len(recorded) == 0 {
		return false
	}
	r, err := Parse(recorded)
	if err != nil {
		return false
	}
	if !bytes.Equal(r.canon, recorded) {
		return false
	}
	return bytes.Equal(recorded, incoming.canon)
}
