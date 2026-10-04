package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// An api friend's usage (the owner, 2026-10-04 4:41 PM and after): a friend at API rates
// (config.BillingAPI) runs her cards in OpenCode, which keeps every session's tokens and
// cost in its own store. friend sync reads a card's sessions there as it finishes the card
// (FinishReq.Usage), and costs retier reads them for the cards finished before, so her
// work is priced in dollars under its model's tier as a fleet route's is. A card's sessions
// are those the friend ran in her working directory, while the card was hers, whose title
// names the card ("Freddy one-shot <work card>", "Card: <primary> (2nd) ..."): the
// primary's id as a whole word. Nothing is estimated: a card with no such session has no
// usage, and the caller names it.

// sessionUsageSQL is the one statement this reader runs, read-only, against an OpenCode
// store: the sessions of the directories dirs created in the window [fromMs, toMs] whose title holds like,
// each with its title, model, cost (ten places, so it reads back as a decimal) and token
// totals. dir and like are quoted as SQL strings.
func sessionUsageSQL(dirs []string, like string, fromMs, toMs int64) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	in := make([]string, len(dirs))
	for i, d := range dirs {
		in[i] = q(d)
	}
	return `SELECT title, COALESCE(model, ''), printf('%.10f', cost), tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write ` +
		`FROM session WHERE directory IN (` + strings.Join(in, ", ") + `)` +
		` AND time_created >= ` + strconv.FormatInt(fromMs, 10) + ` AND time_created <= ` + strconv.FormatInt(toMs, 10) +
		` AND instr(title, ` + q(like) + `) > 0`
}

// friendStores are the OpenCode stores a friend's sessions may be in: her working
// directory's data home, then home's (~/.local/share/opencode), each once.
func friendStores(dir, home string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range append(swarm.OpenCodeStoreLocations(dir), filepath.Join(home, ".local", "share", "opencode", "opencode.db")) {
		if home == "" && strings.HasPrefix(p, ".local") || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// titleNames says the session title names the card: its primary's id as a whole word,
// the characters on either side none an id holds (ValidCardID: letters, digits, -, _).
func titleNames(title, primary string) bool {
	word := func(b byte) bool {
		return b == '-' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	for i := 0; ; {
		j := strings.Index(title[i:], primary)
		if j < 0 {
			return false
		}
		at, end := i+j, i+j+len(primary)
		if (at == 0 || !word(title[at-1])) && (end == len(title) || !word(title[end])) {
			return true
		}
		i = at + 1
	}
}

// friendSessionUsage is the usage line of a card's sessions (cardcost.ParseUsage's words:
// the token totals, provider/model and the harness's cost), summed over every store; ""
// when no session names it. stores that do not exist are skipped; a store that cannot be
// read is an error, never an absence.
func friendSessionUsage(ctx context.Context, stores []string, dir, primary string, from, to time.Time) (string, error) {
	dirs := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		dirs = append(dirs, real) // OpenCode keeps the directory it ran in, links resolved
	}
	sql := sessionUsageSQL(dirs, primary, from.Add(-5*time.Second).UnixMilli(), to.Add(5*time.Second).UnixMilli())
	var tokens [5]int64
	cost := new(big.Rat)
	model, n := "", 0
	for _, path := range stores {
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			continue
		}
		rows, err := readOnlySQL(ctx, path, sql)
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			if len(row) != 8 || !titleNames(row[0], primary) {
				continue
			}
			n++
			if m := openCodeModel(row[1]); m != "" && model == "" {
				model = m
			}
			if c, ok := new(big.Rat).SetString(row[2]); ok {
				cost.Add(cost, c)
			}
			for i := range tokens {
				v, _ := strconv.ParseInt(row[3+i], 10, 64)
				tokens[i] += v
			}
		}
	}
	if n == 0 {
		return "", nil
	}
	words := []string{"input=" + strconv.FormatInt(tokens[0], 10), "output=" + strconv.FormatInt(tokens[1], 10),
		"reasoning=" + strconv.FormatInt(tokens[2], 10), "cache_read=" + strconv.FormatInt(tokens[3], 10), "cache_write=" + strconv.FormatInt(tokens[4], 10)}
	if model != "" {
		words = append(words, "model="+model)
	}
	if cost.Sign() > 0 {
		words = append(words, "actual_usd="+cardcost.Text(cost), "actual_by="+cardcost.ActualByHarness)
	}
	return strings.Join(words, " "), nil
}

// openCodeModel is a session's model as provider/model, from OpenCode's JSON
// ({"id": ..., "providerID": ...}); "" when it names none.
func openCodeModel(raw string) string {
	var m struct {
		ID       string `json:"id"`
		Provider string `json:"providerID"`
	}
	if json.Unmarshal([]byte(raw), &m) != nil || m.ID == "" || m.Provider == "" {
		return ""
	}
	return m.Provider + "/" + m.ID
}

// readOnlySQL runs one statement against an SQLite file with sqlite3 -readonly, under a
// timeout, its rows split on tabs.
func readOnlySQL(ctx context.Context, path, sql string) ([][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, swarm.SQLiteBinary, "-readonly", "-list", "-separator", "\t", path, sql) // list mode: -tabs quotes a text cell
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("the OpenCode store %s could not be read with %s: %v: %s", path, swarm.SQLiteBinary, err, strings.TrimSpace(errb.String()))
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			rows = append(rows, strings.Split(line, "\t"))
		}
	}
	return rows, nil
}
