package card

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// Release moves a waiting card into the pool only when every card it depends
// on is landed. A parent that is still queued, or landed with no merge commit,
// keeps the dependent waiting. Cards whose parents are not landed stay in the
// waiting set; the set is not copied into the pool.
func Release(ctx context.Context, client *redis.Client, sprint string) VerbResult {
	if !sprintRE.MatchString(sprint) {
		return refused("sprint name must match [a-z0-9-]{1,40}")
	}
	if err := ensure(ctx, client); err != nil {
		return refused(err.Error())
	}
	reply, err := client.FCall(ctx, "ns_card_release", []string{
		keyWaiting(sprint), keyPool(sprint), keyLog(sprint),
	}, sprint).Text()
	if err != nil {
		return refused(err.Error())
	}
	var moved, waiting int
	if _, err := fmt.Sscanf(reply, "moved=%d waiting=%d", &moved, &waiting); err != nil {
		return refused(fmt.Sprintf("card release reply %q", reply))
	}
	return VerbResult{Code: exitOK, Stdout: fmt.Sprintf("CARD RELEASE sprint=%s moved=%d waiting=%d\n",
		oneline.Field(sprint), moved, waiting)}
}

// Land records that the card's work is merged. mergeSHA is the merge commit
// on the card's base. Dependents stay in waiting until Release.
func Land(ctx context.Context, client *redis.Client, sprint, label, mergeSHA string) VerbResult {
	if !sprintRE.MatchString(sprint) {
		return refused("sprint name must match [a-z0-9-]{1,40}")
	}
	if !idRE.MatchString(label) {
		return refused("label is not a card id")
	}
	if !shaRE.MatchString(mergeSHA) {
		return refused("merge sha is not 40 lowercase hex")
	}
	if err := ensure(ctx, client); err != nil {
		return refused(err.Error())
	}
	reply, err := client.FCall(ctx, "ns_card_land", []string{
		keyCard(sprint, label),
		keyPool(sprint),
		keyWaiting(sprint),
		keyLog(sprint),
		keyIdx(sprint, "queued"),
		keyIdx(sprint, "landed"),
	}, label, mergeSHA).Text()
	if err != nil {
		return refused(err.Error())
	}
	switch reply {
	case "OK":
		return VerbResult{Code: exitOK, Stdout: fmt.Sprintf("CARD LAND sprint=%s label=%s merge=%s\n",
			oneline.Field(sprint), oneline.Field(label), oneline.Field(mergeSHA))}
	case "ABSENT":
		return refused("no such card")
	case "REFUSED":
		return refused("merge sha is not 40 lowercase hex")
	case "CONFLICT":
		return VerbResult{Code: exitConflict, Stderr: oneline.Escape("label conflict") + "\n"}
	default:
		return refused(fmt.Sprintf("card land reply %q", reply))
	}
}
