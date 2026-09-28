package decide

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// JevVerdict is one Jev line as the ledger stream carries it: the pull request,
// the exact head, the verdict word and the 1-10 score, or no score at all.
type JevVerdict struct {
	Repo    string // owner/name
	PR      int
	Head    string // the head sha the line was written at
	Verdict string // PASS, BOUNCE or UNSURE; the stream has no field for it, it is here for the caller's record
	Score   int    // 1-10 when Scored
	Scored  bool   // false when the line printed score=-
	Model   string // the model the score was asked of; "jev" when empty
}

// headRE is a git sha, abbreviated or whole.
var headRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Label is the id the fold joins and groups on: repo#pr, one row per pull
// request. The repo alone made every Jev verdict across a repository one
// pseudo-card in by_label and totals (Emma's read of PR #2788).
func (v JevVerdict) Label() string { return v.Repo + "#" + strconv.Itoa(v.PR) }

// Route is score/N, or score/- when no score was given: an absent score is not
// a zero (no evidence is not negative evidence).
func (v JevVerdict) Route() string {
	if !v.Scored {
		return "score/-"
	}
	return fmt.Sprintf("score/%d", v.Score)
}

// Check refuses what is not a Jev verdict before it can reach the stream.
func (v JevVerdict) Check() error {
	owner, name, ok := strings.Cut(v.Repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") || strings.ContainsAny(v.Repo, " #") {
		return fmt.Errorf("jev ledger: repo %q is not owner/name", v.Repo)
	}
	if v.PR <= 0 {
		return fmt.Errorf("jev ledger: %d is not a pull request number", v.PR)
	}
	if !headRE.MatchString(v.Head) {
		return fmt.Errorf("jev ledger: head %q is not a sha; a verdict is at an exact head", v.Head)
	}
	if v.Scored && (v.Score < 1 || v.Score > 10) {
		return fmt.Errorf("jev ledger: score %d is outside the friends' 1-10", v.Score)
	}
	return nil
}

// WriteJevLedger writes one Jev verdict to the cards:done stream: one
// kind=jev entry with the pull request, the head and the score as route.
// The production caller is `nova-decide review --ledger redis`, once per JEV
// line it prints.
func WriteJevLedger(ctx context.Context, emitter events.Emitter, v JevVerdict) (string, error) {
	if err := v.Check(); err != nil {
		return "", err
	}
	model := strings.TrimSpace(v.Model)
	if model == "" {
		model = "jev"
	}
	return emitter.Emit(ctx, events.Event{
		Label: v.Label(),
		PR:    strconv.Itoa(v.PR),
		Head:  v.Head,
		Kind:  events.Jev,
		Model: model,
		Route: v.Route(),
	})
}
