package fleetbuild

// call is one child process a fake runner saw: shared by the release and
// self-update fixtures (the release tests run on a real Redis since
// 2026-09-27; this stays in every tier).
type call struct {
	dir  string
	env  []string
	argv []string
}

// the version and commit every fixture builds
const (
	testV = "v0.16.0-dev.c8178673"
	testC = "c8178673f5e19ffbfe841e11826e611b74b6900d"
)
