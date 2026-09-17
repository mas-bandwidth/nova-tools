package tokens

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FoldPoolColumns are the monthly ledger columns fold-pool writes, in order.
// source is always the fixed word pool.
var FoldPoolColumns = []string{
	"day", "provider", "model", "repo", "tasks",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning",
	"usd", "source",
}

// PoolSource is the fixed source cell fold-pool writes.
const PoolSource = "pool"

// PoolKey is the aggregation key: day of started, provider, model, repo.
type PoolKey struct {
	Day, Provider, Model, Repo string
}

// PoolAgg is one key's folded totals.
type PoolAgg struct {
	Tasks      int
	In         int64
	Out        int64
	Cw         int64
	Cr         int64
	Rsn        int64
	HasIn      bool
	HasOut     bool
	HasCw      bool
	HasCr      bool
	HasRsn     bool
	UsdMicro   int64
	UsdUnknown int
}

// FoldPool reads <pool>/usage/*.tsv (or <pool>/*.tsv when <pool> itself holds
// the files), keeps rows whose started stamp parses and, when since is set,
// is at or after it, and sums by (day of started, provider, model, repo).
func FoldPool(pool, since string) (map[PoolKey]*PoolAgg, int, error) {
	dir := filepath.Join(pool, "usage")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = pool
	}
	var sinceT time.Time
	haveSince := false
	if strings.TrimSpace(since) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(since))
		if err != nil {
			return nil, 0, err
		}
		sinceT = t
		haveSince = true
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var files []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), FileSuffix) {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	groups := map[PoolKey]*PoolAgg{}
	tasks := 0
	for _, path := range files {
		raw, err := readSource(path)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) == 0 {
			continue
		}
		// First non-blank line is the header; it must be the sixteen
		// SPEC-SWARM rule 12 columns in order.
		hi := -1
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			hi = i
			break
		}
		if hi < 0 {
			continue
		}
		head := strings.Split(strings.TrimRight(lines[hi], "\r"), "\t")
		if wrongColumn(head) != "" {
			continue
		}
		idx := map[string]int{}
		for i, c := range head {
			if _, seen := idx[c]; !seen {
				idx[c] = i
			}
		}
		get := func(row []string, name string) string {
			i, has := idx[name]
			if !has || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		for _, l := range lines[hi+1:] {
			if strings.TrimSpace(l) == "" {
				continue
			}
			row := strings.Split(strings.TrimRight(l, "\r"), "\t")
			started := get(row, "started")
			st, err := time.Parse(time.RFC3339, started)
			if err != nil {
				continue
			}
			if haveSince && st.Before(sinceT) {
				continue
			}
			day := st.UTC().Format(dayLayout)
			provider := get(row, "provider")
			if provider == "" {
				provider = Dash
			}
			model := get(row, "model")
			if model == "" {
				model = "unknown"
			}
			repo := get(row, "repo")
			if repo == "" {
				repo = "unknown"
			}
			k := PoolKey{Day: day, Provider: provider, Model: model, Repo: repo}
			a, ok := groups[k]
			if !ok {
				a = &PoolAgg{}
				groups[k] = a
			}
			if v, err := strconv.ParseInt(get(row, "tokens_in"), 10, 64); err == nil {
				a.In += v
				a.HasIn = true
			}
			if v, err := strconv.ParseInt(get(row, "tokens_out"), 10, 64); err == nil {
				a.Out += v
				a.HasOut = true
			}
			if v, err := strconv.ParseInt(get(row, "cache_write"), 10, 64); err == nil {
				a.Cw += v
				a.HasCw = true
			}
			if v, err := strconv.ParseInt(get(row, "cache_read"), 10, 64); err == nil {
				a.Cr += v
				a.HasCr = true
			}
			if v, err := strconv.ParseInt(get(row, "reasoning"), 10, 64); err == nil {
				a.Rsn += v
				a.HasRsn = true
			}
			if micro, ok := ParseMicro(get(row, "usd")); ok {
				a.UsdMicro += micro
			} else {
				a.UsdUnknown++
			}
			a.Tasks++
			tasks++
		}
	}
	return groups, tasks, nil
}

// PoolCell renders one token cell: the sum, or - when no input reported it.
func PoolCell(n int64, has bool) string {
	if !has {
		return Dash
	}
	return strconv.FormatInt(n, 10)
}

// PoolUsd renders the usd cell: - when any input was unknown, else dollars.
func PoolUsd(micro int64, unknown int) string {
	if unknown > 0 {
		return Dash
	}
	return Usd(micro)
}

// SortedPoolKeys returns the group keys in ledger order.
func SortedPoolKeys(groups map[PoolKey]*PoolAgg) []PoolKey {
	out := make([]PoolKey, 0, len(groups))
	for k := range groups {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// PoolDays counts distinct days in the groups.
func PoolDays(groups map[PoolKey]*PoolAgg) int {
	days := map[string]bool{}
	for k := range groups {
		days[k.Day] = true
	}
	return len(days)
}

// WritePoolLedger upserts the groups into the ledger, replacing any existing
// row for the same (day, provider, model, repo) key so a second run changes
// nothing. The header must match FoldPoolColumns.
func WritePoolLedger(path string, groups map[PoolKey]*PoolAgg) error {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	var kept []string
	if raw, err := os.ReadFile(path); err == nil {
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) > 0 && lines[0] != strings.Join(FoldPoolColumns, "\t") {
			return &poolHeaderError{got: lines[0]}
		}
		for _, l := range lines[1:] {
			if strings.TrimSpace(l) == "" {
				continue
			}
			f := strings.Split(l, "\t")
			if len(f) >= 4 {
				k := PoolKey{Day: f[0], Provider: f[1], Model: f[2], Repo: f[3]}
				if _, ok := groups[k]; ok {
					continue
				}
			}
			kept = append(kept, l)
		}
	}
	// Keep non-folded rows sorted with the folded ones: the ledger is one
	// sorted table, so a second run is byte-identical.
	// Build folded lines, merge, sort.
	folded := map[PoolKey]string{}
	for k, a := range groups {
		folded[k] = strings.Join([]string{
			k.Day, k.Provider, k.Model, k.Repo,
			strconv.Itoa(a.Tasks),
			PoolCell(a.In, a.HasIn),
			PoolCell(a.Out, a.HasOut),
			PoolCell(a.Cw, a.HasCw),
			PoolCell(a.Cr, a.HasCr),
			PoolCell(a.Rsn, a.HasRsn),
			PoolUsd(a.UsdMicro, a.UsdUnknown),
			PoolSource,
		}, "\t")
	}
	merged := make([]string, 0, len(kept)+len(folded))
	merged = append(merged, kept...)
	for _, l := range folded {
		merged = append(merged, l)
	}
	sort.Strings(merged)
	out := []string{strings.Join(FoldPoolColumns, "\t")}
	out = append(out, merged...)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type poolHeaderError struct{ got string }

func (e *poolHeaderError) Error() string {
	return "the ledger's header is " + strconv.Quote(e.got) + "; it wants " + strconv.Quote(strings.Join(FoldPoolColumns, "\t"))
}
