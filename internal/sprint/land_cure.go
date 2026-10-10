package sprint

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
)

// The base cure (docs/SPEC-SPRINT.md section 8, the base-gate rule): a base red at its tip
// refuses every landing on it, and the head that would turn it green is one of them. On
// 2026-10-05 at 7:30 PM the base was red on one test, the fix card sat merging, and the
// lander refused it because the base was red; the coordinator fast-forwarded the base by
// hand. Before the lander refuses on a red base it runs the same gate on each queued head
// merged onto that base alone: the first whose tree passes is a cure, not a casualty, and
// lands first as the base fix, the stream going on after it. The stream stops on the base
// only when no queued head cures it.

// CureHead is one queued head the cure search tries: the card and its head.
type CureHead struct{ ID, Head string }

// CureTry is a head tried and refused as a cure: why (it did not merge, or its tree fails
// the gate).
type CureTry struct {
	CureHead
	Why string
}

// BaseCureReq is one search for a base fix in the land round's clone.
type BaseCureReq struct {
	RepoDir string     // the land round's clone, HEAD at the red base
	Base    string     // the red base's tip commit: HEAD is reset to it before each try
	Heads   []CureHead // the queued heads, in queue order
	Env     []string   // git's whole environment, the land round's (nil inherits)
	// Merge merges one head onto HEAD: card is why the head itself does not merge (it is not
	// a cure; the search goes on), env a failure that is not the head's (the search stops).
	// nil is a plain `git merge --no-ff` of the head.
	Merge func(ctx context.Context, h CureHead) (card, env string)
	// Gate is the base's tree gate, run on each merged tree: "" green, else the finding.
	// Required: a search with no gate refuses.
	Gate func(ctx context.Context, dir string) string
	// Tried says a head was tried on this base before and was no cure: it is not gated again.
	Tried func(h CureHead) bool
}

// BaseCure is what one search found: the head that cures the base (ID "" when none does)
// and the clone's HEAD, its merge onto the base, which passed the gate; and each head tried
// that was no cure, with why. With no cure HEAD is the base again.
type BaseCure struct {
	CureHead
	Tip   string
	Tried []CureTry
}

// Found says a queued head cures the base.
func (c BaseCure) Found() bool { return c.ID != "" }

// Note is the cure's landing note, on its merge card's timeline: the head landed first as
// the base fix, the base it fixed and the finding it cured.
func (c BaseCure) Note(base, why string) string {
	if len(base) > 12 {
		base = base[:12]
	}
	return cutText(fmt.Sprintf("landed first as the base fix: the base %s failed its tree gate and the head %s merged onto it passes that gate (%s)", base, c.Head, why), MaxCardTextBytes)
}

// FindBaseCure tries each queued head, in order, merged onto the red base alone, through
// the base's own tree gate; the first green is the cure, the clone left at its merge. A
// head tried before on this base (Tried) is skipped. The error is a search that could not
// be decided: no gate, a git failure, a merge's env.
func FindBaseCure(ctx context.Context, req BaseCureReq) (BaseCure, error) {
	var out BaseCure
	if req.Gate == nil {
		return out, errors.New("base cure: no tree gate; a head cures the base only through the base's own gate")
	}
	git := func(args ...string) (string, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{C: req.RepoDir, Env: req.Env, OwnRepo: true}, args...)
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(res.Stderr)+"\n"+string(res.Stdout)))
		}
		return strings.TrimSpace(string(res.Stdout)), nil
	}
	merge := req.Merge
	if merge == nil {
		merge = func(_ context.Context, h CureHead) (string, string) {
			if _, err := git("merge", "--no-ff", "--no-edit", "-m", "land "+h.ID+" (the base fix)", h.Head); err != nil {
				if _, abort := git("merge", "--abort"); abort != nil {
					return "", "the merge of " + h.ID + " could not be undone: " + abort.Error()
				}
				return "the head " + h.Head + " of " + h.ID + " does not merge onto the base: " + err.Error(), ""
			}
			return "", ""
		}
	}
	reset := func() error {
		if _, err := git("reset", "-q", "--hard", req.Base); err != nil {
			return fmt.Errorf("base cure: %w", err)
		}
		return nil
	}
	for _, h := range req.Heads {
		if req.Tried != nil && req.Tried(h) {
			continue
		}
		if err := reset(); err != nil {
			return out, err
		}
		card, env := merge(ctx, h)
		if env != "" {
			return out, errors.Join(errors.New("base cure: "+env), reset())
		}
		if card != "" {
			out.Tried = append(out.Tried, CureTry{h, card})
			continue
		}
		if why := req.Gate(ctx, req.RepoDir); why != "" {
			out.Tried = append(out.Tried, CureTry{h, "the head " + h.Head + " of " + h.ID + " merged onto the base fails its gate too: " + why})
			continue
		}
		tip, err := git("rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return out, errors.Join(fmt.Errorf("base cure: %w", err), reset())
		}
		out.CureHead, out.Tip = h, tip
		return out, nil
	}
	return out, reset()
}
