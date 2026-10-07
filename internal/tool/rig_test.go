package tool

// Rig is the test harness for tool tests. It provides helpers for parsing
// output renderings and asserting their structure.
type Rig struct{}

// NewRig returns a new test rig.
func NewRig() *Rig { return &Rig{} }
