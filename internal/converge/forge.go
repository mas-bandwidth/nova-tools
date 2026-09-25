package converge

// forge.go is the seam between this verb and a forge. The verb knows nothing
// about gh: it asks for the open pull requests and the ones closed since an
// instant, and everything that comes back is DATA -- a number, a title, a body,
// two timestamps -- never an instruction. The tests hand the streams a fake, so
// no test here touches the network.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// PR is one pull request, in the four facts convergence reads.
type PR struct {
	Number    int
	Title     string
	Body      string
	Merged    bool
	CreatedAt time.Time
	ClosedAt  time.Time
}

// Forge is what this verb needs from a forge, and all of it. Neither method
// writes: convergence reads the queue, it never touches it.
type Forge interface {
	// OpenPRs is every pull request open right now.
	OpenPRs(ctx context.Context) ([]PR, error)
	// ClosedSince is every pull request closed or merged at or after `since`.
	ClosedSince(ctx context.Context, since time.Time) ([]PR, error)
}

// ForgeLimit is the most pull requests one read will take. A read that hits it
// is REFUSED rather than truncated: a convergence number computed from the
// first thousand of an unknown number is a measurement with a lie in it.
const ForgeLimit = 1000

// GH is the real forge: `gh pr list`, bounded, reading only. GH_CONFIG_DIR and
// the rest of the environment come from the caller's, because which identity gh
// speaks as is the caller's business and never this verb's.
type GH struct {
	Repo    string
	Timeout time.Duration
	// Bin is the gh executable, "gh" unless a caller names another. A test
	// names a fake; nothing here searches a path of its own.
	Bin string
}

// ghPR is the shape `gh pr list --json` answers in.
type ghPR struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
	ClosedAt  string `json:"closedAt"`
	MergedAt  string `json:"mergedAt"`
}

const ghFields = "number,title,body,state,createdAt,closedAt,mergedAt"

// OpenPRs lists what is open now.
func (g GH) OpenPRs(ctx context.Context) ([]PR, error) {
	return g.list(ctx, "open", "")
}

// ClosedSince lists what closed or merged inside the window. The search is by
// DAY, because that is the resolution the forge's search takes; the window's
// own edges are applied afterwards by the streams, on the timestamps.
func (g GH) ClosedSince(ctx context.Context, since time.Time) ([]PR, error) {
	return g.list(ctx, "all", "closed:>="+since.UTC().Format("2006-01-02"))
}

func (g GH) list(ctx context.Context, state, search string) ([]PR, error) {
	bin := g.Bin
	if strings.TrimSpace(bin) == "" {
		bin = "gh"
	}
	args := []string{"pr", "list",
		"--repo", g.Repo,
		"--state", state,
		"--limit", fmt.Sprintf("%d", ForgeLimit),
		"--json", ghFields,
	}
	if search != "" {
		args = append(args, "--search", search)
	}
	ctx, cancel := context.WithTimeout(ctx, g.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("gh pr list --state %s ran past --timeout %s and was killed", oneline.Field(state), g.Timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("gh pr list --state %s: %s: %s", oneline.Field(state), oneline.Err(err),
			oneline.Cap(oneline.Escape(strings.TrimSpace(errb.String())), oneline.TailBytes))
	}
	var raw []ghPR
	if err := json.Unmarshal([]byte(out.String()), &raw); err != nil {
		return nil, fmt.Errorf("gh pr list --state %s answered something that is not the JSON it was asked for: %s",
			oneline.Field(state), oneline.Err(err))
	}
	if len(raw) >= ForgeLimit {
		return nil, fmt.Errorf("gh pr list --state %s answered the whole %d-row limit, so the reading would be of an unknown fraction of the queue; narrow --since",
			oneline.Field(state), ForgeLimit)
	}
	out2 := make([]PR, 0, len(raw))
	for _, r := range raw {
		pr := PR{Number: r.Number, Title: r.Title, Body: r.Body, Merged: strings.EqualFold(r.State, "merged")}
		pr.CreatedAt = parseForgeTime(r.CreatedAt)
		// A merged pull request's close is its merge; `closedAt` and `mergedAt`
		// agree on the forge, and mergedAt is the one that means landed.
		if t := parseForgeTime(r.MergedAt); !t.IsZero() {
			pr.ClosedAt = t
			pr.Merged = true
		} else {
			pr.ClosedAt = parseForgeTime(r.ClosedAt)
		}
		out2 = append(out2, pr)
	}
	return out2, nil
}

// parseForgeTime reads a forge timestamp, answering the zero time for the empty
// string an open pull request carries.
func parseForgeTime(s string) time.Time {
	t := strings.TrimSpace(s)
	if t == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339, t)
	if err != nil {
		return time.Time{}
	}
	return at.UTC()
}

// Merged keeps the pull requests that landed, which is what a batch is.
func Merged(prs []PR) []PR {
	out := make([]PR, 0, len(prs))
	for _, pr := range prs {
		if pr.Merged {
			out = append(out, pr)
		}
	}
	return out
}

// StillOpen keeps the pull requests that are open, so one read of `--state all`
// can answer both halves when a caller has one.
func StillOpen(prs []PR) []PR {
	out := make([]PR, 0, len(prs))
	for _, pr := range prs {
		if pr.ClosedAt.IsZero() {
			out = append(out, pr)
		}
	}
	return out
}
