package reconcile

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// unheld is the streams a pit stop does not hold, in their order: the holds
// the pass read (pitstop.FromContext), else a fresh read. The stream-aware
// duties (the friend deal, waiting-resolve, the land worker) walk only these,
// so a scope=streams stop holds exactly its streams.
func unheld(ctx context.Context, c redis.Cmdable, streams []string) ([]string, error) {
	hs, err := pitstop.Current(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("pitstop: %w", err)
	}
	if len(hs) == 0 {
		return streams, nil
	}
	out := make([]string, 0, len(streams))
	for _, s := range streams {
		if _, held := hs.Stream(s); !held {
			out = append(out, s)
		}
	}
	return out, nil
}
