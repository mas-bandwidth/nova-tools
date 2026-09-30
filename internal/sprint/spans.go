package sprint

import "time"

// The spans of the due times the rules and the verbs both stamp (1.2), one
// definition each: a stream with open cards may land nothing for IdleSpan
// before the owner is told (2.5: provisional, the owner's to set, 7); a
// merging stream may go MergeIdleSpan without a merge step; an asked read card
// is due begun within UnbegunSpan.
const (
	IdleSpan      = 2 * time.Hour
	MergeIdleSpan = 30 * time.Minute
	UnbegunSpan   = 30 * time.Minute
)
