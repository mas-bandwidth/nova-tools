// The fold's friend-read line (nova-tools #3105, #2756 spec 4.1.1, control
// 55): the dollars friends spent reading the sprint's PRs, per landed card,
// with the count of reads whose cost was never measured beside it. The
// package doc and the verb are the sprint fold's (#2618, PR #3006).

package fold

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// ReadCost is the sprint's friend-read cost. Unmetered reads are an absence,
// never $0: USDMicro sums only the priced reads, so read $ per landed is a
// lower bound whenever Unmetered is not 0, and the line says so by printing
// the count beside it.
type ReadCost struct {
	Reads     int
	Priced    int
	Unmetered int
	// Malformed counts reads whose evidence names a cost that does not parse;
	// they are unmetered too and are never summed.
	Malformed int
	USDMicro  int64
	Landed    int
}

// ReadTask is the part of a closed task hash the read line needs.
type ReadTask struct {
	ID       string
	Kind     string
	Evidence string
}

// isRead matches the task library's is_review: a read or a review.
func isRead(kind string) bool {
	return kind == string(task.KindRead) || kind == string(task.KindReview)
}

// SumReads prices the closed read tasks. A read whose evidence carries no
// cost field, `cost: unmetered <reason>`, `cost_usd=-` or a cost that does
// not parse is counted unmetered and adds nothing to the dollars.
func SumReads(tasks []ReadTask, landed int) ReadCost {
	rc := ReadCost{Landed: landed}
	for _, t := range tasks {
		if !isRead(t.Kind) {
			continue
		}
		rc.Reads++
		c, found, err := task.ParseCost(t.Evidence)
		switch {
		case err != nil:
			rc.Malformed++
			rc.Unmetered++
		case !found || !c.Metered:
			rc.Unmetered++
		default:
			rc.Priced++
			rc.USDMicro += c.USDMicro
		}
	}
	return rc
}

// USD is the priced reads' dollars, or the dash when no read was priced: no
// measured read is not a $0 read.
func (rc ReadCost) USD() string {
	if rc.Priced == 0 {
		return tokens.Dash
	}
	return tokens.Usd(rc.USDMicro)
}

// PerLanded is read dollars per landed card, rounded to the micro-dollar, or
// the dash when nothing landed or no read was priced.
func (rc ReadCost) PerLanded() string {
	if rc.Landed == 0 || rc.Priced == 0 {
		return tokens.Dash
	}
	return tokens.Usd((rc.USDMicro + int64(rc.Landed)/2) / int64(rc.Landed))
}

// PrintReads prints the fold's friend-read line.
func PrintReads(out io.Writer, sprint string, rc ReadCost) {
	fmt.Fprintf(out, "FOLD READS sprint=%s reads=%d priced=%d unmetered=%d malformed=%d read_usd=%s landed=%d read_usd_per_landed=%s\n",
		oneline.Field(sprint), rc.Reads, rc.Priced, rc.Unmetered, rc.Malformed, rc.USD(), rc.Landed, rc.PerLanded())
}

// ReadTasks reads the sprint's closed tasks from the store in two round
// trips: the closed index, then one pipeline of HMGET kind, evidence.
func ReadTasks(ctx context.Context, client *redis.Client, sprint string) ([]ReadTask, error) {
	key := "s:" + sprint
	ids, err := client.SMembers(ctx, key+":idx:task:closed").Result()
	if err != nil {
		return nil, fmt.Errorf("read %s:idx:task:closed: %w", key, err)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.SliceCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HMGet(ctx, key+":task:"+id, "kind", "evidence")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("read the closed tasks of %s: %w", key, err)
	}
	out := make([]ReadTask, 0, len(ids))
	for i, cmd := range cmds {
		v := cmd.Val()
		t := ReadTask{ID: ids[i]}
		if s, ok := v[0].(string); ok {
			t.Kind = s
		}
		if s, ok := v[1].(string); ok {
			t.Evidence = s
		}
		out = append(out, t)
	}
	return out, nil
}
