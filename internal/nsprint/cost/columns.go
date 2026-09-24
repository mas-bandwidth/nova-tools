// Package cost is `nova-sprint cost import` (nova-tools #3159): a provider's usage export,
// one CSV file, reconciled per UTC day and written to Redis as `cost:<provider>:<day>` hashes
// indexed by the `cost:idx` zset. The verb is the only writer of `cost:*`. It reads one file,
// makes zero REST calls and spends zero model tokens.
package cost

import (
	"fmt"
	"strings"
)

// Column is one logical column of an export.
type Column string

// The four logical columns. Project is optional; the other three are required.
const (
	ColDay     Column = "day"
	ColCost    Column = "cost"
	ColModel   Column = "model"
	ColProject Column = "project"
)

// aliases is the one table of header names per column, matched case-insensitively. When a
// header carries two aliases of one column, the first in this order wins. A new provider
// header is one line here, never a new verb.
var aliases = []struct {
	col      Column
	names    []string
	required bool
}{
	{ColDay, []string{"date", "day", "usage_date", "created_at"}, true},
	{ColCost, []string{"cost_usd", "usd", "cost", "total_cost"}, true},
	{ColModel, []string{"model", "model_name", "model_permaslug"}, true},
	{ColProject, []string{"workspace", "workspace_name", "api_key_name", "key_name", "project"}, false},
}

// columnIndex maps each logical column to its index in the header, -1 when absent. A
// missing required column is an error naming it.
func columnIndex(header []string) (map[Column]int, error) {
	pos := map[string]int{}
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if _, seen := pos[h]; !seen {
			pos[h] = i
		}
	}
	out := map[Column]int{}
	for _, a := range aliases {
		out[a.col] = -1
		for _, name := range a.names {
			if i, ok := pos[name]; ok {
				out[a.col] = i
				break
			}
		}
		if out[a.col] < 0 && a.required {
			return nil, fmt.Errorf("no %s column: the header needs one of %s", a.col, strings.Join(a.names, ", "))
		}
	}
	return out, nil
}
