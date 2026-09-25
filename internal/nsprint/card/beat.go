package card

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// BeatRequest is one card beat from the wrapper. The first beat is the
// start acknowledgement: launched becomes running.
type BeatRequest struct {
	Sprint string
	Label  string
	Token  string
}

// Beat records that the attempt is alive. A mismatched token exits 3 and
// does not move the card; the wrapper then stops its child.
func Beat(ctx context.Context, st *store.Store, req BeatRequest) (Result, error) {
	const verb = "card beat"
	if st == nil || st.Client() == nil || !validSprintLabel(req.Sprint, req.Label) || req.Token == "" {
		return usage(verb, req.Label), nil
	}
	reply, err := fcall(ctx, st, "ns_card_beat", cardKeys(req.Sprint, req.Label),
		req.Sprint, req.Label, req.Token)
	if err != nil {
		if res, down := redisDown(verb, req.Label, err); down {
			return res, nil
		}
		return Result{}, err
	}
	return resultFrom(verb, req.Label, reply), nil
}
