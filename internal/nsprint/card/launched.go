package card

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// LaunchRequest is card launched from the wrapper at child start.
type LaunchRequest struct {
	Sprint string
	Label  string
	Token  string
	Branch string
	JobDir string
	// Deadline is the launcher's absolute batch deadline. Zero preserves the
	// direct verb's historical no-deadline behavior.
	Deadline time.Time
	// WallMax is the attempt's wall cap (#3653), stored as wall_max_s in
	// whole seconds; zero stores nothing.
	WallMax time.Duration
}

// Launched moves dealt to launched. A token that is not this attempt's token
// exits 3 and leaves the card dealt.
func Launched(ctx context.Context, st *store.Store, req LaunchRequest) (Result, error) {
	const verb = "card launched"
	if st == nil || st.Client() == nil || !validSprintLabel(req.Sprint, req.Label) || req.Token == "" || strings.TrimSpace(req.Branch) == "" || strings.TrimSpace(req.JobDir) == "" {
		return usage(verb, req.Label), nil
	}
	deadline := ""
	if !req.Deadline.IsZero() {
		deadline = strconv.FormatInt(req.Deadline.UnixMilli(), 10)
	}
	wallMaxS := ""
	if s := int64(req.WallMax / time.Second); s > 0 {
		wallMaxS = strconv.FormatInt(s, 10)
	}
	reply, err := fcall(ctx, st, "ns_card_launched", cardKeys(req.Sprint, req.Label),
		req.Sprint, req.Label, req.Token, req.Branch, req.JobDir, deadline, wallMaxS)
	if err != nil {
		if res, down := redisDown(verb, req.Label, err); down {
			return res, nil
		}
		return Result{}, err
	}
	return resultFrom(verb, req.Label, reply), nil
}
