package jobs

// Admission is the scheduling side of docs/SPEC-WORKLANG.md's Amendment 1 and
// docs/SPEC-JOBS.md section 9. The graph above answers "whose needs are closed";
// this answers the other half: "and are its resources free". Nothing else in the
// package decides whether a unit may run.
//
// Five rules, and every one of them is a rule about a REFUSAL rather than a wait:
//
//	A5  Admission takes a resource VECTOR, not a slot. A download asks for
//	    network and no cpu; a compiler asks for cpu and memory. A slot is the
//	    degenerate one-dimensional case and stops being the unit of admission.
//	A6  A lane is one dimension of that vector with capacity 1 over an area of
//	    the tree. queue/control/lanes.tsv is the map. Within a lane units are
//	    serial; across lanes they scatter and gather.
//	A7  Two units whose :writes intersect serialize EVEN WHEN THEIR LANES
//	    DIFFER, because an area of the tree and a file are not the same grain.
//	A8  One authority per physical capacity. Every other scheduler holds a
//	    DELEGATED sub-budget drawn from a parent's reservation and returned to
//	    it, never an independent count of the same cores. Admission of a vector
//	    is atomic across its dimensions -- all or none, never a partial grant a
//	    unit then waits inside -- and reserving the same capacity twice is a
//	    refusal, not a wait.
//	A9  No global barriers. A unit goes the moment its own needs are closed and
//	    its own vector is free, whatever every other unit is doing. There is no
//	    phase, no round, no wave.
//
// A9 is why Grant never blocks and this file holds no condition variable, no
// queue of waiters and no timeout: a request either is granted now or is refused
// now, naming the dimension that is short and who holds it. A caller that wants
// to try again asks again. A waiter inside admission would BE the barrier A9
// removes -- it would hold a partial grant while it waited, which is also what
// A8 forbids.
//
// One writer, single thread, like redis. One goroutine owns the state and every
// verb is a message to it, so the arithmetic is serialized by construction
// rather than by a lattice of locks nobody can prove. The package performs no
// output, reads no clock and reaches no network: capacity is never freed by a
// timer, because an expiry is UNKNOWN until termination is proved (A4, Stella's
// lease rule). Only Release frees a reservation.

import (
	"fmt"
	"sort"
	"strings"
)

// LaneDim is the prefix of a lane's dimension in a vector. A lane is named
// rather than counted -- `lane:docs` and `lane:merge` are two dimensions, each
// of capacity 1 -- because two units in two areas of the tree do not contend
// and one opaque "lane" count could not tell them apart.
const LaneDim = "lane:"

// Lane returns the dimension name of a lane.
func Lane(name string) string { return LaneDim + name }

// Vector is a resource request, or an authority's capacity, by dimension: cpu,
// memory-gb, disk-gb, network, gpu, a named scarce class, and a lane under the
// LaneDim prefix. A dimension a request does not name is a dimension it does
// not consume -- a download asks for network and no cpu -- and is never charged
// to it.
type Vector map[string]int64

// Clone copies a vector, so a grant holds its own and a caller cannot reach into
// the writer's state through the map it handed in.
func (v Vector) Clone() Vector {
	out := make(Vector, len(v))
	for k, n := range v {
		out[k] = n
	}
	return out
}

// Dims returns the vector's dimensions in sorted order. Everything this package
// renders walks this rather than a map, so two runs over the same state print
// the same bytes.
func (v Vector) Dims() []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Request is one unit asking to run: its id, the vector it consumes, the paths
// it writes, and the grant it draws from.
type Request struct {
	// ID is the unit's stable id (A2). It is the grant's id too: one live
	// reservation per unit, so asking twice is caught by name.
	ID string
	// Vector is what the unit actually consumes. A lane belongs in it under
	// Lane(name); the capacity of a lane is always 1 and is never declared.
	Vector Vector
	// Writes are the repo-relative paths the unit edits. Two live grants whose
	// writes intersect cannot both hold, whatever their lanes (A7).
	Writes []string
	// Parent is the grant this one is drawn from, or "" for the one authority.
	// A nested scheduler -- a CI runner set, an external engine -- holds its
	// sub-budget as a grant and admits its own units under it (A8).
	Parent string
}

// Grant is a live reservation. It is returned to the caller by value: the
// writer's copy is the only one that counts, and a caller cannot edit it.
type Grant struct {
	ID     string
	Vector Vector
	Writes []string
	Parent string
}

// Refusal is why a request was not granted. It is an error rather than a wait,
// and it names the one thing that is short so the caller does not have to
// re-derive it: the dimension, what was asked, what was free, and who holds it.
type Refusal struct {
	ID string
	// Dim is the dimension that refused, or "" when the refusal is not about a
	// dimension at all (an unknown parent, a double reservation).
	Dim string
	// Path is the intersecting write, when that is what refused.
	Path string
	Want int64
	Free int64
	// Holder is the grant already holding what was asked for, when there is one.
	Holder string
	// Authority is the reservation the arithmetic was done against: "" is the
	// one authority, otherwise the parent grant the sub-budget was drawn from.
	Authority string
	Reason    string
}

func (r *Refusal) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "admission refused %s: %s", r.ID, r.Reason)
	if r.Dim != "" {
		fmt.Fprintf(&b, " (%s want=%d free=%d", r.Dim, r.Want, r.Free)
		if r.Authority != "" {
			fmt.Fprintf(&b, " under=%s", r.Authority)
		}
		b.WriteString(")")
	}
	if r.Path != "" {
		fmt.Fprintf(&b, " (writes %s", r.Path)
		if r.Holder != "" {
			fmt.Fprintf(&b, " held by %s", r.Holder)
		}
		b.WriteString(")")
	} else if r.Holder != "" && r.Dim != "" {
		fmt.Fprintf(&b, " held by %s", r.Holder)
	}
	return b.String()
}

// Admission is the one authority over a set of capacities. Build it with New,
// stop it with Close, and every verb between is a message to the single writer
// that owns the state.
type Admission struct {
	cmds chan func(*state)
}

// state is what the writer goroutine owns. Nothing outside the goroutine ever
// touches a field of it.
type state struct {
	capacity Vector
	grants   map[string]*Grant
	order    []string
	children map[string][]string
	writers  map[string]string
}

// New starts an Admission over a capacity. Lanes are NOT declared: a lane is
// capacity 1 by A6 and declaring it would be a second place for that number to
// live. Every other dimension a request names must be declared here, because a
// dimension no authority counts is exactly the double-counting A8 forbids --
// load average 147 on the Studio was four independent counters over 32 cores.
func New(capacity Vector) *Admission {
	a := &Admission{cmds: make(chan func(*state))}
	st := &state{
		capacity: capacity.Clone(),
		grants:   map[string]*Grant{},
		children: map[string][]string{},
		writers:  map[string]string{},
	}
	go func() {
		for cmd := range a.cmds {
			cmd(st)
		}
	}()
	return a
}

// Close stops the writer. An Admission is not used after it.
func (a *Admission) Close() { close(a.cmds) }

// do runs fn on the writer goroutine and waits for it. This is the whole of the
// concurrency: one channel, one owner, no lock held across a decision.
func (a *Admission) do(fn func(*state)) {
	done := make(chan struct{})
	a.cmds <- func(st *state) {
		fn(st)
		close(done)
	}
	<-done
}

// Grant admits a request, or refuses it. It NEVER waits: A9 says a unit goes
// when its own vector is free, so a request whose vector is not free is told so
// at once, naming what is short. Admission is atomic over the vector -- the
// whole request is checked before one dimension of it is recorded -- so no
// caller ever holds a partial grant.
func (a *Admission) Grant(req Request) (Grant, error) {
	var out Grant
	var err error
	a.do(func(st *state) { out, err = st.grant(req) })
	return out, err
}

// Release returns a grant's reservation to the authority it was drawn from. It
// is the ONLY thing that frees capacity: no clock does, because an expiry is
// unknown until termination is proved.
//
// A parent with live children is refused rather than released: freeing a
// reservation a child is still drawing from would hand the same capacity out
// twice, which is the bug A8 exists to forbid. The refusal names the children.
func (a *Admission) Release(id string) error {
	var err error
	a.do(func(st *state) { err = st.release(id) })
	return err
}

// Held reports whether a grant is live, and its vector.
func (a *Admission) Held(id string) (Grant, bool) {
	var g Grant
	var ok bool
	a.do(func(st *state) {
		if held, live := st.grants[id]; live {
			g, ok = *held, true
			g.Vector = held.Vector.Clone()
			g.Writes = append([]string(nil), held.Writes...)
		}
	})
	return g, ok
}

func (st *state) grant(req Request) (Grant, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return Grant{}, &Refusal{ID: req.ID, Reason: "a request needs the unit's id; refusing to grant to nobody"}
	}
	// A8: reserving the same capacity twice is a refusal, not a wait. The
	// second ask is a bug in the caller, and telling it to wait would hide it.
	if held, live := st.grants[id]; live {
		return Grant{}, &Refusal{ID: id, Holder: id, Reason: fmt.Sprintf(
			"%s already holds a reservation (%s); one live grant per unit, and a second is a refusal, not a wait",
			id, render(held.Vector))}
	}
	var parent *Grant
	if req.Parent != "" {
		p, live := st.grants[req.Parent]
		if !live {
			return Grant{}, &Refusal{ID: id, Authority: req.Parent, Reason: fmt.Sprintf(
				"no live grant %q to draw from; a nested grant is drawn from its parent's reservation, never counted independently",
				req.Parent)}
		}
		parent = p
	}

	// A8, atomicity: every dimension is checked against the authority BEFORE
	// one of them is recorded. A partial grant is never written, so there is
	// nothing for a refused request to be holding when it is told no.
	for _, dim := range req.Vector.Dims() {
		want := req.Vector[dim]
		if want <= 0 {
			// A dimension asked for zero is a dimension not consumed: a
			// download asking (:cpu 0) is asking for no cpu, not for a cpu.
			continue
		}
		total, ok := st.total(dim, parent)
		if !ok {
			where := "this authority counts no such capacity"
			if parent != nil {
				where = fmt.Sprintf("%s's reservation does not carry it", parent.ID)
			}
			return Grant{}, &Refusal{ID: id, Dim: dim, Want: want, Authority: authorityOf(parent), Reason: fmt.Sprintf(
				"%s is not a capacity this request may draw on: %s; declare it on the authority or drop it from the vector",
				dim, where)}
		}
		used, holder := st.used(dim, parent)
		if free := total - used; want > free {
			return Grant{}, &Refusal{ID: id, Dim: dim, Want: want, Free: free, Holder: holder,
				Authority: authorityOf(parent), Reason: reasonFor(dim, parent)}
		}
	}

	// A7: the writes intersection, checked with the same all-or-none discipline
	// and against every live grant whatever its lane.
	for _, path := range req.Writes {
		clean := cleanPath(path)
		if clean == "" {
			continue
		}
		for _, other := range st.order {
			held := st.grants[other]
			if held == nil {
				continue
			}
			for _, theirs := range held.Writes {
				if pathsIntersect(clean, cleanPath(theirs)) {
					return Grant{}, &Refusal{ID: id, Path: path, Holder: other, Reason: fmt.Sprintf(
						"%s writes %s, which %s is already writing; intersecting writes serialize even when the lanes differ",
						id, path, other)}
				}
			}
		}
	}

	g := &Grant{ID: id, Vector: req.Vector.Clone(), Parent: req.Parent,
		Writes: append([]string(nil), req.Writes...)}
	st.grants[id] = g
	st.order = append(st.order, id)
	if req.Parent != "" {
		st.children[req.Parent] = append(st.children[req.Parent], id)
	}
	for _, path := range g.Writes {
		if clean := cleanPath(path); clean != "" {
			st.writers[clean] = id
		}
	}
	out := *g
	out.Vector = g.Vector.Clone()
	out.Writes = append([]string(nil), g.Writes...)
	return out, nil
}

// total is the capacity of one dimension under one authority. A lane is 1
// whoever asks (A6). Under a parent the total is what the PARENT holds, never
// the machine's: that is what "a delegated sub-budget" means, and it is why a
// child cannot outgrow its parent even when the machine is idle.
func (st *state) total(dim string, parent *Grant) (int64, bool) {
	if strings.HasPrefix(dim, LaneDim) {
		return 1, true
	}
	if parent != nil {
		n, ok := parent.Vector[dim]
		return n, ok
	}
	n, ok := st.capacity[dim]
	return n, ok
}

// used is what is already drawn on one dimension under one authority, and the
// last grant that drew on it. A lane is global: its capacity-1 area of the tree
// is the same area whichever scheduler is asking, so a nested grant cannot mint
// a second copy of a lane its parent does not hold.
func (st *state) used(dim string, parent *Grant) (int64, string) {
	var used int64
	var holder string
	for _, id := range st.order {
		g := st.grants[id]
		if g == nil {
			continue
		}
		if !strings.HasPrefix(dim, LaneDim) {
			// Only the authority's own direct children are charged to it: a
			// grandchild's cores are already inside its parent's reservation,
			// and charging them twice is the double count A8 forbids.
			if parent == nil {
				if g.Parent != "" {
					continue
				}
			} else if g.Parent != parent.ID {
				continue
			}
		}
		if n := g.Vector[dim]; n > 0 {
			used += n
			holder = id
		}
	}
	return used, holder
}

func authorityOf(parent *Grant) string {
	if parent == nil {
		return ""
	}
	return parent.ID
}

// reasonFor writes the refusal's sentence. A lane and a counted dimension fail
// for different reasons and a caller acts on them differently, so they are not
// spelled the same.
func reasonFor(dim string, parent *Grant) string {
	if strings.HasPrefix(dim, LaneDim) {
		return fmt.Sprintf("the %s lane has capacity 1 and is live; within a lane units are serial",
			strings.TrimPrefix(dim, LaneDim))
	}
	if parent != nil {
		return fmt.Sprintf("%s's remaining reservation is short of %s; a nested grant draws from its parent, not from the machine",
			parent.ID, dim)
	}
	return fmt.Sprintf("the authority is short of %s; the vector is granted whole or not at all", dim)
}

func (st *state) release(id string) error {
	g, live := st.grants[id]
	if !live {
		return &Refusal{ID: id, Reason: fmt.Sprintf("no live grant %q to release", id)}
	}
	if kids := st.liveChildren(id); len(kids) > 0 {
		return &Refusal{ID: id, Holder: kids[0], Reason: fmt.Sprintf(
			"%s still holds %d nested grant(s) (%s); a parent's reservation is returned only after the grants drawn from it",
			id, len(kids), strings.Join(kids, ", "))}
	}
	delete(st.grants, id)
	for i, held := range st.order {
		if held == id {
			st.order = append(st.order[:i], st.order[i+1:]...)
			break
		}
	}
	if g.Parent != "" {
		kids := st.children[g.Parent]
		for i, kid := range kids {
			if kid == id {
				st.children[g.Parent] = append(kids[:i], kids[i+1:]...)
				break
			}
		}
	}
	for path, holder := range st.writers {
		if holder == id {
			delete(st.writers, path)
		}
	}
	return nil
}

func (st *state) liveChildren(id string) []string {
	var out []string
	for _, kid := range st.children[id] {
		if _, live := st.grants[kid]; live {
			out = append(out, kid)
		}
	}
	return out
}

// cleanPath normalizes a :writes entry for comparison: no leading "./", no
// trailing slash. A directory and the same directory with a slash are one path.
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	for strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// pathsIntersect is A7's grain. Two writes intersect when they are the same
// path or one is a directory containing the other: a unit writing
// internal/worklang/ and a unit writing internal/worklang/units.go touch the
// same bytes, and a comparison by string equality would let them run together.
func pathsIntersect(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func render(v Vector) string {
	parts := make([]string, 0, len(v))
	for _, dim := range v.Dims() {
		parts = append(parts, fmt.Sprintf("%s=%d", dim, v[dim]))
	}
	return strings.Join(parts, " ")
}
