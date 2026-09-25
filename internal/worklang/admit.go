package worklang

// The seam between the work language and the kernel. The reader holds the set;
// the scheduler computes the intersection and the admission (A5 to A9). This
// file is the one place a unit's FORM becomes an admission request, so
// `nova-work set check --ready`, the pull worker and the kernel ask the same
// question of the same bytes and cannot drift apart.
//
// It sits here rather than in internal/jobs because the dependency runs this
// way: the kernel depends on nothing, and the reader -- which already has the
// form in hand -- says what the unit consumes in the kernel's own terms.

import "github.com/mas-bandwidth/nova-tools/internal/jobs"

// Request is the unit's admission request: the vector it consumes and the paths
// it writes. The lane comes from :resources first and the plain :lane key
// second -- the reader already reconciles the two (A6) -- and it enters the
// vector as a NAMED dimension, because `lane:docs` and `lane:merge` do not
// contend and one opaque "lane" count could not tell them apart.
func (u Unit) Request() jobs.Request {
	req := jobs.Request{ID: u.ID, Vector: jobs.Vector{}, Writes: u.Writes()}
	for dim, n := range u.Resources() {
		if dim == "lane" {
			// The reader spells a lane's presence as lane=1 and carries the
			// name on the unit; naming it is what makes two lanes two
			// dimensions rather than one count of two.
			continue
		}
		req.Vector[dim] = n
	}
	if lane := u.Lane(); lane != "" {
		req.Vector[jobs.Lane(lane)] = 1
	}
	return req
}

// Requests builds the admission requests for units in written order, so a pass
// over a set is deterministic in the order the author wrote it.
func Requests(units []Unit) []jobs.Request {
	out := make([]jobs.Request, 0, len(units))
	for _, u := range units {
		out = append(out, u.Request())
	}
	return out
}
