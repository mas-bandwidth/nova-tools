package card

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

func keyCard(sprint, label string) string { return "s:" + sprint + ":card:" + label }
func keyPool(sprint string) string        { return "s:" + sprint + ":pool" }
func keyWaiting(sprint string) string     { return "s:" + sprint + ":waiting" }
func keyLog(sprint string) string         { return "s:" + sprint + ":log" }
func keyIdx(sprint, state string) string  { return "s:" + sprint + ":idx:card:" + state }

// Push lints the card, then writes it into the pool or the waiting set.
// Exit 2: a missing required line, a private repository, or Redis did not
// accept the write. A dependency that is not landed goes to waiting, not the pool.
func Push(ctx context.Context, client *redis.Client, sprint string, body []byte) VerbResult {
	if !sprintRE.MatchString(sprint) {
		return refused("sprint name must match [a-z0-9-]{1,40}")
	}
	doc, err := lint(ctx, body)
	if err != nil {
		return refused(err.Error())
	}
	if err := ensure(ctx, client); err != nil {
		return refused(err.Error())
	}
	keys := []string{
		keyCard(sprint, doc.Label),
		keyPool(sprint),
		keyWaiting(sprint),
		keyLog(sprint),
		keyIdx(sprint, "queued"),
	}
	for _, dep := range doc.Deps {
		keys = append(keys, keyCard(sprint, dep))
	}
	reply, err := client.FCall(ctx, "ns_card_push", keys,
		doc.Label, doc.Payload, "0", doc.Base, doc.BaseSHA, doc.Paths, doc.Repo, doc.Kind, doc.DependsOn, doc.Type,
	).Text()
	if err != nil {
		return refused(err.Error())
	}
	switch reply {
	case "EXISTS":
		return VerbResult{Code: exitOK, Stdout: pushLine(sprint, doc.Label, "exists")}
	case "CONFLICT":
		return VerbResult{Code: exitConflict, Stderr: oneline.Escape("label conflict") + "\n"}
	}
	place, ok := strings.CutPrefix(reply, "OK place=")
	if !ok || (place != "pool" && place != "waiting") {
		return refused(fmt.Sprintf("card push reply %q", reply))
	}
	return VerbResult{Code: exitOK, Stdout: pushLine(sprint, doc.Label, place)}
}

func pushLine(sprint, label, place string) string {
	return fmt.Sprintf("CARD PUSH sprint=%s label=%s place=%s\n",
		oneline.Field(sprint), oneline.Field(label), oneline.Field(place))
}

func ensure(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return fmt.Errorf("redis client is required")
	}
	if err := fn.Load(ctx, client); err != nil {
		return err
	}
	return nil
}
