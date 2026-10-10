package friend

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWakeTargetsAreTheUpFriendsNotNeverWakeNorTheCoordinator(t *testing.T) {
	t.Parallel()
	rows := []WakeRow{{"zed", "up"}, {"ada", "up"}, {"bob", "up"}, {"cy", "held"}, {"dee", "down"}, {"eve", "up"}, {"bob", "up"}}
	assert.Equal(t, []string{"bob", "zed"}, WakeTargets("ada", rows, []string{"eve"}))
	assert.Empty(t, WakeTargets("ada", nil, nil))
}

func TestDeafChangeReportsOnlyWhenTheSetChanges(t *testing.T) {
	t.Parallel()
	var d DeafChange
	assert.Nil(t, d.Report(nil), "no one deaf at the start says nothing")
	assert.Equal(t, []string{"a", "b"}, d.Report([]string{"b", "a"}))
	assert.Nil(t, d.Report([]string{"a", "b"}), "the same set is not said again")
	assert.Equal(t, []string{"a"}, d.Report([]string{"a"}), "a smaller set is a change")
	assert.Nil(t, d.Report(nil), "recovery is remembered, not said")
	assert.Equal(t, []string{"a"}, d.Report([]string{"a"}), "deaf again after recovery is a change")
}

func TestACommandAgentRunsTheBinaryWithTheCommand(t *testing.T) {
	t.Parallel()
	a := Agent{Friend: "wake-ping-ada", Binary: "/b/nova-friend", Command: []string{"ping", "--as", "ada"}}
	assert.Equal(t, []string{"/b/nova-friend", "ping", "--as", "ada"}, a.Args())
	assert.Equal(t, "com.nova.friend-wake-ping-ada", a.Label())
}
