package ws

// DepMet is the one dependency rule in Go: whether a DEPENDS-ON edge on the
// task record id, whose fields are state, where and where_ok, is met. A
// stream's sentinel is met only when landed (its done is a rename's, never a
// landing); any other task when landed, or done with where_ok not fail; a
// record that predates the where field when its state is landed, closed or
// done. A missing record (every field empty) is not met.
//
// It is NS.dep.met_of in fn/lua/01_dep.lua, which the task queue's
// DEP.holds and the release on a move call; fn's TestDepRuleOneTable runs
// both against one table. Every Go reader of a task dependency calls this:
// the waiting resolver (reconcile), ws show, card push and release, and
// ready --why.
func DepMet(id, state, where, whereOK string) bool {
	if IsSentinel(id) {
		return state == Landed || where == Landed
	}
	switch where {
	case Landed:
		return true
	case Done:
		return whereOK != "fail"
	case "":
		return state == Landed || state == Closed || state == Done
	}
	return false
}

// The dependency classes: the one word every reader of a DEPENDS-ON edge on
// a task record prints for it (ready --why, the dealers, the waiting
// resolver, ws show, task take's needs, task push's waits). The table is
// DepClass here and NS.dep.class_of in fn/lua/01_dep.lua; fn's
// TestDepClassOneTable runs both against one table.
const (
	DepClassMet     = "met"     // the one rule (DepMet) says met
	DepClassWaiting = "waiting" // a live record, not met yet
	DepClassParked  = "parked"  // a record in parked: met only after it is taken up again
	DepClassDead    = "dead"    // a record in done that is not met (done/fail, a sentinel done by hand)
	DepClassUnknown = "unknown" // no record has the id
	DepClassCycle   = "cycle"   // the edge leads back to the task that names it (refused at the write doors)
)

// DepWhere is the where a record's fields put it in: where, else (a record
// that predates the where field) the where its state names.
func DepWhere(state, where string) string {
	if where != "" {
		return where
	}
	switch state {
	case Closed, "cancelled", Done:
		return Done
	case "open":
		return Ready
	case "claimed":
		return Working
	}
	return state
}

// DepClass is the class of a DEPENDS-ON edge on the task record id, and the
// detail a reader prints after it: the record's where (done with /where_ok),
// "" when the class already says it.
func DepClass(id, state, where, whereOK string) (class, detail string) {
	if state == "" && where == "" {
		return DepClassUnknown, ""
	}
	w := DepWhere(state, where)
	if w == Done {
		switch {
		case whereOK != "":
			w += "/" + whereOK
		case state == "cancelled":
			w += "/fail"
		}
	}
	switch {
	case DepMet(id, state, where, whereOK):
		class = DepClassMet
	case w == Done || len(w) > len(Done) && w[:len(Done)+1] == Done+"/":
		class = DepClassDead
	case w == Parked:
		class = DepClassParked
	default:
		class = DepClassWaiting
	}
	if w == class {
		w = ""
	}
	return class, w
}

// DepText is a class and its detail as every reader prints them: "dead
// done/fail", "waiting working", "unknown".
func DepText(class, detail string) string {
	if detail == "" {
		return class
	}
	return class + " " + detail
}
