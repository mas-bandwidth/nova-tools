package notice

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each push first lets the route follow the friend's newest session; a failed
// rediscovery keeps the last target and still queues the notice.
func TestDshPushFollowsBeforeQueueing(t *testing.T) {
	t.Parallel()
	var order []string
	follows := []error{nil, errors.New("workspace unreadable")}
	d := &Dsh{
		Route: dshdesktop.Route{EventsDir: t.TempDir()},
		Deliver: func(_ dshdesktop.Route, id, _ string) (string, error) {
			order = append(order, "deliver "+id)
			return "queued", nil
		},
		Follow: func() error {
			order = append(order, "follow")
			err := follows[0]
			follows = follows[1:]
			return err
		},
	}
	for _, id := range []string{"one", "two"} {
		got, err := d.Push(context.Background(), Notice{ID: id})
		require.NoError(t, err)
		assert.Equal(t, "queued", got)
	}
	assert.Equal(t, []string{"follow", "deliver one", "follow", "deliver two"}, order)
}
