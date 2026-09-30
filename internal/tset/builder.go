package tset

import (
	"encoding/json"
)

// BuildStep cuts a fat member entry into aligned entries of the SAME atomic
// step. Call it before assigning Op/Intent. If the step has notes, assign the
// operation pair to the returned step before encoding or dispatching it.
// Bounds use the encoded request and conservative upper bounds on the
// normalized line and expanded argv.
// Dynamic no-op filtering can make the actual plan smaller; only L1/L2 plan
// can give the final exact generated-byte count.
func BuildStep(step Step) (Step, error) {
	cut := step
	cut.Entries = make([]Entry, 0, len(step.Entries))
	for _, entry := range step.Entries {
		if !splittableEntry(entry) || entryFits(entry) {
			cut.Entries = append(cut.Entries, entry)
			continue
		}
		if step.Op != nil || step.Intent != nil {
			return Step{}, NewRefusal("LIMIT", RefusalDetail{})
		}
		parts, err := splitEntry(entry)
		if err != nil {
			return Step{}, err
		}
		cut.Entries = append(cut.Entries, parts...)
	}
	if _, err := encodeStep(cut, true); err != nil {
		return Step{}, err
	}
	if !fitsWholeStep(cut) {
		return Step{}, NewRefusal("LIMIT", RefusalDetail{})
	}
	return cut, nil
}

// PartIdentity fixes every independent part's stable identity before any
// dispatch. A resumed subset must call this with the same part numbers.
type PartIdentity func(part int) (op, intent, result string)

// BuildIndependentSteps is an explicit choice to make several atomic steps.
// It accepts only independent member entries: no topology, guards, notes,
// count predicates, or already named operation may be split this way.
func BuildIndependentSteps(base Step, identity PartIdentity) ([]Step, error) {
	return BuildIndependentStepsFrom(base, 0, identity)
}

// BuildIndependentStepsFrom assigns identities starting at startPart. Retain
// the resulting encoded requests for transport retry; re-chunking a subset
// could change part boundaries even when the numeric offset is preserved.
func BuildIndependentStepsFrom(base Step, startPart int, identity PartIdentity) ([]Step, error) {
	if startPart < 0 || identity == nil || base.Op != nil || base.Intent != nil || len(base.Notes) != 0 {
		return nil, NewRefusal("REQUEST", RefusalDetail{})
	}
	pieces := make([]Entry, 0, len(base.Entries))
	for _, e := range base.Entries {
		if e.Kind != "create" && e.Kind != "move" && e.Kind != "remove" {
			return nil, NewRefusal("REQUEST", RefusalDetail{})
		}
		if !entryFits(e) {
			parts, err := splitEntry(e)
			if err != nil {
				return nil, err
			}
			pieces = append(pieces, parts...)
		} else {
			pieces = append(pieces, e)
		}
	}
	if len(pieces) == 0 {
		return nil, NewRefusal("REQUEST", RefusalDetail{})
	}
	parts := make([]Step, 0)
	seenOps := make(map[string]bool)
	current := Step{Epoch: base.Epoch, Space: base.Space, Entries: []Entry{}}
	flush := func() error {
		if len(current.Entries) == 0 {
			return nil
		}
		part := startPart + len(parts)
		op, intent, result := identity(part)
		if seenOps[op] {
			return NewRefusal("REQUEST", RefusalDetail{})
		}
		current.Op, current.Intent, current.Result = &op, &intent, result
		if _, err := EncodeStep(current); err != nil {
			return err
		}
		seenOps[op] = true
		parts = append(parts, current)
		current = Step{Epoch: base.Epoch, Space: base.Space, Entries: []Entry{}}
		return nil
	}
	for _, piece := range pieces {
		candidate := current
		candidate.Entries = append(append([]Entry(nil), current.Entries...), piece)
		if len(candidate.Entries) <= MaxEntries && fitsWholeStep(candidate) {
			// Check the exact encoded request size. Validation also checks
			// candidate/row/about counts before a Redis call is possible.
			if _, err := EncodeStep(candidate); err == nil {
				current = candidate
				continue
			}
		}
		if err := flush(); err != nil {
			return nil, err
		}
		current.Entries = append(current.Entries, piece)
		if !fitsWholeStep(current) {
			return nil, NewRefusal("LIMIT", RefusalDetail{})
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return parts, nil
}

func splittableEntry(e Entry) bool {
	return e.Kind == "create" || e.Kind == "move" || e.Kind == "remove" || e.Kind == "guard"
}

func entryFits(e Entry) bool {
	if len(e.IDs) > MaxIDsPerEntry {
		return false
	}
	line, argv := entryUpperBounds(e)
	return line <= MaxLineBytes && argv <= MaxPlannedArgvBytes
}

func splitEntry(e Entry) ([]Entry, error) {
	if !splittableEntry(e) || len(e.IDs) == 0 {
		return nil, NewRefusal("LIMIT", RefusalDetail{})
	}
	parts := make([]Entry, 0)
	for start := 0; start < len(e.IDs); {
		lo, hi, best := 1, len(e.IDs)-start, 0
		if hi > MaxIDsPerEntry {
			hi = MaxIDsPerEntry
		}
		for lo <= hi {
			mid := lo + (hi-lo)/2
			candidate := entrySlice(e, start, start+mid)
			if entryFits(candidate) {
				best = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if best == 0 {
			return nil, NewRefusal("LIMIT", RefusalDetail{})
		}
		parts = append(parts, entrySlice(e, start, start+best))
		start += best
	}
	return parts, nil
}

func entrySlice(e Entry, from, to int) Entry {
	out := e
	out.IDs = append([]string(nil), e.IDs[from:to]...)
	if e.Scores != nil && len(e.Scores) >= to {
		out.Scores = append([]string(nil), e.Scores[from:to]...)
	}
	if e.Revs != nil && len(e.Revs) >= to {
		out.Revs = append([]Decimal(nil), e.Revs[from:to]...)
	}
	if e.Each != nil && len(e.Each) >= to {
		out.Each = append([]map[string]string(nil), e.Each[from:to]...)
	}
	if e.About != nil && len(e.About) >= to {
		out.About = append([]string(nil), e.About[from:to]...)
	}
	return out
}

func entryUpperBounds(e Entry) (lineBytes, argvBytes int) {
	if e.Kind == "guard" || e.Kind == "count" || e.Kind == "rcount" {
		return 0, 0 // pre-state checks have no command or log line to prepare
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return MaxLineBytes + 1, MaxPlannedArgvBytes + 1
	}
	// Before/after score and revision pairs can add roughly 2*24+2*20
	// bytes per changed member. 160 includes JSON punctuation and the
	// stream fields, while 1024 covers the event envelope and time.
	lineBytes = len(raw) + 160*len(e.IDs) + 1024
	if e.Kind == "rows" {
		lineBytes += 32 * (len(e.Add) + len(e.Del))
	}
	// A changed member contributes record and cell commands plus their key,
	// revision, score and field-name arguments. The shared field value is
	// charged separately for every record write below. The final planner
	// remains authoritative because actual changed masks and key lengths
	// depend on the initialized space and pre-state.
	argvBytes = len(raw) + 280*len(e.IDs) + 1024
	for name, value := range e.Set {
		argvBytes += len(e.IDs) * (len(name) + len(value) + 16)
	}
	for _, each := range e.Each {
		for name, value := range each {
			argvBytes += len(name) + len(value) + 16
		}
	}
	return lineBytes, argvBytes
}

func fitsWholeStep(s Step) bool {
	if len(s.Entries) > MaxEntries || len(s.Notes) > MaxNotes {
		return false
	}
	argv := 0
	for _, e := range s.Entries {
		line, expanded := entryUpperBounds(e)
		if line > MaxLineBytes || len(e.IDs) > MaxIDsPerEntry {
			return false
		}
		argv += expanded
		if argv > MaxPlannedArgvBytes {
			return false
		}
	}
	for _, n := range s.Notes {
		b, err := json.Marshal(n)
		if err != nil || len(b)+1024 > MaxLineBytes {
			return false
		}
		argv += len(b) + 1024
		if argv > MaxPlannedArgvBytes {
			return false
		}
	}
	return true
}
