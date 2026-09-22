package decide

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// WriteJevLedger writes one Jev verdict to the cards:done stream.
// The verdict appears once as kind=jev with PR, head and score.
func WriteJevLedger(ctx context.Context, emitter events.Emitter, repo string, pr int, head string, score int) (string, error) {
	e := events.Event{
		Label: repo,
		PR:    strconv.Itoa(pr),
		Head:  head,
		Kind:  events.Jev,
		Model: "jev",
		Route: fmt.Sprintf("score/%d", score),
	}
	return emitter.Emit(ctx, e)
}
