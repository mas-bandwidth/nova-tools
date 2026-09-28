package jobs

// The status line's side of admission. A scheduler whose state cannot be read
// in one line is a scheduler nobody can tell is stuck, and "what is this bench
// holding" was a question the loop could only answer by guessing at ps output.
//
// Everything here is sorted before it is rendered, so two reads of the same
// state print the same bytes and a diff of two status lines is a real diff.

import (
	"fmt"
	"sort"
	"strings"
)

// DimView is one dimension of the authority: what it counts and what is drawn
// on it. Lanes are not here -- they are named in Lanes, because a lane's total
// is always 1 and printing `lane:docs=1/1` says less than printing `docs`.
type DimView struct {
	Name  string
	Used  int64
	Total int64
}

// GrantView is one live reservation as the status line shows it.
type GrantView struct {
	ID     string
	Parent string
	Vector Vector
	Lanes  []string
	Writes []string
}

// WriteView is one path a live grant is writing, and who holds it.
type WriteView struct {
	Path   string
	Holder string
}

// Snapshot is the whole authority at one instant: what is live, what is drawn
// on each counted dimension, which lanes are held and which paths are locked.
type Snapshot struct {
	Grants []GrantView
	Dims   []DimView
	Lanes  []string
	Writes []WriteView
}

// Snapshot reads the state on the writer goroutine, so what it returns is a
// consistent instant rather than a walk racing a grant.
func (a *Admission) Snapshot() Snapshot {
	var s Snapshot
	a.do(func(st *state) { s = st.snapshot() })
	return s
}

func (st *state) snapshot() Snapshot {
	s := Snapshot{}
	laneHeld := map[string]bool{}
	for _, id := range st.order {
		g := st.grants[id]
		if g == nil {
			continue
		}
		view := GrantView{ID: g.ID, Parent: g.Parent, Vector: g.Vector.Clone()}
		for _, dim := range g.Vector.Dims() {
			if strings.HasPrefix(dim, LaneDim) {
				name := strings.TrimPrefix(dim, LaneDim)
				view.Lanes = append(view.Lanes, name)
				laneHeld[name] = true
			}
		}
		view.Writes = append([]string(nil), g.Writes...)
		sort.Strings(view.Writes)
		s.Grants = append(s.Grants, view)
	}
	for _, name := range sortedKeys(laneHeld) {
		s.Lanes = append(s.Lanes, name)
	}
	counted := map[string]bool{}
	for dim := range st.capacity {
		if !strings.HasPrefix(dim, LaneDim) {
			counted[dim] = true
		}
	}
	for _, dim := range sortedKeys(counted) {
		used, _ := st.used(dim, nil)
		s.Dims = append(s.Dims, DimView{Name: dim, Used: used, Total: st.capacity[dim]})
	}
	paths := make([]string, 0, len(st.writers))
	for path := range st.writers {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		s.Writes = append(s.Writes, WriteView{Path: path, Holder: st.writers[path]})
	}
	return s
}

// Line is the one status line: what is live, which lanes, each counted
// dimension as used/total, and how many paths are locked. One line, bounded,
// and the same bytes for the same state.
func (s Snapshot) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "JOBS live=%d", len(s.Grants))
	if len(s.Lanes) > 0 {
		fmt.Fprintf(&b, " lanes=%s", strings.Join(s.Lanes, ","))
	} else {
		b.WriteString(" lanes=-")
	}
	for _, d := range s.Dims {
		fmt.Fprintf(&b, " %s=%d/%d", d.Name, d.Used, d.Total)
	}
	fmt.Fprintf(&b, " writes=%d", len(s.Writes))
	return b.String()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
