package refmodel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// A friend's fleet row (sprint.FriendRow) is no member of the model: the model's members
// are the machines, so the differential state never counts her row as a machine down.
func TestAbstractTakesNoFriendRowForAMember(t *testing.T) {
	t.Parallel()
	w := newWorld("reader-a")
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "up", Member: "m1"}))
	w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), sprint.FriendRow("amy")))
	s := refmodel.Abstract(refmodel.Observed{Snap: w.s, Machine: refmodel.Running})
	assert.Equal(t, map[string]string{"m1": refmodel.Up}, s.Members)
}
