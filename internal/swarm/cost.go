package swarm

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// COST READS <pool>/usage/ AND NOTHING ELSE, which is why it answers after a reclaim.
//
// A fleet can burn a session: one fleet ran about 9M tokens and three parallel workflows hit
// the limit in 20 minutes (Glenn, 2026-09-10). A pool whose cost is invisible is a pool that
// is discovered to be expensive by being cut off.
//
// A ZERO IS A MEASUREMENT AND A DASH IS AN ABSENCE. COST OK carries `dashes=` so a total
// with an absence in it is never read as complete.
func Cost(p *Pool, since string, max int, stdout, stderr io.Writer) int {
	rows, err := p.ReadUsage()
	if err != nil {
		fmt.Fprintf(stderr, "COST REFUSED: the usage directory could not be read: %s\n", oneline.Escape(redactedReason(err)))
		return 2
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["job"] < rows[j]["job"] })
	list := bounded.Capped(stdout, max, "COST", "task", "nova-swarm cost --pool "+p.Dir+" --max 0")
	totals := map[string]int{}
	dashes := map[string]int{}
	usd := 0.0
	first, last := "", ""
	tasks := 0
	for _, row := range rows {
		if since != "" && row["job"] < since {
			continue
		}
		tasks++
		if first == "" || row["started"] < first {
			first = row["started"]
		}
		if row["ended"] > last {
			last = row["ended"]
		}
		for _, c := range TokenColumns {
			if n, ok := row.Int(c); ok {
				totals[c] += n
			} else {
				dashes[c]++
			}
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(row["usd"]), 64); err == nil {
			usd += f
		}
		list.Line(fmt.Sprintf("COST TASK id=%s attempt=%s end=%s in=%s out=%s cache_write=%s cache_read=%s reasoning=%s usd=%s model=%s repo=%s",
			oneline.Field(row["job"]), oneline.Field(dashOr(row["attempt"])), oneline.Field(dashOr(row["end"])),
			oneline.Field(dashOr(row["tokens_in"])), oneline.Field(dashOr(row["tokens_out"])),
			oneline.Field(dashOr(row["cache_write"])), oneline.Field(dashOr(row["cache_read"])),
			oneline.Field(dashOr(row["reasoning"])), oneline.Field(dashOr(row["usd"])),
			oneline.Field(dashOr(row["model"])), oneline.Field(dashOr(row["repo"]))))
	}
	list.More()
	fmt.Fprintf(stdout, "COST OK tasks=%d in=%d out=%d cache_write=%d cache_read=%d reasoning=%d dashes=%d,%d,%d,%d,%d usd=%.4f window=%s..%s\n",
		tasks, totals["tokens_in"], totals["tokens_out"], totals["cache_write"], totals["cache_read"], totals["reasoning"],
		dashes["tokens_in"], dashes["tokens_out"], dashes["cache_write"], dashes["cache_read"], dashes["reasoning"],
		usd, oneline.Field(dashOr(first)), oneline.Field(dashOr(last)))
	return 0
}
