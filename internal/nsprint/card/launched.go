package card

import (
	"context"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// LaunchRequest is card launched from the wrapper at child start.
type LaunchRequest struct {
	Sprint string
	Label  string
	Token  string
	Branch string
	JobDir string
}

// Launched moves dealt to launched. A token that is not this attempt's token
// exits 3 and leaves the card dealt.
func Launched(ctx context.Context, st *store.Store, req LaunchRequest) (Result, error) {
	const verb = "card launched"
	if st == nil || st.Client() == nil || !validSprintLabel(req.Sprint, req.Label) || req.Token == "" || strings.TrimSpace(req.Branch) == "" || strings.TrimSpace(req.JobDir) == "" {
		return usage(verb, req.Label), nil
	}
	reply, err := fcall(ctx, st, "ns_card_launched", cardKeys(req.Sprint, req.Label),
		req.Sprint, req.Label, req.Token, req.Branch, req.JobDir)
	if err != nil {
		if res, down := redisDown(verb, req.Label, err); down {
			return res, nil
		}
		return Result{}, err
	}
	return resultFrom(verb, req.Label, reply), nil
}
