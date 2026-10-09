package friend

import (
	"context"
	"time"
)

// toolResultWatch observes directory changes, never payloads. Every wake,
// including an overflow, causes Wait to rescan the durable records.
type toolResultWatch interface {
	Wait(context.Context, time.Duration) (bool, error)
	Close() error
}
