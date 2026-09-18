package jobs

// One admission pass, which is A9 in code.
//
// The pass takes requests already known to be READY -- their needs closed, the
// graph's half of the question -- and answers the other half for each: is its
// own vector free NOW. It never stops at a refusal and never waits for one, so
// a request whose vector is free goes whatever the request before it is doing.
// A pass that stopped at the first refusal would be a barrier wearing a loop's
// clothes, and the pit-stop set of 2026-09-17 is the argument against it: 54 of
// 79 units name needs and the rest are independent, so a barrier idles most of
// the fleet behind the slowest member of a group it has nothing to do with.
//
// The pass takes plain Requests and no language: internal/worklang depends on
// this package, so the kernel can depend on nothing. A unit becomes a Request
// on the reader's side, where the form is already in hand.

// Admitted is one request's verdict: whether it went, and when it did not, the
// refusal that says why.
type Admitted struct {
	Request Request
	// Go reports that the vector was granted and the unit may run NOW.
	Go bool
	// Refusal is the reason it may not, nil when Go. It names the dimension or
	// the path that is short, and who holds it.
	Refusal *Refusal
}

// ID is the unit the verdict is about.
func (a Admitted) ID() string { return a.Request.ID }

// On names the one thing that held the unit back: the dimension, or the path
// under a `writes:` prefix so the two can never be read as each other.
func (a Admitted) On() string {
	switch {
	case a.Refusal == nil:
		return ""
	case a.Refusal.Path != "":
		return "writes:" + a.Refusal.Path
	default:
		return a.Refusal.Dim
	}
}

// By names the live grant holding it, when there is one.
func (a Admitted) By() string {
	if a.Refusal == nil {
		return ""
	}
	return a.Refusal.Holder
}

// Admit runs one pass over ready requests, in the order given. It is
// deterministic: the same requests over the same authority always yield the
// same verdicts, so pinning the answer for a real file is a real pin.
//
// The requests that go are left HOLDING their grants, because "these may run
// together" is only true of a set that was admitted together -- one live unit
// per lane (A6), no two intersecting writes (A7). A caller that wanted only the
// reading releases them, or closes the Admission.
func (a *Admission) Admit(ready []Request) []Admitted {
	out := make([]Admitted, 0, len(ready))
	for _, req := range ready {
		if _, err := a.Grant(req); err == nil {
			out = append(out, Admitted{Request: req, Go: true})
			continue
		} else if ref, ok := err.(*Refusal); ok {
			out = append(out, Admitted{Request: req, Refusal: ref})
		} else {
			out = append(out, Admitted{Request: req, Refusal: &Refusal{ID: req.ID, Reason: err.Error()}})
		}
	}
	return out
}
