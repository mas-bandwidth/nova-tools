//go:build functional

package card

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// Land is the test-side landing of a card (ns_card_land): the fixture the
// release and never-disappear tests seed a landed card with. No verb reaches
// it any more, so it lives with the tests.
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
