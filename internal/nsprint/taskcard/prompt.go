package taskcard

// prompt.go renders the card a MODEL reads from the record (nova-tools#3956):
// `nova-sprint card render --id <task> --for-model <family>`. The record and
// the family's template override (cfg:card:template:<family>) are read in one
// pipeline; the prompt's shape is card.RenderPrompt's.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/redis/go-redis/v9"
)

// PromptSpec is the prompt's input from a record: the same completed spec
// RenderHeader prints (TEST from DONE-WHEN, the defaults), the goal the task
// line or the title.
func PromptSpec(id string, rec map[string]string) card.PromptSpec {
	s := specOf(rec)
	s.Complete(rec["ref"], rec["origin"])
	return card.PromptSpec{
		ID: id, Goal: firstNonEmpty(s.Task, rec["title"]), Repo: s.Repo, Base: s.Base, BaseSHA: s.BaseSHA,
		Paths: s.Paths, Test: s.Test, DoneWhen: s.DoneWhen, Issue: s.Body,
	}
}

// RenderPrompt reads task:<id> and the family's template in one pipeline and
// renders the prompt. forModel is a family (card.Families, or default) or a
// model launch string (card.FamilyOf picks its family). A missing record, a
// swarm card that lacks a field a run needs, or a template that breaks the
// prompt's shape is a *Refused naming it.
func RenderPrompt(ctx context.Context, c redis.Cmdable, id, forModel string) (card.Prompt, error) {
	family := card.FamilyOf(forModel)
	pipe := c.Pipeline()
	recCmd := pipe.HGetAll(ctx, Key(id))
	tmplCmd := pipe.Get(ctx, card.TemplateKey(family))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return card.Prompt{}, err
	}
	rec := recCmd.Val()
	if len(rec) == 0 {
		return card.Prompt{}, &Refused{Why: "NOTASK " + Key(id)}
	}
	s := specOf(rec)
	if missing := s.Complete(rec["ref"], rec["origin"]); len(missing) > 0 {
		return card.Prompt{}, &Refused{Why: fmt.Sprintf("INCOMPLETE task:%s route=%s lacks %s", id, dash(s.Route), strings.Join(missing, ", "))}
	}
	tmpl := tmplCmd.Val() // "" when unset: the family's built-in template
	p, err := card.RenderPrompt(family, tmpl, PromptSpec(id, rec))
	var pr *card.PromptRefused
	if errors.As(err, &pr) {
		return card.Prompt{}, &Refused{Why: "PROMPT " + pr.Why}
	}
	return p, err
}
