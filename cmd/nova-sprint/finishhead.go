package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// finishHeadWords is finish --head's help: what an ok finish that names no head records
// (nova-tools#5154: it said the card's id, which land refuses).
const finishHeadWords = "the commit the work finished at, the head land merges (default for an ok finish: origin's tip of the work's branch, --branch or else the one its packet names, in the repository its card's REPO: line names, so the finish records what land merges; refused when origin holds no such branch or its tip cannot be read; a card whose brief names no repository, a run with no git, records the card's id, which a twin's merge records and land refuses)"

// finishHeads is the head an ok finish that names no --head records for each card it
// names that is working on one of the members: defaultHead of its packet. A card left
// out records its id (sprint.FinishReq.Heads); a card not working is the step's to refuse.
func (a *app) finishHeads(ctx context.Context, st *store.Store, members, ids []string, branch string) (map[string]string, error) {
	var cards []*sprint.Card
	for _, m := range members {
		cs, err := st.ReadCells(ctx, sprint.Fleet, m, sprint.Working)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if slices.Contains(ids, c.ID) {
				cards = append(cards, c)
			}
		}
	}
	if len(cards) == 0 {
		return nil, nil
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return nil, err
	}
	heads := map[string]string{}
	for _, p := range packets {
		h, err := defaultHead(ctx, p, branch, a.tip)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Card, err)
		}
		if h != "" {
			heads[p.Card] = h
		}
	}
	return heads, nil
}

// defaultHead is the head an ok finish of the packet's card records when it names none:
// origin's tip of the work's branch (branch, else the one the packet names) in the
// repository the card's REPO: line names, read once through tip, as a friend's LAND is
// (friendFinish). "" for a card whose brief names no repository: a run with no git. A tip
// that cannot be read, or a branch origin does not hold, is an error naming the remedy.
func defaultHead(ctx context.Context, p sprint.Packet, branch string, tip tipFn) (string, error) {
	repo := swarm.ReadCardBase([]byte(p.Brief)).Repo
	if repo == "" {
		return "", nil
	}
	if branch == "" {
		branch = p.Branch
	}
	at, err := tip(ctx, repo, branch)
	switch {
	case err != nil:
		return "", fmt.Errorf("origin's tip of %s in %s cannot be read (%v); push the work, or name the commit: --head <commit>", branch, repo, err)
	case at == "":
		return "", fmt.Errorf("origin holds no branch %s in %s; push the work there, or name the commit: --head <commit>", branch, repo)
	}
	return at, nil
}
