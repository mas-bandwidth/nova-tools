package sprint

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// CIxy is the ci cell of `sprint status` (#2756 10.4 item 5, nova-tools
// #3046): OK is the unique required head-and-base pairs whose verdict record
// says OK for that base, Required is the unique pairs the sprint cut ci cards
// for. A rerun is another attempt of the same label, so it never counts as an
// extra done; FLAKY, PENDING, FAIL and MISSING are required and not OK.
type CIxy struct {
	OK       int
	Required int
}

func (c CIxy) String() string { return fmt.Sprintf("ci %d/%d", c.OK, c.Required) }

// WithCI puts the ci cell beside the work line (`x/y z% -> eta`).
func WithCI(work string, c CIxy) string { return work + " | " + c.String() }

// ciCardFields are the head binding ns_ci_cut writes on every ci card.
var ciCardFields = []string{"ci_repo", "ci_head", "base"}

// ReadCIxy derives the ci cell from the card indexes and the verdict records
// only (10.3 item 5), in three batches: a SCAN plus one pipeline for the
// index members, one for the ci cards' head binding, one for the records.
func ReadCIxy(ctx context.Context, st *store.Store, sprint string) (CIxy, error) {
	client := st.Client()
	prefix := "s:" + sprint + ":idx:card:"
	var idx []string
	iter := client.Scan(ctx, 0, prefix+"*", 1000).Iterator()
	for iter.Next(ctx) {
		idx = append(idx, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return CIxy{}, fmt.Errorf("scan %s*: %w", prefix, err)
	}
	labelSet := map[string]bool{}
	if len(idx) > 0 {
		pipe := client.Pipeline()
		cmds := make([]*redis.StringSliceCmd, len(idx))
		for i, k := range idx {
			cmds[i] = pipe.SMembers(ctx, k)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return CIxy{}, fmt.Errorf("read the card indexes of %s: %w", sprint, err)
		}
		for _, cmd := range cmds {
			for _, l := range cmd.Val() {
				if strings.HasPrefix(l, "ci-") {
					labelSet[l] = true
				}
			}
		}
	}
	labels := make([]string, 0, len(labelSet))
	for l := range labelSet {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	reads := make([]store.HashRead, len(labels))
	for i, l := range labels {
		reads[i] = store.HashRead{Key: "s:" + sprint + ":card:" + l, Fields: ciCardFields}
	}
	cards, err := st.PipelineHMGet(ctx, reads)
	if err != nil {
		return CIxy{}, err
	}
	type pair struct{ repo, head, base string }
	seen := map[pair]bool{}
	var pairs []pair
	for _, v := range cards {
		p := pair{str(v[0]), str(v[1]), str(v[2])}
		if p.repo == "" || p.head == "" {
			continue // not a ci card: no head binding
		}
		if !seen[p] {
			seen[p] = true
			pairs = append(pairs, p)
		}
	}
	xy := CIxy{Required: len(pairs)}
	for _, p := range pairs {
		fields, err := civerdict.ReadHead(ctx, client, p.repo, p.head, p.base)
		if err != nil {
			return CIxy{}, err
		}
		if civerdict.Green(civerdict.Of(fields)) && fields["base"] == p.base {
			xy.OK++
		}
	}
	return xy, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
