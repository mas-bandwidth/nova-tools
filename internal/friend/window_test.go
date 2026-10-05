package friend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWindowReachNamesThePersonGrantedPermission(t *testing.T) {
	t.Parallel()
	err := WindowReach(context.Background(), nil, "friend.bundle", "window", "composer", "message")
	require.Error(t, err)
}
