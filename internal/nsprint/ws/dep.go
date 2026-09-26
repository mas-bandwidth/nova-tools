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
